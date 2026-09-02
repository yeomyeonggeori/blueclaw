package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/bluecollarharness"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/protocolidentity"
	"github.com/yeomyeonggeori/blueclaw/internal/runtimecontrol"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type applicationMCPRegistryCloser struct {
	closeCount int
	closeError error
}

func (closer *applicationMCPRegistryCloser) Close() error {
	closer.closeCount++
	return closer.closeError
}

func TestTaskIntakeControllerStartsUnquiesced(t *testing.T) {
	controller := runtimecontrol.NewTaskIntakeController()

	if controller.IsQuiesced() {
		t.Fatal("fresh task intake controller must start unquiesced")
	}
}

func TestCapabilityToolDescriptorsPreserveResultContracts(t *testing.T) {
	inputIntentSchema := json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"additionalProperties":false}`)
	descriptors := capabilityToolDescriptors([]config.CapabilityToolDescriptor{{
		Name:              "task_add",
		InputIntentSchema: inputIntentSchema,
		ResultContract: &config.CapabilityToolResultContract{
			Schema: json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"}},"required":["taskID"],"additionalProperties":false}`),
			Effects: []config.CapabilityResourceEffectContract{{
				ObjectType:     "task",
				Effect:         "created",
				ResultField:    "taskID",
				EffectIdentity: "id",
			}},
			EvidenceCondition: &config.EvidenceCondition{
				ResultField: "taskID",
				Equals:      json.RawMessage(`"task-1"`),
			},
		},
	}})

	if len(descriptors) != 1 || descriptors[0].ResultContract == nil {
		t.Fatalf("expected mapped result contract, got %+v", descriptors)
	}
	if string(descriptors[0].InputIntentSchema) != string(inputIntentSchema) {
		t.Fatalf("expected mapped input intent schema, got %s", descriptors[0].InputIntentSchema)
	}
	if len(descriptors[0].ResultContract.Effects) != 1 ||
		descriptors[0].ResultContract.Effects[0].ObjectType != "task" ||
		descriptors[0].ResultContract.Effects[0].Effect != "created" ||
		descriptors[0].ResultContract.Effects[0].ResultField != "taskID" ||
		descriptors[0].ResultContract.Effects[0].EffectIdentity != "id" {
		t.Fatalf("expected mapped resource effect, got %+v", descriptors[0].ResultContract)
	}
	if descriptors[0].ResultContract.EvidenceCondition == nil ||
		string(descriptors[0].ResultContract.EvidenceCondition.Equals) != `"task-1"` {
		t.Fatalf("expected mapped evidence condition, got %+v", descriptors[0].ResultContract)
	}
}

func TestResolveLanguageModelProviderDefaultsToCapabilityLLM(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "must-not-be-read")
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.Capability.Model = "gemma-4-E4B-it"

	languageModelProvider := resolveLanguageModelProvider(runtimeConfiguration)
	if languageModelProvider == nil {
		t.Fatal("expected capability provider to be inferred")
	}
	capabilityLLMClient, isCapabilityProvider := languageModelProvider.(llm.CapabilityLLMClient)
	if !isCapabilityProvider {
		t.Fatalf("expected capability provider, got %T", languageModelProvider)
	}
	if capabilityLLMClient.ModelName != "gemma-4-E4B-it" {
		t.Fatalf("expected capability model, got %q", capabilityLLMClient.ModelName)
	}
}

func TestResolveIntakeLanguageModelProviderUsesReliableTaskTierModel(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Agent.Intake.Enabled = true
	runtimeConfiguration.Agent.Intake.ExecutionMode = "auto"

	languageModelProvider := resolveIntakeLanguageModelProvider(runtimeConfiguration, nil)
	fallbackLanguageModelProvider, isFallbackProvider := languageModelProvider.(llm.FallbackLanguageModelProvider)
	if !isFallbackProvider {
		t.Fatalf("expected fallback intake provider, got %T", languageModelProvider)
	}
	primaryClient, isPrimaryCapabilityClient := unwrapModelTier(fallbackLanguageModelProvider.PrimaryProvider).(llm.CapabilityLLMClient)
	if !isPrimaryCapabilityClient {
		t.Fatalf("expected primary capability intake provider, got %T", fallbackLanguageModelProvider.PrimaryProvider)
	}
	fallbackClient, isFallbackCapabilityClient := unwrapModelTier(fallbackLanguageModelProvider.FallbackProvider).(llm.CapabilityLLMClient)
	if !isFallbackCapabilityClient {
		t.Fatalf("expected fallback capability intake provider, got %T", fallbackLanguageModelProvider.FallbackProvider)
	}
	expectedTierNames := llm.ResolveModelTierNames(deriveLanguageModelRuntimeConfiguration(runtimeConfiguration))
	if primaryClient.ModelName != expectedTierNames.Medium {
		t.Fatalf("expected medium tier intake model %q, got %q", expectedTierNames.Medium, primaryClient.ModelName)
	}
	if fallbackClient.ModelName != expectedTierNames.High {
		t.Fatalf("expected high tier intake fallback model %q, got %q", expectedTierNames.High, fallbackClient.ModelName)
	}
	if primaryClient.ExecutionMode != "auto" || fallbackClient.ExecutionMode != "auto" {
		t.Fatalf("expected automatic intake execution mode, got %q and %q", primaryClient.ExecutionMode, fallbackClient.ExecutionMode)
	}
}

