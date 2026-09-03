package cliharness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/turnbriefing"
	"github.com/yeomyeonggeori/blueclaw/internal/turnoutcome"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

const (
	toolCatalogServerName           = "blueclaw"
	toolCatalogTokenEnvironmentName = "BLUECLAW_TOOL_CATALOG_TOKEN"
)

type ToolCatalogPublisher interface {
	PublishToolCatalog(requesterToolSet mcpserver.RequesterToolSet) (endpointURL string, bearerToken string, revoke func(), errorValue error)
}

type AgentCommand struct {
	Path                            string
	HarnessName                     string
	SessionArguments                func(sessionID string, isResuming bool) []string
	RequiresCapturedSessionIdentity bool
	ParseAgentOutput                func(standardOutput string) (finishMessage string, capturedSessionIdentity string)
	PromptArguments                 []string
	ToolCatalogArguments            func(toolCatalogConfigurationPath string) []string
	ToolCatalogInlineArguments      func(endpointURL string, bearerTokenEnvironmentName string) []string
	PromptArgumentsWithPrompt       func(prompt string) []string
	ToolCatalogWorkspaceFile        func(endpointURL string, bearerToken string) (relativePath string, document any)
	Environment                     []string
}

type RequesterProcessRunner interface {
	Requester(context.Context, security.WorkspaceActorRequest) (security.WorkspaceActor, error)
}

type conversationState struct {
	hasStartedTurn    bool
	capturedSessionID string
}

type Harness struct {
	agentCommand                   AgentCommand
	toolCatalogPublisher           ToolCatalogPublisher
	taskRunStore                   taskstate.TaskRunStore
	requesterProcessRunner         RequesterProcessRunner
	workspaceRootPath              string
	outcomeClassifier              turnoutcome.Classifier
	agentTimeoutSecond             int
	conversationStateMutex         sync.Mutex
	conversationStateByIdentityKey map[string]*conversationState
	instructionBundleLoader        func() agentcontract.InstructionBundle
}

func New(agentCommand AgentCommand, toolCatalogPublisher ToolCatalogPublisher, taskRunStore taskstate.TaskRunStore) *Harness {
	return &Harness{
		agentCommand:                   agentCommand,
		toolCatalogPublisher:           toolCatalogPublisher,
		taskRunStore:                   taskRunStore,
		agentTimeoutSecond:             600,
		conversationStateByIdentityKey: map[string]*conversationState{},
	}
}

func (harness *Harness) UseOutcomeClassifier(outcomeClassifier turnoutcome.Classifier) {
	harness.outcomeClassifier = outcomeClassifier
}

func (harness *Harness) UseRequesterProcessRunner(requesterProcessRunner RequesterProcessRunner, workspaceRootPath string) {
	harness.requesterProcessRunner = requesterProcessRunner
	harness.workspaceRootPath = workspaceRootPath
}

