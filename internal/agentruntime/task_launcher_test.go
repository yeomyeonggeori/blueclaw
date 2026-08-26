package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/launchfailure"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
	"github.com/yeomyeonggeori/bluecollar/loop"
)

func TestTaskLauncherCreatesAuditedAgentRun(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := loop.NewAgentKernel(taskRunService, task.NewTaskStepService())
	runtimeLanguageModel := staticRuntimeLanguageModel{content: runtimeFinishMessage("done")}
	useRuntimeTestLanguageModel(agentKernel, runtimeFinishMessage("done"))
	pinnedMemoryStore := memory.NewMarkdownStore(t.TempDir(), 1200)
	if _, errorValue := pinnedMemoryStore.MergePersonMemory(context.Background(), "person-1", "사용자는 발표자료 생성을 자주 요청한다."); errorValue != nil {
		t.Fatalf("expected pinned memory setup to succeed: %v", errorValue)
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UsePinnedMemoryStore(pinnedMemoryStore)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"conversation_history", "memory_search"},
	}, nil)

	launchResult, errorValue := routedTaskLauncher(agentKernel, taskRunService, toolCatalogBuilder, runtimeLanguageModel).Launch(context.Background(), TaskLaunchRequest{
		Source:                    TaskLaunchSourceConnector,
		SourceReference:           "mattermost:post-1",
		RequesterPersonID:         "person-1",
		ProfileName:               "default",
		ConversationID:            "channel-1",
		Prompt:                    "발표자료 만들어줘",
		HistoryProvider:           staticHistoryProvider{},
		PersonAccess:              policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
		MemoryNamespaces:          []memory.MemoryNamespace{memory.UserNamespace("person-1")},
		AccessibleConversationIDs: []string{"channel-1"},
	})
	if errorValue != nil {
		t.Fatalf("expected launch to succeed: %v", errorValue)
	}
	if launchResult.TurnResult.TaskRun.TaskRunID == "" {
		t.Fatal("expected task run id")
	}
	if len(launchResult.MemoryFacts) != 1 {
		t.Fatalf("expected pinned memory result, got %+v", launchResult.MemoryFacts)
	}
	if !containsString(launchResult.ToolNames, "conversation_history") || !containsString(launchResult.ToolNames, "memory_search") {
		t.Fatalf("expected launch tool catalog, got %+v", launchResult.ToolNames)
	}

	taskEvents := taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID)
	if !containsTaskEvent(taskEvents, "agent.instructions_loaded") {
		t.Fatalf("expected instructions_loaded event, got %+v", taskEvents)
	}
	taskLaunchEvent := findTaskEvent(taskEvents, "agent.task_launched")
	if taskLaunchEvent.Name == "" {
		t.Fatalf("expected task launch event, got %+v", taskEvents)
	}
	if !strings.Contains(taskLaunchEvent.Body, `"source":"connector"`) || !strings.Contains(taskLaunchEvent.Body, `"memoryFactCount":1`) {
		t.Fatalf("expected launch audit body, got %s", taskLaunchEvent.Body)
	}
}

func TestTaskLauncherPersistsAuthoritativeRouterFailure(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	noticeAuthoringLanguageModel := authoredRuntimeFailureLanguageModel{reply: "요청을 분류하지 못해 작업을 시작하지 못했습니다. 다시 요청해 주세요."}
	agentKernel := loop.NewAgentKernel(taskRunService, task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(noticeAuthoringLanguageModel)
	agentKernel.UseIntakeLanguageModelProvider(failingRuntimeRouterLanguageModel{errorValue: errors.New("router unavailable")})
	agentKernel.UseIntakeOptions(agentcontract.IntakeOptions{IsEnabled: true})

	launchResult, errorValue := routedTaskLauncherAuthoringNoticesWith(agentKernel, taskRunService, NewToolCatalogBuilder(), failingRuntimeRouterLanguageModel{errorValue: errors.New("router unavailable")}, noticeAuthoringLanguageModel).Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceAdmin,
		RequesterPersonID: "person-1",
		ConversationID:    "admin:person-1",
		Prompt:            "run admin task",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
	})
	if errorValue != nil {
		t.Fatalf("expected persisted router failure: %v", errorValue)
	}
	if launchResult.TurnResult.TaskRun.Status != task.TaskStatusFailed || launchResult.TurnResult.FailureNotice.Source != "generated" {
		t.Fatalf("expected LLM-authored failed task, got %+v", launchResult.TurnResult)
	}
	taskEvents := taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID)
	if llmCallEvent := findTaskEvent(taskEvents, "llm.call"); !strings.Contains(llmCallEvent.Body, `"isError":true`) {
		t.Fatalf("expected persisted router call error, got %+v", taskEvents)
	}
	if !containsTaskEvent(taskEvents, "agent.task_launched") {
		t.Fatalf("expected launch audit for failed task, got %+v", taskEvents)
	}
}