func TestResolveIntakeLanguageModelProviderUsesExplicitModel(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Agent.Intake.Enabled = true
	runtimeConfiguration.Agent.Intake.Model = "x-ai/grok-4.3"

	languageModelProvider := resolveIntakeLanguageModelProvider(runtimeConfiguration, nil)
	fallbackLanguageModelProvider, isFallbackProvider := languageModelProvider.(llm.FallbackLanguageModelProvider)
	if !isFallbackProvider {
		t.Fatalf("expected fallback intake provider, got %T", languageModelProvider)
	}
	primaryClient, isPrimaryCapabilityClient := unwrapModelTier(fallbackLanguageModelProvider.PrimaryProvider).(llm.CapabilityLLMClient)
	if !isPrimaryCapabilityClient {
		t.Fatalf("expected primary capability intake provider, got %T", fallbackLanguageModelProvider.PrimaryProvider)
	}
	if primaryClient.ModelName != "x-ai/grok-4.3" {
		t.Fatalf("expected explicit intake model, got %q", primaryClient.ModelName)
	}
}

func TestMaximumXLowTierCapsTaskModelsAndUsesLowForImages(t *testing.T) {
	runtimeConfiguration := configuredModelTierRuntime("xlow")
	providers := resolveTaskTierLanguageModelProviders(runtimeConfiguration, slog.New(slog.DiscardHandler))

	for providerName, provider := range map[string]llm.LanguageModelProvider{
		"low": providers.Low, "xlow": providers.XLow, "medium": providers.Medium,
		"high": providers.High, "xhigh": providers.XHigh, "max": providers.Max,
	} {
		modelNames := languageModelProviderNames(provider)
		if !reflect.DeepEqual(modelNames, []string{"vendor/low", "vendor/xlow"}) {
			t.Fatalf("expected %s provider to use only xlow text and low vision models, got %v", providerName, modelNames)
		}
	}
}

func TestMaximumLowTierCapsIntakeFallbacks(t *testing.T) {
	runtimeConfiguration := configuredModelTierRuntime("low")
	runtimeConfiguration.Agent.Intake.Enabled = true
	provider := resolveIntakeLanguageModelProvider(runtimeConfiguration, nil)

	modelNames := languageModelProviderNames(provider)
	if !reflect.DeepEqual(modelNames, []string{"vendor/low", "vendor/xlow"}) {
		t.Fatalf("expected intake to stay at or below low, got %v", modelNames)
	}
}

func configuredModelTierRuntime(maximumModelTier string) config.RuntimeConfiguration {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.Capability.MaximumModelTier = maximumModelTier
	runtimeConfiguration.LanguageModel.Capability.MaxModel = "vendor/max"
	runtimeConfiguration.LanguageModel.Capability.XHighModel = "vendor/xhigh"
	runtimeConfiguration.LanguageModel.Capability.HighModel = "vendor/high"
	runtimeConfiguration.LanguageModel.Capability.MediumModel = "vendor/medium"
	runtimeConfiguration.LanguageModel.Capability.LowModel = "vendor/low"
	runtimeConfiguration.LanguageModel.Capability.XLowModel = "vendor/xlow"
	return runtimeConfiguration
}

func languageModelProviderNames(provider llm.LanguageModelProvider) []string {
	modelNames := map[string]bool{}
	collectLanguageModelProviderNames(provider, modelNames)
	result := make([]string, 0, len(modelNames))
	for modelName := range modelNames {
		result = append(result, modelName)
	}
	sort.Strings(result)
	return result
}

