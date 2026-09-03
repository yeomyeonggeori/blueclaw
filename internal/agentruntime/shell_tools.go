package agentruntime

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

var terminalRunHeartbeatInterval = 60 * time.Second

func (input terminalRunToolInput) commandRequest() security.CommandRequest {
	return security.CommandRequest{
		Command:              input.Command,
		WorkingDirectoryPath: input.WorkingDirectoryPath,
		TimeoutSecond:        input.TimeoutSecond,
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerTerminalTools(toolRegistry *toolcontract.ToolSet, handlerContext toolHandlerContext) {
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[terminalRunToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        "shell",
			Description: "Run one command inside the requester workspace.",
			RecoveryCard: toolcontract.ToolRecoveryCard{
				Does:       "Runs workspace commands, build scripts, render checks, or tests.",
				Produces:   "Command stdout, stderr, exit status, and runtime diagnostics.",
				SideEffect: "workspace_write",
				UseWhen:    "You need to execute a toolchain command, build, render, test, list files, or inspect environment state.",
				AvoidWhen:  "A dedicated bundled skill script or typed capability tool can perform the action more directly.",
			},
			InputSchema: terminalRunInputSchema,
		},
		Handler: func(toolContext context.Context, input terminalRunToolInput) (toolcontract.ToolResult, error) {
			return toolCatalogBuilder.runTerminalRunTool(toolContext, input, handlerContext)
		},
		Result: toolcontract.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) runTerminalRunTool(toolContext context.Context, input terminalRunToolInput, handlerContext toolHandlerContext) (toolcontract.ToolResult, error) {
	if errorValue := validateTerminalRunInput(input); errorValue != nil {
		result := toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "shell", errorValue.Error())
		return normalizedTerminalRunFailure(result), nil
	}
	result, errorValue := toolCatalogBuilder.runTerminalTool(toolContext, input.commandRequest(), handlerContext)
	return normalizedTerminalRunFailure(result), errorValue
}

func (toolCatalogBuilder *ToolCatalogBuilder) runTerminalTool(toolContext context.Context, input security.CommandRequest, handlerContext toolHandlerContext) (toolcontract.ToolResult, error) {
	if toolCatalogBuilder.terminalService == nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureDependencyUnavailable, toolcontract.FailureCodes.Unavailable, "shell", "terminal service is unavailable"), nil
	}
	requesterHomePath := toolCatalogBuilder.requesterHomePath(handlerContext.request)
	taskRunID := toolcontract.TaskRunIDFromContext(toolContext)
	input.Command = toolCatalogBuilder.resolveAgentWorkspaceReferences(input.Command)
	input.EnvironmentVariables = toolCatalogBuilder.terminalEnvironmentVariables(input.EnvironmentVariables, requesterHomePath, taskRunID)
	input.WorkingDirectoryPath = toolCatalogBuilder.terminalWorkingDirectoryPath(input.WorkingDirectoryPath, handlerContext.request, requesterHomePath)
	actorStartedAt := time.Now()
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	slog.Info("shell actor acquired", "durationMs", time.Since(actorStartedAt).Milliseconds())
	workingDirectoryStartedAt := time.Now()
	if errorValue := workspaceActor.MkdirAll(toolContext, input.WorkingDirectoryPath); errorValue != nil {
		return actorToolFailure("mkdir_all", "shell_working_directory", input.WorkingDirectoryPath, errorValue), nil
	}
	slog.Info("shell working directory prepared", "durationMs", time.Since(workingDirectoryStartedAt).Milliseconds())
	materializeStartedAt := time.Now()
	if toolFailure := materializeTerminalRuntimeDirectories(toolContext, workspaceActor, requesterHomePath, input.EnvironmentVariables); toolFailure != nil {
		return *toolFailure, nil
	}
	slog.Info("shell runtime directories materialized", "durationMs", time.Since(materializeStartedAt).Milliseconds())
	input.ExecutionIdentity = toolCatalogBuilder.executionIdentityForRequester(handlerContext.request)
	runStartedAt := time.Now()
	stopHeartbeat := toolCatalogBuilder.startTerminalRunHeartbeat(toolContext, input.Command)
	commandResult, errorValue := workspaceActor.Run(toolContext, input)
	stopHeartbeat()
	slog.Info("shell command completed", "durationMs", time.Since(runStartedAt).Milliseconds(), "exitCode", commandResult.ExitCode, "timedOut", commandResult.TimedOut, "signal", commandResult.Signal)
	content := marshalToolResult(commandResult)
	if errorValue != nil {
		if runtimePathFailure := terminalRuntimePathFailure(input, commandResult, content); runtimePathFailure != nil {
			return *runtimePathFailure, nil
		}
		document := terminalCommandResult(commandResult, false)
		content = marshalToolResult(document)
		return toolcontract.ToolFailureWithOutput(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "shell", content, json.RawMessage(content)), nil
	}
	document := terminalCommandResult(commandResult, true)
	content = marshalToolResult(document)
	return toolcontract.ToolSuccessData(content, json.RawMessage(content)), nil
}

