package security

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
)

type CommandGuardrailService struct {
	terminalConfiguration config.TerminalConfiguration
}

func NewCommandGuardrailService(terminalConfiguration config.TerminalConfiguration) CommandGuardrailService {
	return CommandGuardrailService{
		terminalConfiguration: terminalConfiguration,
	}
}

func (commandGuardrailService CommandGuardrailService) BuildCommandPlan(commandRequest CommandRequest) (CommandPlan, error) {
	if os.Geteuid() == 0 {
		return CommandPlan{}, errors.New("terminal execution is denied for root")
	}

	workspaceRootPath, errorValue := filepath.Abs(commandGuardrailService.terminalConfiguration.WorkspaceRootPath)
	if errorValue != nil {
		return CommandPlan{}, errorValue
	}

	workingDirectoryPath, errorValue := resolveWorkingDirectoryPath(commandRequest.WorkingDirectoryPath, workspaceRootPath)
	if errorValue != nil {
		return CommandPlan{}, errorValue
	}

	if strings.TrimSpace(commandRequest.Command) != "" || commandRequest.IsPTY {
		return commandGuardrailService.buildBashCommandPlan(commandRequest, workingDirectoryPath, workspaceRootPath)
	}

	resolvedExecutablePath, errorValue := commandGuardrailService.resolveExecutablePath(commandRequest.ExecutableName)
	if errorValue != nil {
		return CommandPlan{}, errorValue
	}

	if commandRequest.IsInteractive && !commandGuardrailService.terminalConfiguration.AllowInteractiveShell {
		return CommandPlan{}, errors.New("interactive shell is disabled")
	}

	commandPlan := CommandPlan{
		ExecutablePath:       resolvedExecutablePath,
		Arguments:            append([]string{}, commandRequest.Arguments...),
		WorkingDirectoryPath: workingDirectoryPath,
		EnvironmentVariables: sanitizeEnvironmentVariables(commandRequest.EnvironmentVariables, workspaceRootPath),
		Timeout:              time.Duration(commandGuardrailService.timeoutSecond(commandRequest.TimeoutSecond)) * time.Second,
		OutputMaximumBytes:   commandRequest.OutputMaximumBytes,
		ExecutionIdentity:    commandRequest.ExecutionIdentity,
	}

	if commandGuardrailService.terminalConfiguration.Mode == "sandbox" {
		if commandGuardrailService.sandboxProvider() != "bubblewrap" {
			return CommandPlan{}, errors.New("only bubblewrap sandbox provider is supported in v1")
		}
		return commandGuardrailService.buildSandboxCommandPlan(commandPlan, workspaceRootPath)
	}

	if !commandGuardrailService.terminalConfiguration.AllowNetwork {
		return CommandPlan{}, errors.New("native execution without network is not allowed when terminal.allowNetwork is false")
	}

	return commandGuardrailService.applyPOSIXRunner(commandPlan, workspaceRootPath)
}

func (commandGuardrailService CommandGuardrailService) buildBashCommandPlan(commandRequest CommandRequest, workingDirectoryPath string, workspaceRootPath string) (CommandPlan, error) {
	if commandRequest.IsInteractive && !commandGuardrailService.terminalConfiguration.AllowInteractiveShell {
		return CommandPlan{}, errors.New("interactive shell is disabled")
	}
	resolvedExecutablePath, errorValue := commandGuardrailService.resolveExecutablePath("bash")
	if errorValue != nil {
		return CommandPlan{}, errorValue
	}
	arguments := []string{"--noprofile", "--norc", "-s"}
	if commandRequest.IsPTY {
		arguments = []string{"-i"}
	}
	commandPlan := CommandPlan{
		ExecutablePath:       resolvedExecutablePath,
		Arguments:            arguments,
		Stdin:                joinCommandInput(commandRequest.Command, commandRequest.Stdin),
		WorkingDirectoryPath: workingDirectoryPath,
		EnvironmentVariables: sanitizeEnvironmentVariables(commandRequest.EnvironmentVariables, workspaceRootPath),
		Timeout:              time.Duration(commandGuardrailService.timeoutSecond(commandRequest.TimeoutSecond)) * time.Second,
		OutputMaximumBytes:   commandRequest.OutputMaximumBytes,
		IsPTY:                commandRequest.IsPTY,
		ExecutionIdentity:    commandRequest.ExecutionIdentity,
	}
	return commandGuardrailService.applyPOSIXRunner(commandPlan, workspaceRootPath)
}