func collectLanguageModelProviderNames(provider llm.LanguageModelProvider, modelNames map[string]bool) {
	if tieredProvider, isTieredProvider := provider.(interface {
		UnderlyingProvider() llm.LanguageModelProvider
	}); isTieredProvider {
		collectLanguageModelProviderNames(tieredProvider.UnderlyingProvider(), modelNames)
		return
	}
	switch typedProvider := provider.(type) {
	case llm.CapabilityLLMClient:
		modelNames[typedProvider.ModelName] = true
	case llm.FallbackLanguageModelProvider:
		collectLanguageModelProviderNames(typedProvider.PrimaryProvider, modelNames)
		collectLanguageModelProviderNames(typedProvider.FallbackProvider, modelNames)
	case llm.VisionFallbackProvider:
		collectLanguageModelProviderNames(typedProvider.TextOnlyModel, modelNames)
		collectLanguageModelProviderNames(typedProvider.VisionModel, modelNames)
	}
}

func unwrapModelTier(provider llm.LanguageModelProvider) llm.LanguageModelProvider {
	if tieredProvider, isTieredProvider := provider.(interface {
		UnderlyingProvider() llm.LanguageModelProvider
	}); isTieredProvider {
		return tieredProvider.UnderlyingProvider()
	}
	return provider
}