func (harness *Harness) RunTurn(ctx context.Context, request agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	if strings.TrimSpace(harness.agentCommand.Path) == "" {
		return agentcontract.AgentTurnResult{}, errors.New("cli harness needs an agent command to run")
	}
	if strings.TrimSpace(request.RequesterPersonID) == "" {
		return agentcontract.AgentTurnResult{}, errors.New("cli harness refuses a turn with no requester, because tools execute as the requester")
	}
	identityKey := conversationIdentityKey(request)
	harnessSession := harness.harnessSessionForTurn(request, identityKey)
	succeededToolRecorder := &turnoutcome.SucceededToolRecorder{}
	endpointURL, bearerToken, revokeToolCatalog, errorValue := harness.toolCatalogPublisher.PublishToolCatalog(mcpserver.RequesterToolSet{
		ObserveToolInvocation: succeededToolRecorder.Observe,
		RequesterPersonID:     request.RequesterPersonID,
		TaskRunID:             request.ExistingTaskRunID,
		ToolSet:               request.ToolSet,
		ResponseLanguage:      request.ResponseLanguage,
		Prompt:                request.Prompt,
		HarnessSession:        harnessSession,
		ToolAudience:          mcpserver.ToolAudienceSelfEquipped,
	})
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	defer revokeToolCatalog()

	configurationPath := ""
	if harness.agentCommand.ToolCatalogWorkspaceFile == nil {
		configurationPath, errorValue = writeToolCatalogConfiguration(endpointURL, bearerToken)
		if errorValue != nil {
			return agentcontract.AgentTurnResult{}, errorValue
		}
		defer os.Remove(configurationPath)
	}

	arguments := append([]string{}, harness.agentCommand.PromptArguments...)
	if harness.agentCommand.SessionArguments != nil && harnessSession.IsResumable {
		arguments = append(arguments, harness.agentCommand.SessionArguments(harnessSession.SessionID, harness.isResumingTurn(request, identityKey))...)
	}
	if harness.agentCommand.ToolCatalogArguments != nil && configurationPath != "" {
		arguments = append(arguments, harness.agentCommand.ToolCatalogArguments(configurationPath)...)
	}
	if harness.agentCommand.ToolCatalogInlineArguments != nil {
		arguments = append(arguments, harness.agentCommand.ToolCatalogInlineArguments(endpointURL, toolCatalogTokenEnvironmentName)...)
	}
	agentPrompt := harness.promptForTurn(request)
	agentStandardInput := agentPrompt
	if harness.agentCommand.PromptArgumentsWithPrompt != nil {
		arguments = append(arguments, harness.agentCommand.PromptArgumentsWithPrompt(agentPrompt)...)
		agentStandardInput = ""
	}
	if harness.requesterProcessRunner != nil {
		return harness.runAsRequester(ctx, request, identityKey, arguments, agentStandardInput, endpointURL, bearerToken, succeededToolRecorder)
	}
	if errorValue := harness.writeToolCatalogWorkspaceFile(request.WorkspaceRootPath, endpointURL, bearerToken, nil); errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	command := exec.CommandContext(ctx, harness.agentCommand.Path, arguments...)
	command.Dir = request.WorkspaceRootPath
	command.Stdin = strings.NewReader(agentStandardInput)
	command.Env = append(harness.commandEnvironment(), toolCatalogTokenEnvironmentName+"="+bearerToken)
	standardOutput := &bytes.Buffer{}
	standardError := &bytes.Buffer{}
	command.Stdout = standardOutput
	command.Stderr = standardError
	if errorValue := command.Run(); errorValue != nil {
		return agentcontract.AgentTurnResult{}, errors.New(strings.TrimSpace(standardError.String()) + " (" + errorValue.Error() + ")")
	}
	return harness.turnResult(ctx, request, harness.completeTurn(identityKey, standardOutput.String()), succeededToolRecorder), nil
}

func (harness *Harness) turnResult(ctx context.Context, request agentcontract.AgentTurnRequest, finishMessage string, succeededToolRecorder *turnoutcome.SucceededToolRecorder) agentcontract.AgentTurnResult {
	taskStatus, failureReason := harness.outcomeForEndedTurn(ctx, request, finishMessage, succeededToolRecorder.SucceededToolNames())
	taskRun := agentcontract.TaskRun{Status: taskStatus, FailureReason: failureReason}
	if harness.taskRunStore != nil && strings.TrimSpace(request.ExistingTaskRunID) != "" {
		if existingTaskRun, isFound := harness.taskRunStore.FindTaskRun(request.ExistingTaskRunID); isFound {
			taskRun = existingTaskRun
		}
	}
	return agentcontract.AgentTurnResult{TaskRun: taskRun, FinishMessage: finishMessage, UserNotice: finishMessage}
}

func (harness *Harness) outcomeForEndedTurn(ctx context.Context, request agentcontract.AgentTurnRequest, finishMessage string, succeededToolNames []string) (agentcontract.TaskStatus, string) {
	if !harness.outcomeClassifier.IsConfigured() {
		return agentcontract.TaskStatusCompleted, ""
	}
	verdict, errorValue := harness.outcomeClassifier.Classify(ctx, request.Prompt, finishMessage, succeededToolNames)
	if errorValue != nil {
		return agentcontract.TaskStatusFailed, "the runtime could not determine what this turn achieved: " + errorValue.Error()
	}
	return verdict.Status, verdict.Reason
}