func TestTaskLauncherAuditsPlatformMessageRegistryFingerprint(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	harness := harnesstest.New(taskRunService)
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{
		Endpoint:   "http://capability.local",
		HTTPClient: &recordingHTTPClient{responseBody: platformMessageLiveRegistryResponse(platformMessageDeleteCriteriaSchema())},
	}, testPlatformMessageCapabilityDescriptors())
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"message_context", "message_search", "message_delete"},
	}, nil)

	taskLauncher := NewTaskLauncher(harness, taskRunService, toolCatalogBuilder)
	taskLauncher.UseLaunchFailureCompleter(launchfailure.NewCompleter(taskRunService, nil))
	launchResult, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:                    TaskLaunchSourceConnector,
		SourceReference:           "mattermost:post-1",
		RequesterPersonID:         "person-1",
		ProfileName:               "default",
		ConversationID:            "channel-1",
		Prompt:                    "너가 보낸 메시지 삭제해줘",
		PersonAccess:              policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
		AccessibleConversationIDs: []string{"channel-1"},
	})
	if errorValue != nil {
		t.Fatalf("expected matching registry launch to succeed: %v", errorValue)
	}

	taskLaunchEvent := findTaskEvent(taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID), "agent.task_launched")
	if taskLaunchEvent.Name == "" {
		t.Fatalf("expected launch event")
	}
	for _, expected := range []string{
		`"toolRegistryVersion":"platform-message-v1"`,
		`"capabilityDescriptorHash":"`,
		`"liveCapabilityHash":"`,
		`"platformMessageDescriptorHash":"`,
		`"livePlatformMessageDescriptorHash":"`,
		`"allowedToolHash":"`,
		`"hasPlatformMessageDelete":true`,
		`"liveHasPlatformMessageDelete":true`,
		`"hasOldMattermostPostDelete":false`,
		`"hasOldPlatformDMInspect":false`,
	} {
		if !strings.Contains(taskLaunchEvent.Body, expected) {
			t.Fatalf("expected launch event to contain %s, got %s", expected, taskLaunchEvent.Body)
		}
	}
}

func TestTaskLauncherAuditsPlatformMessageSchemaSkewWithoutBlocking(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	harness := harnesstest.New(taskRunService)
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{
		Endpoint:   "http://capability.local",
		HTTPClient: &recordingHTTPClient{responseBody: platformMessageLiveRegistryResponse(platformMessageDeleteIDsOnlySchema())},
	}, testPlatformMessageCapabilityDescriptors())
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"message_context", "message_search", "message_delete"},
	}, nil)

	taskLauncher := NewTaskLauncher(harness, taskRunService, toolCatalogBuilder)
	taskLauncher.UseLaunchFailureCompleter(launchfailure.NewCompleter(taskRunService, nil))
	launchResult, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:                    TaskLaunchSourceConnector,
		SourceReference:           "mattermost:post-1",
		RequesterPersonID:         "person-1",
		ProfileName:               "default",
		ConversationID:            "channel-1",
		Prompt:                    "너가 보낸 메시지 삭제해줘",
		PersonAccess:              policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
		AccessibleConversationIDs: []string{"channel-1"},
	})
	if errorValue != nil {
		t.Fatalf("expected schema skew to remain diagnostic-only, got %v", errorValue)
	}
	taskLaunchEvent := findTaskEvent(taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID), "agent.task_launched")
	if taskLaunchEvent.Name == "" {
		t.Fatal("expected task launch event")
	}
	if !strings.Contains(taskLaunchEvent.Body, `"platformMessageDescriptorHash":"`) ||
		!strings.Contains(taskLaunchEvent.Body, `"livePlatformMessageDescriptorHash":"`) {
		t.Fatalf("expected schema skew hashes in launch event, got %s", taskLaunchEvent.Body)
	}
}

func TestPlatformMessageDescriptorHashIncludesInputIntentSchema(t *testing.T) {
	baseDescriptor := CapabilityToolDescriptor{
		Name:              "message_delete",
		InputSchema:       platformMessageDeleteCriteriaSchema(),
		InputIntentSchema: platformMessageDeleteIDsOnlySchema(),
	}
	changedDescriptor := baseDescriptor
	changedDescriptor.InputIntentSchema = platformMessageEmptySchema()

	if hashCapabilityDescriptors([]CapabilityToolDescriptor{baseDescriptor}) ==
		hashCapabilityDescriptors([]CapabilityToolDescriptor{changedDescriptor}) {
		t.Fatal("expected input intent schema drift to change the descriptor hash")
	}
}

func TestTaskLauncherRejectsStaleMessageToolRegistryBeforeModelCall(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	harness := harnesstest.New(taskRunService)
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{
		Endpoint: "http://capability.local",
		HTTPClient: &recordingHTTPClient{responseBody: `{"deviceCapabilities":[
			{"name":"message_context"},
			{"name":"message_search"},
			{"name":"message_send"},
			{"name":"message_update"},
			{"name":"message_delete"}
		]}`},
	}, []CapabilityToolDescriptor{{Name: "mattermost_post_delete", PolicyResource: "tool:mattermost_post_delete"}})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"mattermost_post_delete"},
	}, nil)

	taskLauncher := NewTaskLauncher(harness, taskRunService, toolCatalogBuilder)
	taskLauncher.UseLaunchFailureCompleter(launchfailure.NewCompleter(taskRunService, nil))
	launchResult, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		SourceReference:   "mattermost:post-1",
		RequesterPersonID: "person-1",
		ProfileName:       "default",
		ConversationID:    "channel-1",
		Prompt:            "너가 보낸 메시지 삭제해줘",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
	})
	if errorValue != nil {
		t.Fatalf("expected registry mismatch to return failed task result: %v", errorValue)
	}
	if launchResult.TurnResult.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task, got %+v", launchResult.TurnResult.TaskRun)
	}
	if !strings.Contains(launchResult.TurnResult.FailureNotice.SendableMessage(), "runtime_registry_mismatch") {
		t.Fatalf("expected raw registry mismatch notice, got %+v", launchResult.TurnResult.FailureNotice)
	}
	if harness.RunTurnCallCount() != 0 {
		t.Fatalf("expected the registry mismatch to stop before the turn, got %d turns", harness.RunTurnCallCount())
	}
	taskEvents := taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID)
	if !containsTaskEvent(taskEvents, "agent.launch_step.error") {
		t.Fatalf("expected launch step error event, got %+v", taskEvents)
	}
}