func TestLoadAgentInstructionPromptUsesAgentsAndSkills(t *testing.T) {
	workspacePath := t.TempDir()
	skillDirectoryPath := filepath.Join(workspacePath, ".agents", "skills", "browser")
	if errorValue := os.MkdirAll(skillDirectoryPath, 0o755); errorValue != nil {
		t.Fatalf("expected skill directory: %v", errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(workspacePath, "IDENTITY.md"), []byte("Use the runtime display name."), 0o600); errorValue != nil {
		t.Fatalf("expected identity file: %v", errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(workspacePath, "BOT_PROFILE.yaml"), []byte("username: internkim\ndisplayName: internkim\nenglishDisplayName: Intern Kim\naliases:\n  - internkim\npublicDescription: \"\"\nidentityExtension: Use the display name.\n"), 0o600); errorValue != nil {
		t.Fatalf("expected bot profile file: %v", errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(workspacePath, "SOUL.md"), []byte("Lead with the result."), 0o600); errorValue != nil {
		t.Fatalf("expected soul file: %v", errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(workspacePath, "AGENTS.md"), []byte("Use agent-browser for web automation."), 0o600); errorValue != nil {
		t.Fatalf("expected agents file: %v", errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(skillDirectoryPath, "SKILL.md"), []byte("Run agent-browser snapshot -i after navigation."), 0o600); errorValue != nil {
		t.Fatalf("expected skill file: %v", errorValue)
	}
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Terminal.WorkspaceRootPath = workspacePath

	instructionBundle := loadAgentInstructionBundle(runtimeConfiguration)
	for _, expectedFragment := range []string{"Use the runtime display name.", "current displayName: internkim", "Use the display name.", "Lead with the result.", "Use agent-browser for web automation."} {
		if !strings.Contains(instructionBundle.Prompt, expectedFragment) {
			t.Fatalf("expected instruction prompt to contain %q, got %q", expectedFragment, instructionBundle.Prompt)
		}
	}
	if len(instructionBundle.Skills) == 0 || instructionBundle.Skills[0].Name != "browser" {
		t.Fatalf("expected skill metadata to be loaded: %+v", instructionBundle.Skills)
	}
}

func TestLoadAgentIdentityReadsBotProfile(t *testing.T) {
	workspacePath := t.TempDir()
	if errorValue := os.WriteFile(filepath.Join(workspacePath, "BOT_PROFILE.yaml"), []byte("username: internkim\ndisplayName: 김인턴\n"), 0o600); errorValue != nil {
		t.Fatalf("expected bot profile file: %v", errorValue)
	}
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Terminal.WorkspaceRootPath = workspacePath

	agentIdentity := loadAgentIdentity(runtimeConfiguration)

	if agentIdentity.Name != "김인턴" || agentIdentity.Handle != "internkim" {
		t.Fatalf("expected the bot profile identity, got %+v", agentIdentity)
	}
}

func TestRenderBotProfileInstructionOmitsUnconfiguredUsername(t *testing.T) {
	instruction := renderBotProfileInstruction([]byte("displayName: 김인턴\n"))

	if strings.Contains(instruction, "internal username") {
		t.Fatalf("expected no username line without a configured username, got %q", instruction)
	}
	if !strings.Contains(instruction, "current displayName: 김인턴") {
		t.Fatalf("expected the configured displayName, got %q", instruction)
	}
}

func TestLoadAgentInstructionBundleDiscoversAddedUserSkill(t *testing.T) {
	workspacePath := t.TempDir()
	skillDirectoryPath := filepath.Join(workspacePath, ".agents", "skills", "research-helper")
	if errorValue := os.MkdirAll(skillDirectoryPath, 0o755); errorValue != nil {
		t.Fatalf("expected skill directory: %v", errorValue)
	}
	skillDocument := `---
name: research-helper
description: Help with research tasks and source lookup requests.
---
Research helper body.
`
	if errorValue := os.WriteFile(filepath.Join(skillDirectoryPath, "SKILL.md"), []byte(skillDocument), 0o600); errorValue != nil {
		t.Fatalf("expected skill file: %v", errorValue)
	}
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Terminal.WorkspaceRootPath = workspacePath

	instructionBundle := loadAgentInstructionBundle(runtimeConfiguration)

	if len(instructionBundle.Skills) != 1 || instructionBundle.Skills[0].Name != "research-helper" {
		t.Fatalf("expected added user skill to be discovered, got %+v", instructionBundle.Skills)
	}
	if instructionBundle.Skills[0].Description != "Help with research tasks and source lookup requests." {
		t.Fatalf("expected standard skill fields, got %+v", instructionBundle.Skills[0])
	}
}

func TestDeriveAllowedToolNamesByProfileKeepsDomainCapabilitiesOutOfBaseline(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.AgentProfiles = []config.AgentProfileConfiguration{
		{Name: "default", AllowedToolNames: []string{"shell"}},
	}
	runtimeConfiguration.Capabilities.ToolDescriptors = []config.CapabilityToolDescriptor{{Name: "site_serve"}}

	allowedToolNamesByProfile := deriveAllowedToolNamesByProfile(runtimeConfiguration)
	defaultProfileToolNames := allowedToolNamesByProfile["default"]

	if containsString(defaultProfileToolNames, "site_serve") {
		t.Fatalf("expected domain capability to stay out of profile baseline, got %+v", defaultProfileToolNames)
	}
	for _, expectedToolName := range []string{"shell", "file_deliver", "skill_search", "file_read", "file_write", "file_edit", "file_preview", "image_read"} {
		if !containsString(defaultProfileToolNames, expectedToolName) {
			t.Fatalf("expected baseline tool %q, got %+v", expectedToolName, defaultProfileToolNames)
		}
	}
	if containsString(defaultProfileToolNames, "ask_confirm") {
		t.Fatalf("expected runtime-owned confirmation to stay out of model tools, got %+v", defaultProfileToolNames)
	}
	if containsString(defaultProfileToolNames, "ask_input") {
		t.Fatalf("expected typed user input to stay out of the baseline tools, got %+v", defaultProfileToolNames)
	}
}

func TestNewApplicationRegistersSecretlessConnectorTransports(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()

	application := NewApplication(runtimeConfiguration, "", bluecollarharness.New)

	transportNames := strings.Join(application.connectorTransportNames(), ",")
	for _, expectedName := range []string{"mattermost:mattermost-internal-ingress", "slack:slack-internal-ingress", "signal:signal-internal-ingress"} {
		if !strings.Contains(transportNames, expectedName) {
			t.Fatalf("expected transport %q in %q", expectedName, transportNames)
		}
	}
	if strings.Contains(transportNames, "websocket") {
		t.Fatalf("expected no platform-owned websocket transport, got %q", transportNames)
	}
}

func TestNewApplicationAllowsSignalInternalIngress(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()
	runtimeConfiguration.Connectors.Signal.Enabled = true

	application := NewApplication(runtimeConfiguration, "", bluecollarharness.New)

	if application.startupError != nil {
		t.Fatalf("expected signal internal ingress to be allowed: %v", application.startupError)
	}
}

func TestApplicationShutdownClosesOwnedMCPRegistry(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()
	application := NewApplication(runtimeConfiguration, "", bluecollarharness.New)
	expectedError := errors.New("close MCP registry")
	registry := &applicationMCPRegistryCloser{closeError: expectedError}
	application.mcpRegistry = registry

	errorValue := application.Shutdown(context.Background())

	if !errors.Is(errorValue, expectedError) {
		t.Fatalf("expected MCP close error, got %v", errorValue)
	}
	if registry.closeCount != 1 {
		t.Fatalf("expected MCP registry to close once, got %d", registry.closeCount)
	}
}

func TestMCPQuarantineLogsPreserveStructuredIdentity(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))

	logMCPServerQuarantines(logger, mcp.LoadReport{Quarantined: []mcp.QuarantinedServer{{
		Name:   "workspace",
		Reason: "server unavailable",
	}}})
	logMCPProviderQuarantine(logger, toolcontract.QuarantinedToolProvider{
		ProviderID: "mcp:workspace",
		Reason:     "tool name collision",
	})

	logOutput := output.String()
	for _, expectedText := range []string{
		`"msg":"mcp.server.quarantined"`,
		`"serverName":"workspace"`,
		`"reason":"server unavailable"`,
		`"msg":"mcp.provider.quarantined"`,
		`"providerID":"mcp:workspace"`,
		`"reason":"tool name collision"`,
	} {
		if !strings.Contains(logOutput, expectedText) {
			t.Fatalf("expected structured log field %s in %s", expectedText, logOutput)
		}
	}
}

func TestApplicationChecksProtocolIdentityOnceAndStoresResult(t *testing.T) {
	protocolVersion := "0.4.0"
	aggregateProtocolHash := "58ff1977989bacbf2db3fdce08fd57c9b52f344ca747a3322f4e60bdf6052a78"
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		requestCount++
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"status":"ok","protocolVersion":"` + protocolVersion + `","aggregateProtocolHash":"` + aggregateProtocolHash + `"}`))
	}))
	defer server.Close()

	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()
	runtimeConfiguration.Capabilities.Endpoint = server.URL
	runtimeConfiguration.Capabilities.ProtocolVersion = protocolVersion
	runtimeConfiguration.Capabilities.AggregateProtocolHash = aggregateProtocolHash
	application := NewApplication(runtimeConfiguration, "", bluecollarharness.New)
	application.protocolIdentityExpected = protocolidentity.Identity{
		ProtocolVersion:       protocolVersion,
		AggregateProtocolHash: aggregateProtocolHash,
	}
	application.protocolIdentityChecker = protocolidentity.NewChecker(protocolidentity.Configuration{
		CapabilityEndpoint: server.URL,
		HTTPClient:         server.Client(),
	})

	if errorValue := application.checkProtocolIdentity(); errorValue != nil {
		t.Fatalf("expected protocol identity check to pass: %v", errorValue)
	}
	if errorValue := application.checkProtocolIdentity(); errorValue != nil {
		t.Fatalf("expected repeated protocol identity check to reuse result: %v", errorValue)
	}
	if requestCount != 2 {
		t.Fatalf("expected one companion-status seed and one capabilityd request, got %d", requestCount)
	}
	if !application.protocolIdentityStatus.Passed {
		t.Fatalf("expected stored protocol identity result to pass: %+v", application.protocolIdentityStatus)
	}
}

func TestApplicationConnectorRouteAcceptsNormalizedSlackEvent(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()
	application := NewApplication(runtimeConfiguration, "", bluecollarharness.New)

	payload := []byte(`{}`)
	request := httptest.NewRequest(http.MethodPost, "/connectors/slack/events", bytes.NewReader(payload))
	responseRecorder := httptest.NewRecorder()
	application.httpServer.Handler.ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected normalized event status ok, got %d", responseRecorder.Code)
	}
	var responseDocument map[string]any
	if errorValue := json.Unmarshal(responseRecorder.Body.Bytes(), &responseDocument); errorValue != nil {
		t.Fatalf("expected response document: %v", errorValue)
	}
	if responseDocument["platform"] != "slack" {
		t.Fatalf("expected slack platform response, got %+v", responseDocument)
	}
	if responseDocument["reason"] != "no_event" {
		t.Fatalf("expected no_event response, got %+v", responseDocument)
	}
}

func TestApplicationRegistersSignalHTTPRoute(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()
	application := NewApplication(runtimeConfiguration, "", bluecollarharness.New)

	payload := []byte(`{}`)
	request := httptest.NewRequest(http.MethodPost, "/connectors/signal/events", bytes.NewReader(payload))
	responseRecorder := httptest.NewRecorder()
	application.httpServer.Handler.ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected signal normalized event status ok, got %d", responseRecorder.Code)
	}
}

func TestApplicationAutoResumeLaunchesAtMostFiveInterruptedTaskRuns(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	now := time.Now()
	repository := &applicationAutoResumeRepository{}
	for index := 0; index < 7; index++ {
		taskRun := task.TaskRun{
			TaskRunID:            "task-" + string(rune('a'+index)),
			RequesterPersonID:    "person-1",
			OriginConversationID: "conversation-1",
			OriginReplyTargetID:  "reply-1",
			Status:               task.TaskStatusInterrupted,
			Prompt:               "finish task",
			CreatedAt:            now.Add(time.Duration(index) * time.Minute),
			UpdatedAt:            now.Add(time.Duration(index) * time.Minute),
		}
		repository.taskRuns = append(repository.taskRuns, taskRun)
		taskEventService.AppendTaskEvent(taskRun.TaskRunID, "task.interrupted", "runtime restarted")
	}
	taskRunService.UseRepository(repository)
	resumer := &applicationAutoResumeResumer{}
	application := &Application{
		taskRunService:             taskRunService,
		interruptedTaskResumer:     resumer,
		interruptedTaskResumeDelay: 0,
	}

	application.resumeInterruptedTaskRuns(context.Background(), now.Add(time.Hour))

	if len(resumer.taskRunIDs) != 5 {
		t.Fatalf("resume count = %d, want 5", len(resumer.taskRunIDs))
	}
	for _, taskRunID := range resumer.taskRunIDs {
		if !taskEventsContainApplicationEvent(taskRunService.ListTaskEvent(taskRunID), "task.auto_resume_attempted") {
			t.Fatalf("expected auto-resume attempt event for %s", taskRunID)
		}
	}
	skippedCount := 0
	for _, taskRun := range repository.taskRuns {
		if taskEventsContainApplicationEvent(taskRunService.ListTaskEvent(taskRun.TaskRunID), "task.auto_resume_skipped") {
			skippedCount++
		}
	}
	if skippedCount != 2 {
		t.Fatalf("skipped count = %d, want 2", skippedCount)
	}
}

type applicationAutoResumeResumer struct {
	taskRunIDs       []string
	failedTaskRunIDs []string
}

func (resumer *applicationAutoResumeResumer) CanResumeInterruptedTaskRun(task.TaskRun) bool {
	return true
}

func (resumer *applicationAutoResumeResumer) ResumeInterruptedTaskRun(_ context.Context, taskRun task.TaskRun) (connectors.ConnectorRuntimeResult, error) {
	resumer.taskRunIDs = append(resumer.taskRunIDs, taskRun.TaskRunID)
	return connectors.ConnectorRuntimeResult{Handled: true, TaskRunID: taskRun.TaskRunID}, nil
}

func (resumer *applicationAutoResumeResumer) FailUnresumedInterruptedTaskRun(_ context.Context, taskRun task.TaskRun, _ string) bool {
	resumer.failedTaskRunIDs = append(resumer.failedTaskRunIDs, taskRun.TaskRunID)
	return true
}

func TestApplicationFailsUnresumedInterruptedTaskRunsWithNotice(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	now := time.Now()
	repository := &applicationAutoResumeRepository{}
	for index := 0; index < 2; index++ {
		taskRun := task.TaskRun{
			TaskRunID:            "exhausted-" + string(rune('a'+index)),
			RequesterPersonID:    "person-1",
			OriginConversationID: "conversation-1",
			OriginReplyTargetID:  "reply-1",
			Status:               task.TaskStatusInterrupted,
			FailureReason:        task.TaskInterruptReasonRuntimeRestart,
			Prompt:               "finish task",
			CreatedAt:            now,
			UpdatedAt:            now,
		}
		repository.taskRuns = append(repository.taskRuns, taskRun)
		taskEventService.AppendTaskEvent(taskRun.TaskRunID, "task.interrupted", task.TaskInterruptReasonRuntimeRestart)
		taskEventService.AppendTaskEvent(taskRun.TaskRunID, "task.auto_resume_attempted", `{"attemptCount":1}`)
	}
	taskRunService.UseRepository(repository)
	resumer := &applicationAutoResumeResumer{}
	application := &Application{
		taskRunService:             taskRunService,
		interruptedTaskResumer:     resumer,
		interruptedTaskResumeDelay: 0,
	}

	application.resumeInterruptedTaskRuns(context.Background(), now.Add(time.Hour))

	if len(resumer.taskRunIDs) != 0 {
		t.Fatalf("resume count = %d, want 0 for attempt-exhausted crash interruptions", len(resumer.taskRunIDs))
	}
	if len(resumer.failedTaskRunIDs) != 2 {
		t.Fatalf("failed count = %d, want 2", len(resumer.failedTaskRunIDs))
	}
	for _, taskRun := range repository.taskRuns {
		if !taskEventsContainApplicationEvent(taskRunService.ListTaskEvent(taskRun.TaskRunID), "task.auto_resume_abandoned") {
			t.Fatalf("expected auto-resume abandoned event for %s", taskRun.TaskRunID)
		}
	}
}

type applicationAutoResumeRepository struct {
	taskRuns []task.TaskRun
}

func (repository *applicationAutoResumeRepository) SaveTaskRun(taskRun task.TaskRun) error {
	repository.taskRuns = append(repository.taskRuns, taskRun)
	return nil
}

func (repository *applicationAutoResumeRepository) StartTaskRunAttempt(task.TaskRun, task.TaskAttempt) error {
	return nil
}

func (repository *applicationAutoResumeRepository) FinishTaskRunAttempt(task.TaskRun, task.TaskAttempt) error {
	return nil
}

func (repository *applicationAutoResumeRepository) TransitionTaskRun(transition task.TaskRunTransition) (task.TaskRun, error) {
	for index, taskRun := range repository.taskRuns {
		if taskRun.TaskRunID != transition.TaskRunID {
			continue
		}
		if !applicationTaskRunStatusAllowed(taskRun.Status, transition.FromStates) {
			return task.TaskRun{}, task.ErrIllegalTransition{
				TaskRunID:     transition.TaskRunID,
				CurrentStatus: taskRun.Status,
				FromStates:    append([]task.TaskStatus{}, transition.FromStates...),
				ToState:       transition.ToState,
			}
		}
		taskRun.Status = transition.ToState
		taskRun.UpdatedAt = transition.UpdatedAt
		repository.taskRuns[index] = taskRun
		return taskRun, nil
	}
	return task.TaskRun{}, errors.New("task run not found")
}

func applicationTaskRunStatusAllowed(status task.TaskStatus, allowedStatuses []task.TaskStatus) bool {
	for _, allowedStatus := range allowedStatuses {
		if status == allowedStatus {
			return true
		}
	}
	return false
}

func (repository *applicationAutoResumeRepository) FindTaskRun(taskRunID string) (task.TaskRun, bool, error) {
	for _, taskRun := range repository.taskRuns {
		if taskRun.TaskRunID == taskRunID {
			return taskRun, true, nil
		}
	}
	return task.TaskRun{}, false, nil
}

func (repository *applicationAutoResumeRepository) FindTaskAttempt(string) (task.TaskAttempt, bool, error) {
	return task.TaskAttempt{}, false, nil
}

func (repository *applicationAutoResumeRepository) ListTaskRun() ([]task.TaskRun, error) {
	return append([]task.TaskRun{}, repository.taskRuns...), nil
}

func (repository *applicationAutoResumeRepository) ListTaskRunByPersonID(personID string) ([]task.TaskRun, error) {
	taskRuns := []task.TaskRun{}
	for _, taskRun := range repository.taskRuns {
		if taskRun.RequesterPersonID == personID {
			taskRuns = append(taskRuns, taskRun)
		}
	}
	return taskRuns, nil
}

func (repository *applicationAutoResumeRepository) DeleteTaskRun(string, []string) (bool, error) {
	return false, nil
}

func (repository *applicationAutoResumeRepository) DeleteTaskRunsBefore(time.Time, []string) ([]string, error) {
	return nil, nil
}

func taskEventsContainApplicationEvent(taskEvents []task.TaskEvent, name string) bool {
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == name {
			return true
		}
	}
	return false
}

func TestResolveTaskTierLanguageModelProvidersEscalateLowToMedium(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.Capability.LowModel = "vendor/low"
	runtimeConfiguration.LanguageModel.Capability.MediumModel = "vendor/medium"
	providers := resolveTaskTierLanguageModelProviders(runtimeConfiguration, slog.New(slog.DiscardHandler))

	lowProvider, isFallbackProvider := providers.Low.(llm.FallbackLanguageModelProvider)
	if !isFallbackProvider {
		t.Fatalf("expected low tier to escalate on failure, got %T", providers.Low)
	}
	if lowProvider.PrimaryLabel != "low" || lowProvider.FallbackLabel != "medium" {
		t.Fatalf("expected low/medium labels, got %q/%q", lowProvider.PrimaryLabel, lowProvider.FallbackLabel)
	}

	xLowProvider, isFallbackProvider := providers.XLow.(llm.FallbackLanguageModelProvider)
	if !isFallbackProvider {
		t.Fatalf("expected xlow tier fallback provider, got %T", providers.XLow)
	}
	if _, xLowClimbsTheLadder := xLowProvider.FallbackProvider.(llm.FallbackLanguageModelProvider); !xLowClimbsTheLadder {
		t.Fatalf("expected xlow to fall into the low-to-medium ladder, got %T", xLowProvider.FallbackProvider)
	}

	mediumProvider, isFallbackProvider := providers.Medium.(llm.FallbackLanguageModelProvider)
	if !isFallbackProvider {
		t.Fatalf("expected medium tier fallback provider, got %T", providers.Medium)
	}
	if _, mediumWouldRetryItself := mediumProvider.FallbackProvider.(llm.FallbackLanguageModelProvider); mediumWouldRetryItself {
		t.Fatalf("expected medium to fall back to the bare low model without climbing back up, got %T", mediumProvider.FallbackProvider)
	}
}

func TestResolveTaskTierLanguageModelProvidersKeepsBareLowWhenMediumMatchesLow(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.Capability.LowModel = "vendor/shared"
	runtimeConfiguration.LanguageModel.Capability.MediumModel = "vendor/shared"
	providers := resolveTaskTierLanguageModelProviders(runtimeConfiguration, slog.New(slog.DiscardHandler))

	if _, isFallbackProvider := providers.Low.(llm.FallbackLanguageModelProvider); isFallbackProvider {
		t.Fatal("expected bare low provider when the medium model equals the low model")
	}
}

func TestEveryTaskTierPostsTheConfiguredDirectModel(t *testing.T) {
	receivedModelNames := make(chan string, 6)
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		requestDocument := struct {
			Model string `json:"model"`
		}{}
		if errorValue := json.NewDecoder(request.Body).Decode(&requestDocument); errorValue != nil {
			t.Errorf("expected a decodable completion request: %v", errorValue)
		}
		receivedModelNames <- requestDocument.Model
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"answer","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`))
	}))
	defer server.Close()

	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.DefaultProvider = "direct"
	runtimeConfiguration.LanguageModel.Direct.Endpoint = server.URL
	runtimeConfiguration.LanguageModel.Direct.Model = "llama3.1"

	providers := resolveTaskTierLanguageModelProviders(runtimeConfiguration, slog.New(slog.DiscardHandler))

	for tierName, provider := range map[string]llm.LanguageModelProvider{
		"xlow":   providers.XLow,
		"low":    providers.Low,
		"medium": providers.Medium,
		"high":   providers.High,
		"xhigh":  providers.XHigh,
		"max":    providers.Max,
	} {
		if provider == nil {
			t.Fatalf("expected a %s tier provider", tierName)
		}
		request := llm.StructuredResponseRequest{
			Messages: []llm.Message{{Role: "user", Content: "hello"}},
			StructuredOutputSchema: llm.StructuredOutputSchema{
				Name:     "answer",
				Document: `{"type":"object","properties":{},"additionalProperties":false}`,
			},
		}
		if _, errorValue := provider.GenerateStructuredResponse(context.Background(), request); errorValue != nil {
			t.Fatalf("expected the %s tier to reach the endpoint: %v", tierName, errorValue)
		}
		receivedModelName := <-receivedModelNames
		if receivedModelName != "llama3.1" {
			t.Fatalf("the %s tier asked the endpoint for %q, which it does not serve", tierName, receivedModelName)
		}
	}
}