func terminalCommandResult(commandResult security.CommandResult, isCompleted bool) terminalCommandResultDocument {
	return terminalCommandResultDocument{
		Mode:          terminalRunModeCommand,
		Completed:     isCompleted && commandResult.ExitCode == 0 && !commandResult.TimedOut,
		ExitCode:      commandResult.ExitCode,
		Stdout:        commandResult.Stdout,
		Stderr:        commandResult.Stderr,
		TimedOut:      commandResult.TimedOut,
		Signal:        commandResult.Signal,
		OutputTrimmed: commandResult.OutputTrimmed,
	}
}

func normalizedTerminalRunFailure(result toolcontract.ToolResult) toolcontract.ToolResult {
	if !result.Failed() {
		return result
	}
	document := terminalFailureDocument(result)
	document["mode"] = terminalRunModeCommand
	document["completed"] = false
	completeTerminalCommandFailureDocument(document, result)
	data := json.RawMessage(marshalToolResult(document))
	result.Output.Data = data
	return result
}

func terminalFailureDocument(result toolcontract.ToolResult) map[string]any {
	document := map[string]any{}
	_ = json.Unmarshal(result.Output.Data, &document)
	return document
}

func completeTerminalCommandFailureDocument(document map[string]any, result toolcontract.ToolResult) {
	setTerminalFailureDefault(document, "exitCode", -1)
	setTerminalFailureDefault(document, "stdout", "")
	setTerminalFailureDefault(document, "stderr", terminalFailureSummary(result))
	setTerminalFailureDefault(document, "timedOut", false)
	setTerminalFailureDefault(document, "outputTrimmed", false)
}

func setTerminalFailureDefault(document map[string]any, fieldName string, value any) {
	if _, isFound := document[fieldName]; !isFound {
		document[fieldName] = value
	}
}

func terminalFailureSummary(result toolcontract.ToolResult) string {
	if result.Failure != nil && strings.TrimSpace(result.Failure.UserSafeSummary) != "" {
		return strings.TrimSpace(result.Failure.UserSafeSummary)
	}
	return strings.TrimSpace(result.Output.Content)
}

func (toolCatalogBuilder *ToolCatalogBuilder) startTerminalRunHeartbeat(toolContext context.Context, command string) func() {
	taskRunID := toolcontract.TaskRunIDFromContext(toolContext)
	if taskRunID == "" || toolCatalogBuilder.taskRunService == nil {
		return func() {}
	}
	commandHead := terminalCommandHead(command)
	startedAt := time.Now()
	stopChannel := make(chan struct{})
	go func() {
		heartbeatTicker := time.NewTicker(terminalRunHeartbeatInterval)
		defer heartbeatTicker.Stop()
		for {
			select {
			case <-stopChannel:
				return
			case <-heartbeatTicker.C:
				toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, taskstate.TaskEventTerminalRunHeartbeat, marshalToolResult(map[string]any{
					"elapsedSeconds": int(time.Since(startedAt).Seconds()),
					"command":        commandHead,
				}))
			}
		}
	}()
	return func() { close(stopChannel) }
}

func terminalCommandHead(command string) string {
	commandRunes := []rune(strings.TrimSpace(command))
	if len(commandRunes) <= 80 {
		return string(commandRunes)
	}
	return string(commandRunes[:80])
}

func terminalRuntimePathFailure(commandRequest security.CommandRequest, commandResult security.CommandResult, content string) *toolcontract.ToolResult {
	combinedText := strings.ToLower(commandResult.Stderr + "\n" + commandResult.Stdout + "\n" + content)
	if !strings.Contains(combinedText, "not found in $path") && !strings.Contains(combinedText, "command not found") && !strings.Contains(combinedText, "executable file not found") {
		return nil
	}
	document := json.RawMessage(marshalToolResult(map[string]any{
		"failureClass":      "shell_runtime_path",
		"command":           commandRequest.Command,
		"actualPATH":        commandRequest.EnvironmentVariables["PATH"],
		"canonicalPATH":     security.CanonicalRuntimePATH,
		"executionUser":     commandRequest.ExecutionIdentity.UserName,
		"workingDirectory":  commandRequest.WorkingDirectoryPath,
		"commandResult":     commandResult,
		"recommendedAction": "Fix Blueclaw runtime PATH propagation; do not change site source or ask the user to use external hosting.",
	}))
	result := toolcontract.ToolFailureWithOutput(toolcontract.FailureDependencyUnavailable, toolcontract.FailureCode("shell_runtime_path"), "shell_runtime_path", "terminal runtime PATH did not expose a managed executable", document)
	result.Failure.Retryable = true
	result.Failure.SafeRetry = false
	return &result
}

func (toolCatalogBuilder *ToolCatalogBuilder) requesterHomePath(request ToolCatalogRequest) string {
	identity := toolCatalogBuilder.executionIdentityForRequester(request)
	return firstNonEmptyString(identity.HomeDirectoryPath, toolCatalogBuilder.workspaceRootPath)
}