func TestTaskLauncherAddsStaffToRequesterAccess(t *testing.T) {
	personAccess := requesterPersonAccessForTaskLaunch(TaskLaunchRequest{
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			Circles: []string{"finance"},
		},
	})

	if personAccess.PersonID != "person-1" {
		t.Fatalf("expected requester person id to be copied, got %+v", personAccess)
	}
	if !containsString(personAccess.Circles, "staff") || !containsString(personAccess.Circles, "finance") {
		t.Fatalf("expected task requester access to include staff and explicit circles, got %+v", personAccess.Circles)
	}
}

func TestTaskLauncherProvisionsRequesterWorkspaceBeforeToolSet(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	harness := harnesstest.New(taskRunService)
	workspacePath := t.TempDir()
	requesterHomePath := filepath.Join(workspacePath, "private", "people", "person-1")
	provisioner := &recordingRequesterWorkspaceProvisioner{
		provision: func(personAccess policy.PersonAccess, workspaceRootPath string) error {
			if personAccess.PersonID != "person-1" {
				t.Fatalf("expected requester person access, got %+v", personAccess)
			}
			if workspaceRootPath != workspacePath {
				t.Fatalf("expected workspace root %s, got %s", workspacePath, workspaceRootPath)
			}
			if _, errorValue := os.Stat(requesterHomePath); !os.IsNotExist(errorValue) {
				t.Fatalf("expected requester home to be absent before provisioning, got %v", errorValue)
			}
			return os.MkdirAll(requesterHomePath, 0700)
		},
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"memory_search"},
	}, nil)
	taskLauncher := NewTaskLauncher(harness, taskRunService, toolCatalogBuilder)
	taskLauncher.UseRequesterWorkspaceProvisioner(provisioner)

	launchResult, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		SourceReference:   "mattermost:post-1",
		RequesterPersonID: "person-1",
		ProfileName:       "default",
		ConversationID:    "channel-1",
		Prompt:            "prepare workspace",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
	})
	if errorValue != nil {
		t.Fatalf("expected launch to succeed: %v", errorValue)
	}
	if provisioner.callCount != 1 {
		t.Fatalf("expected one requester provisioning call, got %d", provisioner.callCount)
	}
	workspaceActor, errorValue := security.NewDirectWorkspaceActorFactory().Requester(context.Background(), security.WorkspaceActorRequest{
		WorkspaceRootPath: workspacePath,
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	requesterSitePath := filepath.Join(requesterHomePath, "sites", "site-1")
	if errorValue := workspaceActor.MkdirAll(context.Background(), requesterSitePath); errorValue != nil {
		t.Fatalf("expected requester actor mkdir to succeed after launch provisioning: %v", errorValue)
	}
	taskEvents := taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID)
	launchStepBodies := launchStepResultBodies(taskEvents)
	if len(launchStepBodies) < 2 {
		t.Fatalf("expected launch step results, got %+v", taskEvents)
	}
	if !strings.Contains(launchStepBodies[0], `"stepName":"provision_requester_workspace"`) {
		t.Fatalf("expected requester provisioning before toolset build, got %+v", launchStepBodies)
	}
	if !strings.Contains(launchStepBodies[1], `"stepName":"build_tool_set"`) {
		t.Fatalf("expected toolset build after requester provisioning, got %+v", launchStepBodies)
	}
}

func TestTaskLauncherAuditsPinnedMemoryFailureAndRunsWithoutMemory(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	harness := harnesstest.New(taskRunService)
	rootPath := t.TempDir()
	if errorValue := os.WriteFile(filepath.Join(rootPath, "people"), []byte("not a directory"), 0600); errorValue != nil {
		t.Fatalf("expected pinned memory failure setup to succeed: %v", errorValue)
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UsePinnedMemoryStore(memory.NewMarkdownStore(rootPath, 1200))
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"memory_search"},
	}, nil)

	taskLauncher := NewTaskLauncher(harness, taskRunService, toolCatalogBuilder)
	taskLauncher.UseLaunchFailureCompleter(launchfailure.NewCompleter(taskRunService, nil))
	launchResult, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		SourceReference:   "mattermost:post-1",
		RequesterPersonID: "person-1",
		ProfileName:       "default",
		ConversationID:    "channel-1",
		Prompt:            "내 이름 뭐야?",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
		MemoryNamespaces:  []memory.MemoryNamespace{memory.UserNamespace("person-1")},
	})
	if errorValue != nil {
		t.Fatalf("expected launch to continue without memory: %v", errorValue)
	}
	if len(launchResult.MemoryFacts) != 0 {
		t.Fatalf("expected no memory facts after pinned memory failure, got %+v", launchResult.MemoryFacts)
	}
	taskEvents := taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID)
	if !containsTaskEvent(taskEvents, "memory.pinned_load_failed") {
		t.Fatalf("expected pinned memory failure event, got %+v", taskEvents)
	}
}