func TestCapabilityEffectWhenConditionSurvivesConfigLineage(t *testing.T) {
	configuredContract := config.CapabilityToolResultContract{}
	document := `{
		"schema": {"type":"object","additionalProperties":false,"properties":{"siteID":{"type":"string"},"mode":{"type":"string"},"publishedURL":{"type":"string"}},"required":["siteID","mode"]},
		"effects": [{"objectType":"website","effect":"published","resultField":"publishedURL","effectIdentity":"url","when":{"resultField":"mode","equals":"\"publish\""}}]
	}`
	if errorValue := json.Unmarshal([]byte(document), &configuredContract); errorValue != nil {
		t.Fatal(errorValue)
	}
	converted := capabilityToolResultContract(&configuredContract)
	if converted == nil || len(converted.Effects) != 1 {
		t.Fatalf("conversion lost the effect: %#v", converted)
	}
	if converted.Effects[0].When == nil || converted.Effects[0].When.ResultField != "mode" {
		t.Fatalf("the when condition was dropped in config conversion: %#v", converted.Effects[0])
	}
}

func TestApplicationServesHealthWhenProtocolIdentityDisagrees(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"status":"ok","protocolVersion":"0.4.0","aggregateProtocolHash":"b4b5c630888e6de30e52ec88809c9356cf2fb42d7e1189215d3de0d44e60c775"}`))
	}))
	defer server.Close()

	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Logging.DirectoryPath = t.TempDir()
	runtimeConfiguration.Capabilities.Endpoint = server.URL
	application := NewApplication(runtimeConfiguration, "", bluecollarharness.New)
	application.httpServer.Addr = "127.0.0.1:0"
	application.protocolIdentityExpected = protocolidentity.Identity{
		ProtocolVersion:       "0.4.0",
		AggregateProtocolHash: "fccec45c4b3fc539159b3a293d61275ed2fc4ae738f371ec9122c1546b32a42f",
	}
	application.protocolIdentityChecker = protocolidentity.NewChecker(protocolidentity.Configuration{
		CapabilityEndpoint: server.URL,
		HTTPClient:         server.Client(),
	})

	serveError := make(chan error, 1)
	go func() { serveError <- application.Start() }()
	t.Cleanup(func() { _ = application.httpServer.Close() })

	select {
	case errorValue := <-serveError:
		t.Fatalf("a rejected protocol identity must not end the process: %v", errorValue)
	case <-time.After(500 * time.Millisecond):
	}
	if application.protocolIdentityStatus.Passed {
		t.Fatal("expected the stored protocol identity result to record the disagreement")
	}
}