func writeToolCatalogConfiguration(endpointURL string, bearerToken string) (string, error) {
	document, errorValue := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			toolCatalogServerName: map[string]any{
				"type":    "http",
				"url":     endpointURL,
				"headers": map[string]string{"Authorization": "Bearer " + bearerToken},
			},
		},
	})
	if errorValue != nil {
		return "", errorValue
	}
	configurationFile, errorValue := os.CreateTemp("", "blueclaw-tool-catalog-*.json")
	if errorValue != nil {
		return "", errorValue
	}
	defer configurationFile.Close()
	if _, errorValue := configurationFile.Write(document); errorValue != nil {
		return "", errorValue
	}
	return configurationFile.Name(), nil
}

func ClaudeCodeAgentCommand(commandPath string) AgentCommand {
	return AgentCommand{
		Path:        commandPath,
		HarnessName: "claude-code",
		SessionArguments: func(sessionID string, isResuming bool) []string {
			if isResuming {
				return []string{"--resume", sessionID}
			}
			return []string{"--session-id", sessionID}
		},
		PromptArguments: []string{"--print", "--strict-mcp-config", "--allowedTools", "mcp__" + toolCatalogServerName},
		ToolCatalogArguments: func(toolCatalogConfigurationPath string) []string {
			return []string{"--mcp-config", toolCatalogConfigurationPath}
		},
	}
}

func (harness *Harness) writeToolCatalogWorkspaceFile(workspaceRootPath string, endpointURL string, bearerToken string, requesterActor security.WorkspaceActor) error {
	if harness.agentCommand.ToolCatalogWorkspaceFile == nil {
		return nil
	}
	relativePath, document := harness.agentCommand.ToolCatalogWorkspaceFile(endpointURL, bearerToken)
	encodedDocument, errorValue := json.Marshal(document)
	if errorValue != nil {
		return errorValue
	}
	configurationPath := filepath.Join(workspaceRootPath, relativePath)
	if requesterActor == nil {
		if errorValue := os.MkdirAll(filepath.Dir(configurationPath), 0o755); errorValue != nil {
			return errorValue
		}
		return os.WriteFile(configurationPath, encodedDocument, 0o600)
	}
	if errorValue := requesterActor.MkdirAll(context.Background(), filepath.Dir(configurationPath)); errorValue != nil {
		return errorValue
	}
	return requesterActor.WriteFile(context.Background(), configurationPath, encodedDocument)
}

func (harness *Harness) runAsRequester(ctx context.Context, request agentcontract.AgentTurnRequest, identityKey string, arguments []string, agentStandardInput string, endpointURL string, bearerToken string, succeededToolRecorder *turnoutcome.SucceededToolRecorder) (agentcontract.AgentTurnResult, error) {
	workspaceRootPath := harness.workspaceRootPath
	if strings.TrimSpace(workspaceRootPath) == "" {
		workspaceRootPath = request.WorkspaceRootPath
	}
	requesterActor, errorValue := harness.requesterProcessRunner.Requester(ctx, security.WorkspaceActorRequest{
		PersonAccess:      policy.PersonAccess{PersonID: request.RequesterPersonID},
		WorkspaceRootPath: workspaceRootPath,
	})
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	if errorValue := harness.writeToolCatalogWorkspaceFile(request.WorkspaceRootPath, endpointURL, bearerToken, requesterActor); errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	commandResult, errorValue := requesterActor.Run(ctx, security.CommandRequest{
		ExecutableName:       harness.agentCommand.Path,
		Arguments:            arguments,
		Stdin:                agentStandardInput,
		WorkingDirectoryPath: request.WorkspaceRootPath,
		TimeoutSecond:        harness.agentTimeoutSecond,
		OutputMaximumBytes:   1 << 20,
	})
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	if commandResult.ExitCode != 0 {
		return agentcontract.AgentTurnResult{}, errors.New(strings.TrimSpace(commandResult.Stderr))
	}
	return harness.turnResult(ctx, request, harness.completeTurn(identityKey, commandResult.Stdout), succeededToolRecorder), nil
}

// Antigravity has no way to allow only the catalog's tools the way claude-code
// does, and its headless mode cancels any tool call rather than asking a person
// who is not there. The requester's POSIX identity is the boundary either way.
const antigravityHeadlessPermissionFlag = "--dangerously-skip-permissions"