func TestToolCatalogHidesHistoryAndQuarantinedMCPTools(t *testing.T) {
	mcpRegistry := mcp.NewMcpRegistry()
	loadReport := mcpRegistry.LoadServerDefinition([]config.MCPServerConfiguration{
		{
			Name: "local-mcp",
			Tools: []config.MCPToolConfiguration{
				{Name: "allowed_tool", Description: "Allowed"},
				{Name: "blocked_tool", Description: "Blocked"},
			},
		},
	})
	if len(loadReport.Quarantined) != 1 {
		t.Fatalf("expected invalid MCP server to be quarantined, got %+v", loadReport)
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMCPRegistry(mcpRegistry)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"allowed_tool", "memory_search"},
	}, nil)

	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	toolNames := toolRegistry.ListToolNames()
	if containsString(toolNames, "conversation_history") {
		t.Fatalf("expected history tool to be hidden without provider, got %+v", toolNames)
	}
	if containsString(toolNames, "allowed_tool") {
		t.Fatalf("expected quarantined MCP tool to stay hidden, got %+v", toolNames)
	}
	if containsString(toolNames, "blocked_tool") {
		t.Fatalf("expected blocked MCP tool to be hidden, got %+v", toolNames)
	}

	toolResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{ToolName: "blocked_tool", Input: json.RawMessage(`{}`)})
	if errorValue != nil {
		t.Fatalf("expected denied tool as result: %v", errorValue)
	}
	if !toolResult.Failed() || toolResult.ContentText() != "tool is not allowed" {
		t.Fatalf("expected denied result, got %+v", toolResult)
	}
}

func TestToolCatalogProfileFiltersBuiltInTerminalTools(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"planner":   {"memory_search"},
		"developer": {"memory_search", "shell"},
	}, nil)

	plannerToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "planner"})
	developerToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "developer"})

	if containsString(plannerToolSet.ListToolNames(), "shell") {
		t.Fatalf("expected planner terminal tools to be hidden, got %+v", plannerToolSet.ListToolNames())
	}
	if !containsString(developerToolSet.ListToolNames(), "shell") || containsString(developerToolSet.ListToolNames(), "shell_session") {
		t.Fatalf("expected developer shell only, got %+v", developerToolSet.ListToolNames())
	}
}

func TestInteractiveBrowserCapabilityUsesCompanion(t *testing.T) {
	httpClient := &recordingHTTPClient{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityTools(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []string{"browser_open"})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"browser_open"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:             "default",
		Prompt:                  "로그인해서 계정을 확인해줘",
		RequesterPersonID:       "person-1",
		RequesterPlatformUserID: "mattermost-user-1",
		ConversationID:          "conversation-1",
		Platform:                "mattermost",
	})

	toolResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "browser_open",
		Input:    json.RawMessage(`{"url":"https://example.com"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected browser capability result: %v", errorValue)
	}
	if toolResult.Failed() {
		t.Fatalf("expected browser capability success, got %+v", toolResult)
	}
	var requestDocument struct {
		ExecutionMode        string `json:"executionMode"`
		RequiresUserPresence bool   `json:"requiresUserPresence"`
		PrivacyClass         string `json:"privacyClass"`
	}
	if errorValue := json.Unmarshal([]byte(httpClient.requestBody), &requestDocument); errorValue != nil {
		t.Fatalf("expected browser capability request json: %v", errorValue)
	}
	if requestDocument.ExecutionMode != "companion" || !requestDocument.RequiresUserPresence || requestDocument.PrivacyClass != "user_browser" {
		t.Fatalf("expected interactive browser capability to require companion, got %+v body=%s", requestDocument, httpClient.requestBody)
	}
}

func TestPublicBrowserCapabilityWithRequesterUsesCompanion(t *testing.T) {
	httpClient := &recordingHTTPClient{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityTools(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []string{"browser_open"})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"browser_open"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:             "default",
		Prompt:                  "https://example.com 열어줘",
		RequesterPersonID:       "person-1",
		RequesterPlatformUserID: "mattermost-user-1",
		ConversationID:          "conversation-1",
		Platform:                "mattermost",
	})

	toolResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "browser_open",
		Input:    json.RawMessage(`{"url":"https://example.com"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected browser capability result: %v", errorValue)
	}
	if toolResult.Failed() {
		t.Fatalf("expected browser capability success, got %+v", toolResult)
	}
	var requestDocument struct {
		ExecutionMode        string `json:"executionMode"`
		RequiresUserPresence bool   `json:"requiresUserPresence"`
		PrivacyClass         string `json:"privacyClass"`
	}
	if errorValue := json.Unmarshal([]byte(httpClient.requestBody), &requestDocument); errorValue != nil {
		t.Fatalf("expected browser capability request json: %v", errorValue)
	}
	if requestDocument.ExecutionMode != "companion" || !requestDocument.RequiresUserPresence || requestDocument.PrivacyClass != "user_browser" {
		t.Fatalf("expected public requester browser capability to require companion, got %+v body=%s", requestDocument, httpClient.requestBody)
	}
}