func (toolCatalogBuilder *ToolCatalogBuilder) terminalWorkingDirectoryPath(value string, request ToolCatalogRequest, requesterHomePath string) string {
	if strings.TrimSpace(value) == "" {
		return requesterHomePath
	}
	return toolCatalogBuilder.nativeRequesterPath(request, value)
}

func (toolCatalogBuilder *ToolCatalogBuilder) terminalEnvironmentVariables(environmentVariables map[string]string, requesterHomePath string, taskRunID string) map[string]string {
	mergedEnvironmentVariables := mergeWorkspaceEnvironment(environmentVariables, requesterWorkspaceEnvironment(requesterHomePath, toolCatalogBuilder.workspaceRootPath, taskRunID))
	if builtinSkillsPythonPath := strings.TrimSpace(os.Getenv("BLUECLAW_BUILTIN_SKILLS_PYTHON")); builtinSkillsPythonPath != "" {
		mergedEnvironmentVariables["BLUECLAW_BUILTIN_SKILLS_PYTHON"] = builtinSkillsPythonPath
	}
	if endpoint := strings.TrimSpace(toolCatalogBuilder.capabilityClient.Endpoint); endpoint != "" {
		mergedEnvironmentVariables["CAPABILITY_BRIDGE_URL"] = endpoint
	}
	return toolCatalogBuilder.resolveAgentWorkspaceEnvironment(mergedEnvironmentVariables)
}

func requesterWorkspaceEnvironment(requesterHomePath string, workspaceRootPath string, taskRunID string) map[string]string {
	requesterTmpPath := security.RequesterTemporaryDirectoryPath(requesterHomePath)
	runtimeRootPath := filepath.Join(requesterTmpPath, ".runtime")
	bunRuntimeRootPath := filepath.Join(runtimeRootPath, "bun")
	dependencyCachePath := filepath.Join(workspaceRootPath, "shared", "cache", "dependencies")
	taskTmpPath := security.TaskTemporaryDirectoryPath(requesterHomePath, taskRunID)
	scratchRootPath := firstNonEmptyString(taskTmpPath, runtimeRootPath)
	environmentVariables := map[string]string{
		"BLUECLAW_REQUESTER_TMP":       requesterTmpPath,
		"BLUECLAW_REQUESTER_ARTIFACTS": filepath.Join(requesterHomePath, "artifacts"),
		"BLUECLAW_DEPENDENCY_CACHE":    dependencyCachePath,
		"HOME":                         requesterHomePath,
		"PATH":                         security.CanonicalRuntimePATH,
		"TMPDIR":                       filepath.Join(scratchRootPath, "tmp"),
		"TMP":                          filepath.Join(scratchRootPath, "tmp"),
		"TEMP":                         filepath.Join(scratchRootPath, "tmp"),
		"XDG_CACHE_HOME":               filepath.Join(runtimeRootPath, "cache"),
		"XDG_CONFIG_HOME":              filepath.Join(runtimeRootPath, "config"),
		"XDG_RUNTIME_DIR":              filepath.Join(runtimeRootPath, "runtime"),
		"BUN_TMPDIR":                   filepath.Join(bunRuntimeRootPath, "tmp"),
		"BUN_INSTALL":                  filepath.Join(bunRuntimeRootPath, "install"),
		"BUN_INSTALL_CACHE_DIR":        filepath.Join(dependencyCachePath, "bun"),
		"npm_config_cache":             filepath.Join(runtimeRootPath, "npm"),
	}
	if taskTmpPath != "" {
		environmentVariables["BLUECLAW_TASK_TMP"] = taskTmpPath
	}
	return environmentVariables
}

func materializeTerminalRuntimeDirectories(ctx context.Context, workspaceActor security.WorkspaceActor, requesterHomePath string, environmentVariables map[string]string) *toolcontract.ToolResult {
	for _, directoryPath := range terminalRuntimeDirectories(requesterHomePath, environmentVariables) {
		if errorValue := workspaceActor.MkdirAll(ctx, directoryPath); errorValue != nil {
			result := actorToolFailure("mkdir_all", "shell_runtime_environment", directoryPath, errorValue)
			return &result
		}
	}
	return nil
}

func terminalRuntimeDirectories(requesterHomePath string, environmentVariables map[string]string) []string {
	seenDirectoryPaths := map[string]bool{}
	var directoryPaths []string
	for _, name := range []string{
		"BLUECLAW_TASK_TMP",
		"TMPDIR",
		"TMP",
		"TEMP",
		"XDG_CACHE_HOME",
		"XDG_CONFIG_HOME",
		"XDG_RUNTIME_DIR",
		"BUN_TMPDIR",
		"BUN_INSTALL",
		"BUN_INSTALL_CACHE_DIR",
		"npm_config_cache",
	} {
		directoryPath := filepath.Clean(strings.TrimSpace(environmentVariables[name]))
		if directoryPath == "." || !strings.HasPrefix(directoryPath, filepath.Clean(requesterHomePath)+string(filepath.Separator)) {
			continue
		}
		if seenDirectoryPaths[directoryPath] {
			continue
		}
		seenDirectoryPaths[directoryPath] = true
		directoryPaths = append(directoryPaths, directoryPath)
	}
	return directoryPaths
}