func AntigravityAgentCommand(commandPath string) AgentCommand {
	return AgentCommand{
		Path:                            commandPath,
		HarnessName:                     "antigravity",
		RequiresCapturedSessionIdentity: true,
		SessionArguments: func(sessionID string, isResuming bool) []string {
			if isResuming {
				return []string{"--conversation", sessionID}
			}
			return nil
		},
		ParseAgentOutput: parseAntigravityAgentOutput,
		PromptArguments:  []string{"--output-format", "json", "--new-project", antigravityHeadlessPermissionFlag},
		PromptArgumentsWithPrompt: func(prompt string) []string {
			return []string{"-p", prompt}
		},
		ToolCatalogWorkspaceFile: func(endpointURL string, bearerToken string) (string, any) {
			return ".agents/mcp_config.json", map[string]any{
				"mcpServers": map[string]any{
					toolCatalogServerName: map[string]any{
						"serverUrl": endpointURL,
						"headers":   map[string]string{"Authorization": "Bearer " + bearerToken},
					},
				},
			}
		},
	}
}

func parseAntigravityAgentOutput(standardOutput string) (string, string) {
	var envelope struct {
		ConversationID string `json:"conversation_id"`
		Response       string `json:"response"`
	}
	if errorValue := json.Unmarshal([]byte(strings.TrimSpace(standardOutput)), &envelope); errorValue != nil {
		return strings.TrimSpace(standardOutput), ""
	}
	finishMessage := strings.TrimSpace(envelope.Response)
	if finishMessage == "" {
		finishMessage = strings.TrimSpace(standardOutput)
	}
	return finishMessage, envelope.ConversationID
}

func CodexAgentCommand(commandPath string) AgentCommand {
	return AgentCommand{
		Path:                            commandPath,
		HarnessName:                     "codex",
		RequiresCapturedSessionIdentity: true,
		SessionArguments: func(sessionID string, isResuming bool) []string {
			if isResuming {
				return []string{"resume", sessionID}
			}
			return nil
		},
		ParseAgentOutput: parseCodexAgentOutput,
		PromptArguments:  []string{"exec", "--json", "--sandbox", "read-only", "--skip-git-repo-check"},
		ToolCatalogArguments: func(toolCatalogConfigurationPath string) []string {
			return nil
		},
		ToolCatalogInlineArguments: func(endpointURL string, bearerTokenEnvironmentName string) []string {
			return []string{"-c", "mcp_servers." + toolCatalogServerName + "={url=" + strconv.Quote(endpointURL) + ",bearer_token_env_var=" + strconv.Quote(bearerTokenEnvironmentName) + "}"}
		},
	}
}

func parseCodexAgentOutput(standardOutput string) (string, string) {
	capturedSessionIdentity := ""
	finishMessage := ""
	for _, outputLine := range strings.Split(standardOutput, "\n") {
		trimmedLine := strings.TrimSpace(outputLine)
		if trimmedLine == "" {
			continue
		}
		var event map[string]any
		if errorValue := json.Unmarshal([]byte(trimmedLine), &event); errorValue != nil {
			continue
		}
		switch event["type"] {
		case "thread.started":
			if threadID, isString := event["thread_id"].(string); isString {
				capturedSessionIdentity = threadID
			}
		case "item.completed":
			if item, isMap := event["item"].(map[string]any); isMap && item["type"] == "agent_message" {
				if messageText, isString := item["text"].(string); isString {
					finishMessage = messageText
				}
			}
		}
	}
	if finishMessage == "" {
		finishMessage = strings.TrimSpace(standardOutput)
	}
	return finishMessage, capturedSessionIdentity
}

// A turn that runs inside the requester's POSIX identity never reaches here: that path
// builds its environment from the resolved identity like every other workspace command.
// This one is for a developer running a real agent CLI against their own credentials.
func (harness *Harness) commandEnvironment() []string {
	return append([]string{}, harness.agentCommand.Environment...)
}