func TestPrivateBrowserCapabilityUsesCompanion(t *testing.T) {
	httpClient := &recordingHTTPClient{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityTools(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []string{"browser_open"})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"browser_open"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "browser_open",
		Input:    json.RawMessage(`{"url":"http://127.0.0.1:3000"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected browser capability result: %v", errorValue)
	}
	if toolResult.Failed() {
		t.Fatalf("expected browser capability success, got %+v", toolResult)
	}
	var requestDocument struct {
		ExecutionMode        string `json:"executionMode"`
		RequiresUserPresence bool   `json:"requiresUserPresence"`
		PrivacyClass         string `json:"privacyClass"`
	}
	if errorValue := json.Unmarshal([]byte(httpClient.requestBody), &requestDocument); errorValue != nil {
		t.Fatalf("expected browser capability request json: %v", errorValue)
	}
	if requestDocument.ExecutionMode != "companion" || !requestDocument.RequiresUserPresence || requestDocument.PrivacyClass != "user_browser" {
		t.Fatalf("expected private browser capability to require companion, got %+v body=%s", requestDocument, httpClient.requestBody)
	}
}

func TestBrowserFollowUpWithSensitiveVisibleContextUsesCompanion(t *testing.T) {
	httpClient := &recordingHTTPClient{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityTools(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []string{"browser_open"})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"browser_open"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		Prompt:      "다시 열어봐",
		VisibleContext: agentcontract.VisibleContext{Messages: []agentcontract.VisibleContextMessage{
			{Speaker: "사용자", Text: "구글 클라우드 콘솔에서 credential.json 받는 거 도와줘"},
			{Speaker: "김인턴", Text: "Companion 브라우저 연결이 필요합니다."},
		}},
	})

	toolResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "browser_open",
		Input:    json.RawMessage(`{"url":"https://console.cloud.google.com/"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected browser capability result: %v", errorValue)
	}
	if toolResult.Failed() {
		t.Fatalf("expected browser capability success, got %+v", toolResult)
	}
	var requestDocument struct {
		ExecutionMode        string `json:"executionMode"`
		RequiresUserPresence bool   `json:"requiresUserPresence"`
		PrivacyClass         string `json:"privacyClass"`
	}
	if errorValue := json.Unmarshal([]byte(httpClient.requestBody), &requestDocument); errorValue != nil {
		t.Fatalf("expected browser capability request json: %v", errorValue)
	}
	if requestDocument.ExecutionMode != "companion" || !requestDocument.RequiresUserPresence || requestDocument.PrivacyClass != "user_browser" {
		t.Fatalf("expected browser follow-up to require companion, got %+v body=%s", requestDocument, httpClient.requestBody)
	}
}

func TestCapabilityDenialPreservesRecoveryAction(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{"provider":"companion","selectedBackend":"companion","toolName":"browser_open","outcome":"denied","status":"denied","content":"Companion이 연결되어 있지 않아 브라우저를 열 수 없습니다.","isError":true,"result":{"status":"denied","code":"not_connected","toolName":"browser_open","userReason":"Companion이 연결되어 있지 않아 브라우저를 열 수 없습니다.","recovery":{"kind":"companion_connect","delivery":"dm_preferred","downloadURL":"https://example.com/companion.dmg","connectCommand":"/connect"}}}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityTools(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []string{"browser_open"})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"browser_open"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		Prompt:      "브라우저 열어줘",
	})

	toolResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "browser_open",
		Input:    json.RawMessage(`{"url":"https://example.com"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected browser capability result: %v", errorValue)
	}
	if !toolResult.Failed() || len(toolResult.RecoveryActions) != 1 {
		t.Fatalf("expected recovery action on denied tool result, got %+v", toolResult)
	}
	recoveryAction := toolResult.RecoveryActions[0]
	if recoveryAction.Kind != "companion_connect" || recoveryAction.Delivery != "dm_preferred" || recoveryAction.ConnectCommand != "/connect" {
		t.Fatalf("unexpected recovery action: %+v", recoveryAction)
	}
	if len(toolResult.Failure.RecoveryHints) != 1 || toolResult.Failure.RecoveryHints[0].Action != "companion_connect" {
		t.Fatalf("expected recovery hint normalized onto failure, got %+v", toolResult.Failure)
	}
}

func TestPublicBrowserCapabilityUsesCompanion(t *testing.T) {
	httpClient := &recordingHTTPClient{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityTools(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []string{"browser_open"})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"browser_open"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "browser_open",
		Input:    json.RawMessage(`{"url":"https://example.com"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected browser capability result: %v", errorValue)
	}
	if toolResult.Failed() {
		t.Fatalf("expected browser capability success, got %+v", toolResult)
	}
	var requestDocument struct {
		ExecutionMode        string `json:"executionMode"`
		RequiresUserPresence bool   `json:"requiresUserPresence"`
		PrivacyClass         string `json:"privacyClass"`
	}
	if errorValue := json.Unmarshal([]byte(httpClient.requestBody), &requestDocument); errorValue != nil {
		t.Fatalf("expected browser capability request json: %v", errorValue)
	}
	if requestDocument.ExecutionMode != "companion" || !requestDocument.RequiresUserPresence || requestDocument.PrivacyClass != "user_browser" {
		t.Fatalf("expected public browser capability to require companion, got %+v body=%s", requestDocument, httpClient.requestBody)
	}
}

func TestCapabilityDescriptorAppearsInToolSetAndInvokesBridge(t *testing.T) {
	httpClient := &recordingHTTPClient{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:             "browser_open",
		InputSchema:      json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"],"additionalProperties":false}`),
		OutputSchema:     json.RawMessage(`{"type":"object","properties":{"status":{"type":"string"}},"additionalProperties":false}`),
		PolicyResource:   "tool:browser_open",
		SideEffectClass:  toolcontract.ToolSideEffectConnect,
		RequiresApproval: true,
	}})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"browser_open"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	descriptions := toolRegistry.Descriptions()
	actionSchema := loop.ActionSchemaForToolSet(toolRegistry, false, nil, false)
	if !strings.Contains(descriptions, "Test capability browser_open") || strings.Contains(descriptions, `"url"`) {
		t.Fatalf("expected concise descriptor description without duplicated schema, got %s", descriptions)
	}
	if !strings.Contains(actionSchema, `"browser_open"`) || !strings.Contains(actionSchema, `"url"`) {
		t.Fatalf("expected descriptor schema in the action schema, got %s", actionSchema)
	}

	toolResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "browser_open",
		Input:    json.RawMessage(`{"url":"https://example.com"}`),
	})
	if errorValue != nil {
		t.Fatalf("expected capability descriptor invocation: %v", errorValue)
	}
	if toolResult.Failed() || httpClient.requestPath != "/v1/tools/browser_open/invoke" {
		t.Fatalf("expected capability bridge invocation, got result=%+v path=%s", toolResult, httpClient.requestPath)
	}
}