func (commandGuardrailService CommandGuardrailService) applyPOSIXRunner(commandPlan CommandPlan, workspaceRootPath string) (CommandPlan, error) {
	if strings.TrimSpace(commandGuardrailService.terminalConfiguration.POSIXHelperPath) == "" {
		return commandPlan, nil
	}
	if strings.TrimSpace(commandPlan.ExecutionIdentity.UserName) == "" {
		return commandPlan, nil
	}

	resolvedIdentity, errorValue := ResolveExecutionIdentity(commandPlan.ExecutionIdentity)
	if errorValue != nil {
		return CommandPlan{}, errorValue
	}
	targetWorkingDirectoryPath := commandPlan.WorkingDirectoryPath
	helperArguments := []string{
		"exec",
		"--uid", formatUnsignedID(resolvedIdentity.UserID),
		"--gid", formatUnsignedID(resolvedIdentity.GroupID),
		"--groups", joinUnsignedIDs(resolvedIdentity.SupplementaryGroupIDs),
		"--cwd", targetWorkingDirectoryPath,
		"--",
		commandPlan.ExecutablePath,
	}
	helperArguments = append(helperArguments, commandPlan.Arguments...)
	commandPlan.ExecutablePath = commandGuardrailService.terminalConfiguration.POSIXHelperPath
	commandPlan.Arguments = helperArguments
	commandPlan.WorkingDirectoryPath = workspaceRootPath
	commandPlan.EnvironmentVariables = applyPOSIXEnvironment(commandPlan.EnvironmentVariables, resolvedIdentity)
	commandPlan.ExecutionIdentity = resolvedIdentity
	return commandPlan, nil
}

func (commandGuardrailService CommandGuardrailService) sandboxProvider() string {
	if strings.TrimSpace(commandGuardrailService.terminalConfiguration.SandboxProvider) == "" {
		return "bubblewrap"
	}

	return commandGuardrailService.terminalConfiguration.SandboxProvider
}

func (commandGuardrailService CommandGuardrailService) timeoutSecond(requestedTimeoutSecond int) int {
	if requestedTimeoutSecond > 0 && requestedTimeoutSecond < commandGuardrailService.maxTimeoutSecond() {
		return requestedTimeoutSecond
	}
	if commandGuardrailService.terminalConfiguration.TimeoutSecond <= 0 {
		return 120
	}

	return commandGuardrailService.terminalConfiguration.TimeoutSecond
}

func (commandGuardrailService CommandGuardrailService) maxTimeoutSecond() int {
	if commandGuardrailService.terminalConfiguration.TimeoutSecond <= 0 {
		return 120
	}
	return commandGuardrailService.terminalConfiguration.TimeoutSecond
}

func (commandGuardrailService CommandGuardrailService) resolveExecutablePath(executableName string) (string, error) {
	if strings.TrimSpace(executableName) == "" {
		return "", errors.New("executableName is required")
	}

	if filepath.IsAbs(executableName) {
		return filepath.EvalSymlinks(executableName)
	}

	for _, searchPath := range strings.Split(CanonicalRuntimePATH, ":") {
		candidatePath := filepath.Join(searchPath, executableName)
		information, errorValue := os.Stat(candidatePath)
		if errorValue == nil && !information.IsDir() {
			return filepath.EvalSymlinks(candidatePath)
		}
	}

	return "", errors.New("executable was not found in canonical runtime PATH")
}

func joinCommandInput(command string, stdin string) string {
	if strings.TrimSpace(command) == "" {
		return stdin
	}
	if strings.TrimSpace(stdin) == "" {
		return command
	}
	if strings.HasSuffix(command, "\n") {
		return command + stdin
	}
	return command + "\n" + stdin
}

func resolveWorkingDirectoryPath(workingDirectoryPath string, workspaceRootPath string) (string, error) {
	if strings.TrimSpace(workingDirectoryPath) == "" {
		return workspaceRootPath, nil
	}

	resolvedPath, errorValue := resolvePathArgument(workingDirectoryPath, workspaceRootPath)
	if errorValue != nil {
		return "", errorValue
	}
	return resolvedPath, nil
}

func resolvePathArgument(pathArgument string, basePath string) (string, error) {
	if filepath.IsAbs(pathArgument) {
		return filepath.Clean(pathArgument), nil
	}

	return filepath.Abs(filepath.Join(basePath, pathArgument))
}

func isWithinRootPath(rootPath string, targetPath string) bool {
	relativePath, errorValue := filepath.Rel(rootPath, targetPath)
	if errorValue != nil {
		return false
	}
	return relativePath == "." || (!strings.HasPrefix(relativePath, "..") && relativePath != "..")
}

func sanitizeEnvironmentVariables(environmentVariables map[string]string, workspaceRootPath string) map[string]string {
	sanitizedEnvironmentVariables := map[string]string{
		"HOME": workspaceRootPath,
		"TERM": "xterm-256color",
		"LANG": "C.UTF-8",
	}

	allowedTerminalEnvironmentName := map[string]bool{
		"TERM":                  true,
		"LANG":                  true,
		"LC_ALL":                true,
		"COLORTERM":             true,
		"CAPABILITY_BRIDGE_URL": true,
	}

	for name, value := range environmentVariables {
		if IsWorkspaceManagedEnvironmentName(name) || allowedTerminalEnvironmentName[name] {
			sanitizedEnvironmentVariables[name] = value
		}
	}

	return enforceCanonicalRuntimePATH(sanitizedEnvironmentVariables)
}