func (harness *Harness) harnessSessionForTurn(request agentcontract.AgentTurnRequest, identityKey string) mcpserver.HarnessSession {
	if harness.agentCommand.SessionArguments == nil {
		return mcpserver.HarnessSession{HarnessName: harness.agentCommand.HarnessName}
	}
	if harness.agentCommand.RequiresCapturedSessionIdentity {
		capturedSessionID := harness.capturedSessionIdentity(identityKey)
		if capturedSessionID == "" {
			return mcpserver.HarnessSession{HarnessName: harness.agentCommand.HarnessName}
		}
		return mcpserver.HarnessSession{
			HarnessName: harness.agentCommand.HarnessName,
			SessionID:   capturedSessionID,
			IsResumable: true,
		}
	}
	return mcpserver.HarnessSession{
		HarnessName: harness.agentCommand.HarnessName,
		SessionID:   harnessSessionIdentity(identityKey),
		IsResumable: true,
	}
}

func (harness *Harness) isResumingTurn(request agentcontract.AgentTurnRequest, identityKey string) bool {
	if request.IsApprovalContinuation || request.IsRuntimeRestartResume {
		return true
	}
	return harness.hasConversationStarted(identityKey)
}

func (harness *Harness) hasConversationStarted(identityKey string) bool {
	harness.conversationStateMutex.Lock()
	defer harness.conversationStateMutex.Unlock()
	state := harness.conversationStateByIdentityKey[identityKey]
	return state != nil && state.hasStartedTurn
}

func (harness *Harness) capturedSessionIdentity(identityKey string) string {
	harness.conversationStateMutex.Lock()
	defer harness.conversationStateMutex.Unlock()
	state := harness.conversationStateByIdentityKey[identityKey]
	if state == nil {
		return ""
	}
	return state.capturedSessionID
}

func (harness *Harness) completeTurn(identityKey string, rawOutput string) string {
	finishMessage := strings.TrimSpace(rawOutput)
	capturedSessionIdentity := ""
	if harness.agentCommand.ParseAgentOutput != nil {
		finishMessage, capturedSessionIdentity = harness.agentCommand.ParseAgentOutput(rawOutput)
	}
	harness.conversationStateMutex.Lock()
	defer harness.conversationStateMutex.Unlock()
	state := harness.conversationStateByIdentityKey[identityKey]
	if state == nil {
		state = &conversationState{}
		harness.conversationStateByIdentityKey[identityKey] = state
	}
	state.hasStartedTurn = true
	if capturedSessionIdentity != "" {
		state.capturedSessionID = capturedSessionIdentity
	}
	return finishMessage
}

func conversationIdentityKey(request agentcontract.AgentTurnRequest) string {
	seed := strings.TrimSpace(request.ExistingTaskRunID)
	if seed == "" {
		seed = strings.TrimSpace(request.RequesterPersonID) + "|" + strings.TrimSpace(request.ConversationID) + "|" + request.TurnStartedAt.UTC().Format(time.RFC3339Nano)
	}
	return seed
}

func harnessSessionIdentity(identityKey string) string {
	digest := sha256.Sum256([]byte(identityKey))
	return formatDigestAsUUID(digest)
}

func formatDigestAsUUID(digest [32]byte) string {
	identityBytes := digest[:16]
	identityBytes[6] = (identityBytes[6] & 0x0f) | 0x40
	identityBytes[8] = (identityBytes[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(identityBytes)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func (harness *Harness) promptForTurn(request agentcontract.AgentTurnRequest) string {
	sections := []string{}
	if preamble := turnbriefing.Preamble(request, harness.instructionPrompt()); preamble != "" {
		sections = append(sections, preamble)
	}
	sections = append(sections, request.Prompt)
	if harness.taskRunStore != nil && strings.TrimSpace(request.ExistingTaskRunID) != "" {
		if declinedCallNote := approvalgate.DeclinedCallNote(harness.taskRunStore.ListTaskEvent(request.ExistingTaskRunID)); declinedCallNote != "" {
			sections = append(sections, declinedCallNote)
		}
	}
	return strings.Join(sections, "\n\n")
}

func (harness *Harness) instructionPrompt() string {
	if harness.instructionBundleLoader == nil {
		return ""
	}
	return harness.instructionBundleLoader().Prompt
}

func (harness *Harness) UseInstructionBundleLoader(instructionBundleLoader func() agentcontract.InstructionBundle) {
	harness.instructionBundleLoader = instructionBundleLoader
}