func TestCapabilityToolExecutionUsesResourceAccess(t *testing.T) {
	resourceAccessRules := []policy.ResourceAccessPolicy{{
		Resource: "tool:company_broadcast_send",
		Actions:  []string{"execute"},
		Circles:  []string{"representative"},
	}}
	httpClient := &recordingHTTPClient{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityTools(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []string{"company_broadcast_send"})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"company_broadcast_send"},
	}, nil)

	staffToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID:            "person-1",
			Circles:             []string{"staff"},
			ResourceAccessRules: resourceAccessRules,
		},
	})
	staffResult, errorValue := staffToolSet.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "company_broadcast_send",
		Input:    json.RawMessage(`{"message":"hello"}`),
	})
	if errorValue != nil {
		t.Fatalf("expected denied tool result: %v", errorValue)
	}
	if !staffResult.Failed() || !strings.Contains(staffResult.ContentText(), "tool is not allowed") {
		t.Fatalf("expected staff execution denial, got %+v", staffResult)
	}
	if strings.Contains(staffToolSet.Descriptions(), "company_broadcast_send") {
		t.Fatalf("expected denied tool to be omitted from catalog, got %s", staffToolSet.Descriptions())
	}
	if httpClient.requestPath != "" {
		t.Fatalf("expected denied tool not to call capability bridge, got path=%s", httpClient.requestPath)
	}

	representativeToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID:            "person-2",
			Circles:             []string{"staff", "representative"},
			ResourceAccessRules: resourceAccessRules,
		},
	})
	representativeResult, errorValue := representativeToolSet.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "company_broadcast_send",
		Input:    json.RawMessage(`{"message":"hello"}`),
	})
	if errorValue != nil {
		t.Fatalf("expected representative tool result: %v", errorValue)
	}
	if representativeResult.Failed() {
		t.Fatalf("expected representative execution success, got %+v", representativeResult)
	}
	if httpClient.requestPath != "/v1/tools/company_broadcast_send/invoke" {
		t.Fatalf("expected capability bridge call, got path=%s body=%s", httpClient.requestPath, httpClient.requestBody)
	}
}

func TestFlowTaskAddToolRequiresStaffCircle(t *testing.T) {
	resourceAccessRules := []policy.ResourceAccessPolicy{{
		Resource: "tool:task_add",
		Actions:  []string{"execute"},
		Circles:  []string{"staff"},
	}}
	httpClient := &recordingHTTPClient{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityTools(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []string{"task_add"})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"task_add"},
	}, nil)

	guestToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID:            "person-1",
			ResourceAccessRules: resourceAccessRules,
		},
	})
	guestResult, errorValue := guestToolSet.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "task_add",
		Input:    json.RawMessage(`{"title":"10분 회의"}`),
	})
	if errorValue != nil {
		t.Fatalf("expected denied tool result: %v", errorValue)
	}
	if !guestResult.Failed() {
		t.Fatalf("expected guest execution denial, got %+v", guestResult)
	}
	if httpClient.requestPath != "" {
		t.Fatalf("expected denied Flow tool not to call capability bridge, got path=%s", httpClient.requestPath)
	}

	staffToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID:            "person-2",
			Circles:             []string{"staff"},
			ResourceAccessRules: resourceAccessRules,
		},
	})
	staffResult, errorValue := staffToolSet.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "task_add",
		Input:    json.RawMessage(`{"title":"10분 회의"}`),
	})
	if errorValue != nil {
		t.Fatalf("expected staff tool result: %v", errorValue)
	}
	if staffResult.Failed() {
		t.Fatalf("expected staff execution success, got %+v", staffResult)
	}
	if httpClient.requestPath != "/v1/tools/task_add/invoke" {
		t.Fatalf("expected Flow capability bridge call, got path=%s body=%s", httpClient.requestPath, httpClient.requestBody)
	}
}

type recordingHTTPClient struct {
	requestPath  string
	requestBody  string
	responseBody string
}

func (httpClient *recordingHTTPClient) Do(request *http.Request) (*http.Response, error) {
	body := []byte{}
	if request.Body != nil {
		body, _ = io.ReadAll(request.Body)
	}
	httpClient.requestPath = request.URL.Path
	httpClient.requestBody = string(body)
	responseBody := httpClient.responseBody
	if responseBody == "" {
		toolName := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/v1/tools/"), "/invoke")
		responseBody = `{"provider":"internkim","selectedBackend":"device","toolName":` + strconv.Quote(toolName) + `,"outcome":"succeeded","content":"opened","status":"ok","result":{}}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(responseBody)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

type staticRuntimeLanguageModel struct {
	content string
}

type authoredRuntimeFailureLanguageModel struct {
	reply string
}

func (languageModel authoredRuntimeFailureLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return languageModel.reply, nil
}

func (languageModel authoredRuntimeFailureLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, errors.New("structured response is not expected")
}

func (languageModel authoredRuntimeFailureLanguageModel) GenerateRecoveryChatCompletion(context.Context, llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	return llm.ChatCompletionResponse{
		FinishReason:    "stop",
		SelectedBackend: "remote",
		Message:         llm.ChatCompletionMessage{Role: "assistant", Content: languageModel.reply},
	}, nil
}

type failingRuntimeRouterLanguageModel struct {
	errorValue error
}

func (languageModel failingRuntimeRouterLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", languageModel.errorValue
}

func (languageModel failingRuntimeRouterLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, languageModel.errorValue
}

func testPlatformMessageCapabilityDescriptors() []CapabilityToolDescriptor {
	return []CapabilityToolDescriptor{
		{Name: "message_context", PolicyResource: "tool:message_context", InputSchema: platformMessageEmptySchema()},
		{Name: "message_search", PolicyResource: "tool:message_search", InputSchema: platformMessageEmptySchema()},
		{Name: "message_send", PolicyResource: "tool:message_send", InputSchema: platformMessageEmptySchema()},
		{Name: "message_update", PolicyResource: "tool:message_update", InputSchema: platformMessageEmptySchema()},
		{Name: "message_delete", PolicyResource: "tool:message_delete", InputSchema: platformMessageDeleteCriteriaSchema()},
	}
}

func platformMessageLiveRegistryResponse(deleteSchema json.RawMessage) string {
	response := map[string]any{
		"deviceCapabilities": []map[string]any{
			{"name": "message_context", "inputSchema": platformMessageEmptySchema()},
			{"name": "message_search", "inputSchema": platformMessageEmptySchema()},
			{"name": "message_send", "inputSchema": platformMessageEmptySchema()},
			{"name": "message_update", "inputSchema": platformMessageEmptySchema()},
			{"name": "message_delete", "inputSchema": deleteSchema},
		},
	}
	document, errorValue := json.Marshal(response)
	if errorValue != nil {
		panic(errorValue)
	}
	return string(document)
}

func platformMessageEmptySchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

func platformMessageDeleteCriteriaSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"messageIDs":{"type":"array","items":{"type":"string"}},"scope":{"type":"string"},"deliveryTarget":{"type":"object","properties":{"type":{"type":"string"},"personHint":{"type":"string"},"channelID":{"type":"string"},"channelName":{"type":"string"}},"additionalProperties":false},"authoredBy":{"type":"string"},"query":{"type":"string"},"limit":{"type":"integer"}},"additionalProperties":false}`)
}

func platformMessageDeleteIDsOnlySchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"messageIDs":{"type":"array","items":{"type":"string"}}},"required":["messageIDs"],"additionalProperties":false}`)
}

func (languageModel staticRuntimeLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel staticRuntimeLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == "bluecollar_turn_router" {
		return llm.StructuredResponse{Content: runtimeTestTurnRouterResponse()}, nil
	}
	return llm.StructuredResponse{Content: languageModel.content}, nil
}

func useRuntimeTestLanguageModel(agentKernel *loop.AgentKernel, content string) {
	languageModel := staticRuntimeLanguageModel{content: content}
	agentKernel.UseLanguageModelProvider(languageModel)
	agentKernel.UseIntakeLanguageModelProvider(languageModel)
	agentKernel.UseIntakeOptions(agentcontract.IntakeOptions{IsEnabled: true})
}

func runtimeTestTurnRouterResponse() string {
	return `{"route":"answer_question","classification":"quick_reply","taskShape":"immediate_reply","level":"xlow","requestedOutputFormats":null,"responseLanguage":"ko","reason":"task launcher test default","userFacingReply":""}`
}

type staticHistoryProvider struct{}

func (historyProvider staticHistoryProvider) FetchHistory(context.Context, string, int) (agentcontract.VisibleContext, error) {
	return agentcontract.VisibleContext{}, nil
}

type recordingRequesterWorkspaceProvisioner struct {
	callCount  int
	provision  func(policy.PersonAccess, string) error
	errorValue error
}

func (provisioner *recordingRequesterWorkspaceProvisioner) ProvisionRequesterWorkspace(ctx context.Context, personAccess policy.PersonAccess, workspaceRootPath string) error {
	_ = ctx
	provisioner.callCount++
	if provisioner.errorValue != nil {
		return provisioner.errorValue
	}
	if provisioner.provision == nil {
		return nil
	}
	return provisioner.provision(personAccess, workspaceRootPath)
}

type failingGraphMemoryStore struct {
	errorValue error
}

func (store failingGraphMemoryStore) AddEpisode(context.Context, memory.MemoryEpisode) (memory.MemoryIngestionResult, error) {
	return memory.MemoryIngestionResult{}, nil
}

func (store failingGraphMemoryStore) SearchFacts(context.Context, memory.MemorySearchRequest) ([]memory.MemoryFact, error) {
	return nil, store.errorValue
}

func runtimeFinishMessage(reply string) string {
	return `{"action":"finish","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"finishMessage":"` + reply + `"}`
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func containsTaskEvent(taskEvents []task.TaskEvent, name string) bool {
	return findTaskEvent(taskEvents, name).Name != ""
}

func countTaskEvents(taskEvents []task.TaskEvent, name string) int {
	count := 0
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == name {
			count++
		}
	}
	return count
}

func launchStepResultBodies(taskEvents []task.TaskEvent) []string {
	bodies := []string{}
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == "agent.launch_step.result" {
			bodies = append(bodies, taskEvent.Body)
		}
	}
	return bodies
}

func findTaskEvent(taskEvents []task.TaskEvent, name string) task.TaskEvent {
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == name {
			return taskEvent
		}
	}
	return task.TaskEvent{}
}

type fakeRequesterEmailResolver struct {
	emailByPersonID map[string]string
}

func (resolver fakeRequesterEmailResolver) ResolvePersonPrimaryEmail(personID string) string {
	return resolver.emailByPersonID[personID]
}

func TestResolveRequesterEmailBackfillsScheduledLaunchFromPersonID(t *testing.T) {
	taskLauncher := &TaskLauncher{requesterEmailResolver: fakeRequesterEmailResolver{
		emailByPersonID: map[string]string{"person-1": "staff@example.com"},
	}}
	resolvedEmail := taskLauncher.resolveRequesterEmail(TaskLaunchRequest{RequesterPersonID: "person-1"})
	if resolvedEmail != "staff@example.com" {
		t.Fatalf("expected resolved email staff@example.com, got %q", resolvedEmail)
	}
}

func TestResolveRequesterEmailLetsPersonIDOverrideSuppliedEmail(t *testing.T) {
	taskLauncher := &TaskLauncher{requesterEmailResolver: fakeRequesterEmailResolver{
		emailByPersonID: map[string]string{"person-1": "mapped@example.com"},
	}}
	resolvedEmail := taskLauncher.resolveRequesterEmail(TaskLaunchRequest{
		RequesterPersonID: "person-1",
		RequesterEmail:    "spoofed@example.com",
	})
	if resolvedEmail != "mapped@example.com" {
		t.Fatalf("expected personID-resolved email to win, got %q", resolvedEmail)
	}
}

func TestResolveRequesterEmailFallsBackToSuppliedWhenPersonUnmapped(t *testing.T) {
	taskLauncher := &TaskLauncher{requesterEmailResolver: fakeRequesterEmailResolver{
		emailByPersonID: map[string]string{},
	}}
	resolvedEmail := taskLauncher.resolveRequesterEmail(TaskLaunchRequest{
		RequesterPersonID: "person-unmapped",
		RequesterEmail:    "event-sender@example.com",
	})
	if resolvedEmail != "event-sender@example.com" {
		t.Fatalf("expected supplied event email as fallback, got %q", resolvedEmail)
	}
}

func TestResolveRequesterEmailWithoutResolverStaysEmpty(t *testing.T) {
	taskLauncher := &TaskLauncher{}
	resolvedEmail := taskLauncher.resolveRequesterEmail(TaskLaunchRequest{RequesterPersonID: "person-1"})
	if resolvedEmail != "" {
		t.Fatalf("expected empty email without resolver, got %q", resolvedEmail)
	}
}

func TestLaunchedAgentTurnRequestCarriesConfiguredAgentIdentity(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	harness := harnesstest.New(taskRunService)
	taskLauncher := NewTaskLauncher(harness, taskRunService, NewToolCatalogBuilder())
	taskLauncher.UseAgentIdentityProvider(func() agentcontract.AgentIdentity {
		return agentcontract.AgentIdentity{Name: "김인턴", Handle: "internkim"}
	})

	if _, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		RequesterPersonID: "person-1",
		ProfileName:       "default",
		ConversationID:    "channel-1",
		Prompt:            "너 누구야?",
	}); errorValue != nil {
		t.Fatalf("expected launch to succeed: %v", errorValue)
	}

	agentIdentity := harness.LastTurnRequest().AgentIdentity
	if agentIdentity.Name != "김인턴" || agentIdentity.Handle != "internkim" {
		t.Fatalf("expected the configured agent identity to reach the harness, got %+v", agentIdentity)
	}
	if agentIdentity.DisplayName() != "김인턴" || agentIdentity.MentionExample() != "@internkim" {
		t.Fatalf("expected the agent to introduce itself by name, got %q and %q", agentIdentity.DisplayName(), agentIdentity.MentionExample())
	}
}

func TestAgentTurnRequestWithoutIdentityProviderStaysEmpty(t *testing.T) {
	taskLauncher := NewTaskLauncher(nil, nil, nil)

	turnRequest := taskLauncher.agentTurnRequestForLaunch(TaskLaunchRequest{}, "default", nil, nil, ConversationResourceScope{})

	if turnRequest.AgentIdentity != (agentcontract.AgentIdentity{}) {
		t.Fatalf("expected an empty agent identity without a provider, got %+v", turnRequest.AgentIdentity)
	}
}
