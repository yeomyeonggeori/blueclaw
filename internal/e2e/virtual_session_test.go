package e2e

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type virtualStructuredOutputCorrectionTestError struct{}

func (virtualStructuredOutputCorrectionTestError) Error() string {
	return "structured output invalid"
}

func (virtualStructuredOutputCorrectionTestError) StructuredOutputCorrection() (llm.StructuredOutputCorrection, bool) {
	return llm.StructuredOutputCorrection{
		Code: "structured_output_invalid",
		Diagnostic: llm.StructuredOutputDiagnostic{
			Category:     llm.StructuredOutputDiagnosticFinishReason,
			FinishReason: llm.StructuredOutputDiagnosticFinishStop,
		},
	}, true
}

func TestDefaultToolPaletteUsesCanonicalNames(t *testing.T) {
	toolNames := allowedToolsOrDefault(nil)
	for _, toolName := range []string{"shell", "ask_input", "file_deliver"} {
		if !slices.Contains(toolNames, toolName) {
			t.Fatalf("expected canonical tool %s, got %+v", toolName, toolNames)
		}
	}
	for _, toolName := range []string{"shell_session", "browser_handoff_openURL", "ask_choice", "file_promote", "file_attach", "task_history", "db_sql"} {
		if slices.Contains(toolNames, toolName) {
			t.Fatalf("expected dead tool %s to be absent, got %+v", toolName, toolNames)
		}
	}
}

func TestExpectedEventCountRejectsRepeatedReadResults(t *testing.T) {
	virtualTurn := VirtualTurn{
		ExpectedEventCounts: []VirtualEventCount{{
			Name:         "tool.task_list.result",
			BodyFragment: "customer task",
			Count:        1,
		}},
	}
	turnResult := VirtualTurnResult{
		FinishMessage: "found",
		Events: []task.TaskEvent{
			{Name: "tool.task_list.result", Body: `{"title":"customer task"}`},
			{Name: "tool.task_list.result", Body: `{"title":"customer task"}`},
		},
	}
	errorValue := assertTurnResult(t.TempDir(), virtualTurn, turnResult)
	if errorValue == nil || !strings.Contains(errorValue.Error(), "expected=1 actual=2") {
		t.Fatalf("expected the exact event count assertion to reject the duplicate read, got %v", errorValue)
	}
	assertions := informationalAssertionResults(virtualTurn, turnResult)
	if len(assertions) != 1 || assertions[0].Satisfied {
		t.Fatalf("expected the duplicate read to remain an informational efficiency mismatch: %+v", assertions)
	}
}

func TestLanguageModelCallAssertionRejectsError(t *testing.T) {
	errorValue := assertLanguageModelCallsSucceeded(VirtualTurnResult{
		LanguageModelCallEvents: []VirtualLanguageModelCallEvent{{
			Kind:       "structured",
			SchemaName: "bluecollar_turn_router",
			IsError:    true,
			Error:      "truncated",
		}},
	})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "bluecollar_turn_router") {
		t.Fatalf("expected strict assertion to reject the model error, got %v", errorValue)
	}
}

func TestLanguageModelCallAssertionAllowsCorrectedTypedError(t *testing.T) {
	observed := &virtualObservedLanguageModel{store: &virtualLanguageModelObservationStore{}}
	request := llm.ChatCompletionRequest{SchemaName: "bluecollar_agent_turn_action"}
	observed.appendCall(virtualChatCallEvent(
		"chat",
		request,
		llm.ChatCompletionResponse{},
		time.Now(),
		virtualStructuredOutputCorrectionTestError{},
	))
	observed.appendCall(VirtualLanguageModelCallEvent{
		Kind:         "chat",
		SchemaName:   "bluecollar_agent_turn_action",
		FinishReason: "tool_calls",
	})

	calls := observed.CallsSince(0)
	if len(calls) != 2 || !calls[0].IsError || !calls[0].WasCorrected {
		t.Fatalf("expected corrected error evidence to remain visible, got %+v", calls)
	}
	if errorValue := assertLanguageModelCallsSucceeded(VirtualTurnResult{LanguageModelCallEvents: calls}); errorValue != nil {
		t.Fatalf("expected corrected typed error to pass strict assertion: %v", errorValue)
	}
}

func TestLanguageModelCallAssertionAllowsCorrectedTypedErrorChain(t *testing.T) {
	observed := &virtualObservedLanguageModel{store: &virtualLanguageModelObservationStore{}}
	request := llm.ChatCompletionRequest{SchemaName: "bluecollar_agent_turn_action"}
	for range 2 {
		observed.appendCall(virtualChatCallEvent(
			"chat",
			request,
			llm.ChatCompletionResponse{},
			time.Now(),
			virtualStructuredOutputCorrectionTestError{},
		))
	}
	observed.appendCall(VirtualLanguageModelCallEvent{
		Kind:         "chat",
		SchemaName:   "bluecollar_agent_turn_action",
		FinishReason: "tool_calls",
	})

	calls := observed.CallsSince(0)
	if len(calls) != 3 || !calls[0].WasCorrected || !calls[1].WasCorrected {
		t.Fatalf("expected both typed errors to be corrected, got %+v", calls)
	}
	if errorValue := assertLanguageModelCallsSucceeded(VirtualTurnResult{LanguageModelCallEvents: calls}); errorValue != nil {
		t.Fatalf("expected corrected typed error chain to pass strict assertion: %v", errorValue)
	}
}

func TestLanguageModelCallAssertionRejectsUnrecoveredTypedError(t *testing.T) {
	observed := &virtualObservedLanguageModel{store: &virtualLanguageModelObservationStore{}}
	observed.appendCall(VirtualLanguageModelCallEvent{
		Kind:                       "chat",
		SchemaName:                 "bluecollar_agent_turn_action",
		IsError:                    true,
		Error:                      "structured output invalid",
		StructuredOutputCorrection: &llm.StructuredOutputCorrection{},
	})
	observed.appendCall(VirtualLanguageModelCallEvent{
		Kind:         "chat",
		SchemaName:   "bluecollar_agent_turn_action",
		FinishReason: "stop",
	})

	calls := observed.CallsSince(0)
	if calls[0].WasCorrected {
		t.Fatalf("expected non-tool response not to correct the error, got %+v", calls)
	}
	if errorValue := assertLanguageModelCallsSucceeded(VirtualTurnResult{LanguageModelCallEvents: calls}); errorValue == nil {
		t.Fatal("expected unrecovered typed error to fail strict assertion")
	}
}

func TestLanguageModelCallAssertionRejectsDeadlineDespiteElapsedCompletionEvent(t *testing.T) {
	turnResult := VirtualTurnResult{
		TaskStatus: task.TaskStatusCompleted,
		Events: []task.TaskEvent{{
			Name: "agent.limit_completed_from_evidence",
			Body: `{"reason":"max_elapsed","source":"typed_evidence"}`,
		}},
		LanguageModelCallEvents: []VirtualLanguageModelCallEvent{{
			Kind:               "structured",
			SchemaName:         "bluecollar_turn_router",
			IsError:            true,
			IsDeadlineExceeded: true,
			Error:              context.DeadlineExceeded.Error(),
		}},
	}

	if errorValue := assertLanguageModelCallsSucceeded(turnResult); errorValue == nil {
		t.Fatal("expected elapsed completion event not to hide a language model deadline")
	}
}

func TestVirtualObservedLanguageModelPreservesChatCapabilityAndMetadata(t *testing.T) {
	observed := newVirtualObservedLanguageModel(virtualChatTestProvider{})
	if _, isDirectChat := observed.(llm.ChatCompleter); isDirectChat {
		t.Fatal("expected virtual observer to expose ChatCompleter only through the optional accessor")
	}
	chatCompleter, isAvailable := llm.ResolveTextChatCompleter(observed)
	if !isAvailable {
		t.Fatal("expected virtual observer chat capability")
	}
	response, errorValue := chatCompleter.GenerateChatCompletion(context.Background(), llm.ChatCompletionRequest{
		Messages: []llm.ChatCompletionMessage{{Role: "user", Content: "reply"}},
	})
	if errorValue != nil || response.Message.Content != "virtual reply" {
		t.Fatalf("expected virtual chat response, got %+v %v", response, errorValue)
	}
	recorder, isRecorder := observed.(virtualLanguageModelCallRecorder)
	if !isRecorder {
		t.Fatal("expected virtual observer call recorder")
	}
	calls := recorder.CallsSince(0)
	if len(calls) != 1 || calls[0].Kind != "chat" || calls[0].Provider != "virtual-provider" || calls[0].Model != "virtual-model" || calls[0].SelectedBackend != "device" || calls[0].FinishReason != "stop" || !calls[0].UsedFallback {
		t.Fatalf("expected exact virtual chat metadata, got %+v", calls)
	}
}

func TestVirtualChatCallEventDerivesActionSchemaForForcedChatOnly(t *testing.T) {
	actionEvent := virtualChatCallEvent("chat", virtualActionChatRequest(), llm.ChatCompletionResponse{
		ProviderName:    "llmd",
		ModelName:       "low-model",
		SelectedBackend: "device",
		FinishReason:    "tool_calls",
		UsedFallback:    true,
	}, time.Now(), nil)
	plainEvent := virtualChatCallEvent("chat", llm.ChatCompletionRequest{}, llm.ChatCompletionResponse{
		ProviderName:    "llmd",
		ModelName:       "low-model",
		SelectedBackend: "device",
		FinishReason:    "stop",
	}, time.Now(), nil)
	if actionEvent.SchemaName != "bluecollar_agent_turn_action" || plainEvent.SchemaName != "" {
		t.Fatalf("expected only forced action chat to carry schema, got %+v %+v", actionEvent, plainEvent)
	}
}

func virtualActionChatRequest() llm.ChatCompletionRequest {
	return llm.ChatCompletionRequest{
		SchemaName: "bluecollar_agent_turn_action",
		Tools: []llm.ChatCompletionTool{{
			Type:     "function",
			Function: llm.ChatCompletionFunction{Name: "bluecollar_agent_turn_action"},
		}},
		ToolChoice: json.RawMessage(`{"type":"function","function":{"name":"bluecollar_agent_turn_action"}}`),
	}
}

func TestVirtualObservedLanguageModelResolvesNestedChatAccessors(t *testing.T) {
	inner := newVirtualObservedLanguageModel(virtualChatTestProvider{})
	outer := newVirtualObservedLanguageModel(inner)
	if _, isDirectChat := outer.(llm.ChatCompleter); isDirectChat {
		t.Fatal("expected nested virtual observer to expose ChatCompleter only through the optional accessor")
	}
	chatCompleter, isAvailable := llm.ResolveTextChatCompleter(outer)
	if !isAvailable {
		t.Fatal("expected nested virtual observer chat capability")
	}
	response, errorValue := chatCompleter.GenerateChatCompletion(context.Background(), llm.ChatCompletionRequest{})
	if errorValue != nil || response.Message.Content != "virtual reply" {
		t.Fatalf("expected nested virtual chat response, got %+v %v", response, errorValue)
	}
	for name, provider := range map[string]llm.LanguageModelProvider{"outer": outer, "inner": inner} {
		recorder, isRecorder := provider.(virtualLanguageModelCallRecorder)
		if !isRecorder {
			t.Fatalf("expected %s virtual observer call recorder", name)
		}
		calls := recorder.CallsSince(0)
		if len(calls) != 1 || calls[0].Kind != "chat" || calls[0].Provider != "virtual-provider" || calls[0].Model != "virtual-model" || calls[0].SelectedBackend != "device" || calls[0].FinishReason != "stop" || !calls[0].UsedFallback {
			t.Fatalf("expected exact %s nested virtual chat metadata, got %+v", name, calls)
		}
	}
}

func TestVirtualObservedLanguageModelDoesNotInventChatCapability(t *testing.T) {
	observed := newVirtualObservedLanguageModel(virtualPlainTestProvider{})
	if _, isAvailable := llm.ResolveTextChatCompleter(observed); isAvailable {
		t.Fatal("expected virtual observer without ChatCompleter to remain unavailable")
	}
	if _, isAvailable := llm.ResolveRecoveryChatCompleter(observed); isAvailable {
		t.Fatal("expected virtual observer without RecoveryChatCompleter to remain unavailable")
	}
	if _, isAvailable := llm.ResolveLocalRecoveryChatCompleter(observed); isAvailable {
		t.Fatal("expected virtual observer without LocalRecoveryChatCompleter to remain unavailable")
	}
}

func TestVirtualObservedLanguageModelPreservesNestedRecoveryChatCapabilities(t *testing.T) {
	inner := newVirtualObservedLanguageModel(virtualChatTestProvider{})
	outer := newVirtualObservedLanguageModel(inner)

	recoveryProvider, hasRecoveryChat := llm.ResolveRecoveryChatCompleter(outer)
	if !hasRecoveryChat {
		t.Fatal("expected nested virtual recovery chat capability")
	}
	localRecoveryProvider, hasLocalRecoveryChat := llm.ResolveLocalRecoveryChatCompleter(outer)
	if !hasLocalRecoveryChat {
		t.Fatal("expected nested virtual local recovery chat capability")
	}
	request := llm.ChatCompletionRequest{Messages: []llm.ChatCompletionMessage{{Role: "user", Content: "failure"}}}
	response, errorValue := recoveryProvider.GenerateRecoveryChatCompletion(context.Background(), request)
	if errorValue != nil || response.Message.Content != "virtual recovery reply" {
		t.Fatalf("expected nested virtual recovery response, got %+v %v", response, errorValue)
	}
	response, errorValue = localRecoveryProvider.GenerateLocalRecoveryChatCompletion(context.Background(), request)
	if errorValue != nil || response.Message.Content != "virtual local recovery reply" {
		t.Fatalf("expected nested virtual local recovery response, got %+v %v", response, errorValue)
	}
	assertVirtualRecoveryChatCall(t, inner, "inner")
	assertVirtualRecoveryChatCall(t, outer, "outer")
}

func assertVirtualRecoveryChatCall(t *testing.T, provider llm.LanguageModelProvider, label string) {
	t.Helper()
	recorder, isRecorder := provider.(virtualLanguageModelCallRecorder)
	if !isRecorder {
		t.Fatalf("expected %s virtual observer call recorder", label)
	}
	calls := recorder.CallsSince(0)
	if len(calls) != 2 {
		t.Fatalf("expected two %s recovery calls, got %+v", label, calls)
	}
	for index, call := range calls {
		expectedKind := []string{"recovery_chat", "local_recovery_chat"}[index]
		if call.Kind != expectedKind {
			t.Fatalf("expected %s recovery kind %q, got %+v", label, expectedKind, call)
		}
		if call.Provider != "virtual-recovery-provider" || call.Model != "virtual-recovery-model" || call.SelectedBackend != "remote" || call.FinishReason != "stop" || call.UsedFallback {
			t.Fatalf("expected %s recovery routing metadata, got %+v", label, call)
		}
	}
}

func TestVirtualObservedLanguageModelRecordsChatErrors(t *testing.T) {
	observed := newVirtualObservedLanguageModel(virtualChatErrorTestProvider{})
	chatCompleter, isAvailable := llm.ResolveTextChatCompleter(observed)
	if !isAvailable {
		t.Fatal("expected virtual observer chat capability")
	}
	_, errorValue := chatCompleter.GenerateChatCompletion(context.Background(), llm.ChatCompletionRequest{})
	if errorValue == nil {
		t.Fatal("expected virtual chat error")
	}
	recorder, isRecorder := observed.(virtualLanguageModelCallRecorder)
	if !isRecorder {
		t.Fatal("expected virtual observer call recorder")
	}
	calls := recorder.CallsSince(0)
	if len(calls) != 1 || !calls[0].IsError || calls[0].Error != "virtual chat failed" || calls[0].Provider != "virtual-provider" || calls[0].Model != "virtual-model" || calls[0].SelectedBackend != "remote" || calls[0].FinishReason != "error" || !calls[0].UsedFallback {
		t.Fatalf("expected exact virtual chat error metadata, got %+v", calls)
	}
}

type virtualPlainTestProvider struct{}

func (virtualPlainTestProvider) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (virtualPlainTestProvider) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, nil
}

type virtualChatTestProvider struct{}

func (virtualChatTestProvider) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (virtualChatTestProvider) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, nil
}

func (virtualChatTestProvider) GenerateChatCompletion(context.Context, llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	return llm.ChatCompletionResponse{
		FinishReason:    "stop",
		ProviderName:    "virtual-provider",
		ModelName:       "virtual-model",
		SelectedBackend: "device",
		UsedFallback:    true,
		Message:         llm.ChatCompletionMessage{Role: "assistant", Content: "virtual reply"},
	}, nil
}

func (virtualChatTestProvider) GenerateRecoveryChatCompletion(context.Context, llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	return llm.ChatCompletionResponse{
		FinishReason:    "stop",
		ProviderName:    "virtual-recovery-provider",
		ModelName:       "virtual-recovery-model",
		SelectedBackend: "remote",
		Message:         llm.ChatCompletionMessage{Role: "assistant", Content: "virtual recovery reply"},
	}, nil
}

func (virtualChatTestProvider) GenerateLocalRecoveryChatCompletion(context.Context, llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	return llm.ChatCompletionResponse{
		FinishReason:    "stop",
		ProviderName:    "virtual-recovery-provider",
		ModelName:       "virtual-recovery-model",
		SelectedBackend: "remote",
		Message:         llm.ChatCompletionMessage{Role: "assistant", Content: "virtual local recovery reply"},
	}, nil
}

type virtualChatErrorTestProvider struct{}

func (virtualChatErrorTestProvider) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (virtualChatErrorTestProvider) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, nil
}

func (virtualChatErrorTestProvider) GenerateChatCompletion(context.Context, llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	return llm.ChatCompletionResponse{
		FinishReason:    "error",
		ProviderName:    "virtual-provider",
		ModelName:       "virtual-model",
		SelectedBackend: "remote",
		UsedFallback:    true,
	}, errors.New("virtual chat failed")
}

func TestMemoryGuidedFollowup(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), MemoryGuidedFollowupScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected memory scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turn results, got %d", len(result.TurnResults))
	}
	secondTurn := result.TurnResults[1]
	if !eventsContain(secondTurn.Events, "agent.task_launched", `"memoryFactCount":`) {
		t.Fatal("expected task launch memory fact count")
	}
	if strings.Contains(secondTurn.FinishMessage, "아까") {
		t.Fatalf("expected concrete recalled preference, got %q", secondTurn.FinishMessage)
	}
}

func TestPlainQuestionAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), PlainQuestionAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected plain question acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	if strings.TrimSpace(turnResult.FinishMessage) == "" {
		t.Fatal("expected non-empty final reply")
	}
	if toolEventCount(turnResult.Events) != 0 {
		t.Fatalf("expected no tool events, got events: %s", summarizeEvents(turnResult.Events))
	}
	if failureEventCount(turnResult.Events) != 0 {
		t.Fatalf("expected no failure events, got events: %s", summarizeEvents(turnResult.Events))
	}
}

func TestFailedAssertionReturnsObservedTurnResult(t *testing.T) {
	scenario := PlainQuestionAcceptanceScenario(t.TempDir())
	scenario.Turns[0].ExpectedTaskStatus = task.TaskStatusFailed

	result, errorValue := RunVirtualSession(context.Background(), scenario)

	if errorValue == nil {
		t.Fatal("expected task status assertion to fail")
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected failing turn result to remain observable, got %d", len(result.TurnResults))
	}
	if result.TurnResults[0].TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected observed completed status, got %q", result.TurnResults[0].TaskStatus)
	}
}

func TestVirtualTaskCapabilityPreservesLifecycleState(t *testing.T) {
	service := virtualCapabilityService{}
	addResponse := service.response("task_add", []byte(`{"input":{"title":"비용 테스트 회귀 확인","goal":"회귀 방지","targetPersonHint":"예시","participantPersonHints":["샘플"]},"context":{}}`))
	discoveryResponse := service.response("task_list", []byte(`{"input":{"query":"비용 테스트 회귀 확인"},"context":{}}`))
	taskID := virtualTaskID(t, discoveryResponse)
	updateResponse := service.response("task_update", []byte(fmt.Sprintf(`{"input":{"taskHint":%q,"title":"비용 테스트 회귀 확인 완료 준비"},"context":{}}`, taskID)))
	listResponse := service.response("task_list", []byte(`{"input":{},"context":{}}`))
	approvalResponse := service.response("task_delete", []byte(fmt.Sprintf(`{"input":{"taskHint":%q},"context":{}}`, taskID)))
	deleteResponse := service.response("task_delete", []byte(fmt.Sprintf(`{"input":{"taskHint":%q},"context":{"isApprovalContinuation":true}}`, taskID)))
	emptyListResponse := service.response("task_list", []byte(`{"input":{},"context":{}}`))

	var addDocument struct {
		Result  map[string]any                `json:"result"`
		Effects []toolcontract.ResourceEffect `json:"effects"`
	}
	if errorValue := json.Unmarshal([]byte(addResponse), &addDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	if addDocument.Result["taskID"] != "task-1" ||
		addDocument.Result["content"] != "비용 테스트 회귀 확인" ||
		addDocument.Result["goal"] != "회귀 방지" ||
		addDocument.Result["ownerName"] != "예시" {
		t.Fatalf("expected canonical created task result, got %s", addResponse)
	}
	if _, isFound := addDocument.Result["targetPersonHint"]; isFound {
		t.Fatalf("task_add result must not expose input-only targetPersonHint: %s", addResponse)
	}
	if _, isFound := addDocument.Result["participantPersonHints"]; isFound {
		t.Fatalf("task_add result must not expose input-only participantPersonHints: %s", addResponse)
	}
	if participantNames := stringSliceValue(addDocument.Result["participantNames"]); !slices.Equal(participantNames, []string{"샘플"}) {
		t.Fatalf("expected canonical participant names, got %s", addResponse)
	}
	if len(addDocument.Effects) != 1 || addDocument.Effects[0] != (toolcontract.ResourceEffect{ObjectType: "task", Effect: "created", ID: "task-1"}) {
		t.Fatalf("expected canonical task_add effect, got %s", addResponse)
	}
	if !strings.Contains(updateResponse, `"content":"비용 테스트 회귀 확인 완료 준비"`) {
		t.Fatalf("expected updated title, got %s", updateResponse)
	}
	if !strings.Contains(listResponse, `"content":"비용 테스트 회귀 확인 완료 준비"`) {
		t.Fatalf("expected list to return updated task, got %s", listResponse)
	}
	if !strings.Contains(approvalResponse, `"errorCode":"approval_required"`) {
		t.Fatalf("expected delete approval gate, got %s", approvalResponse)
	}
	if !strings.Contains(deleteResponse, `"deleted":true`) {
		t.Fatalf("expected approved delete, got %s", deleteResponse)
	}
	if strings.Contains(emptyListResponse, `"taskID":"task-1"`) {
		t.Fatalf("expected deleted task to be absent, got %s", emptyListResponse)
	}
}

func virtualTaskID(t *testing.T, response string) string {
	t.Helper()
	var document struct {
		Result struct {
			Tasks []map[string]any `json:"tasks"`
		} `json:"result"`
	}
	if errorValue := json.Unmarshal([]byte(response), &document); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(document.Result.Tasks) != 1 {
		t.Fatalf("expected one discovered task, got %s", response)
	}
	taskID := strings.TrimSpace(stringValue(document.Result.Tasks[0]["taskID"]))
	if taskID == "" {
		t.Fatalf("expected discovered task ID, got %s", response)
	}
	return taskID
}

func TestVirtualTaskMutationRejectsUnknownTaskID(t *testing.T) {
	service := virtualCapabilityService{}
	service.response("task_add", []byte(`{"input":{"title":"비용 테스트 회귀 확인"},"context":{}}`))

	for _, response := range []string{
		service.response("task_update", []byte(`{"input":{"taskHint":"task-missing","title":"변경됨"},"context":{}}`)),
		service.response("task_delete", []byte(`{"input":{"taskHint":"task-missing"},"context":{"isApprovalContinuation":true}}`)),
	} {
		if !strings.Contains(response, `"errorCode":"not_found"`) {
			t.Fatalf("expected mutation without a resolvable taskHint to fail, got %s", response)
		}
	}
}

func TestDOCXAttachmentValidationRejectsTextAndAcceptsCanonicalPackage(t *testing.T) {
	invalidPath := filepath.Join(t.TempDir(), "document.docx")
	if errorValue := os.WriteFile(invalidPath, []byte("plain text renamed as docx"), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := validateDOCXAttachment(invalidPath, toolcontract.FileAttachment{DevicePath: invalidPath}); errorValue == nil {
		t.Fatal("expected renamed text file to fail docx validation")
	}

	placeholderPath := filepath.Join(t.TempDir(), "placeholder.docx")
	writeDOCX(t, placeholderPath, map[string]string{
		"[Content_Types].xml":          "<xml/>",
		"word/document.xml":            "<xml/>",
		"word/_rels/document.xml.rels": "<xml/>",
	})
	if errorValue := validateDOCXAttachment(placeholderPath, toolcontract.FileAttachment{DevicePath: placeholderPath}); errorValue == nil {
		t.Fatal("expected placeholder XML to fail docx validation")
	}

	validPath := filepath.Join(t.TempDir(), "document.docx")
	writeCanonicalDOCX(t, validPath)
	if errorValue := validateDOCXAttachment(validPath, toolcontract.FileAttachment{DevicePath: validPath}); errorValue != nil {
		t.Fatalf("expected canonical docx package to pass validation: %v", errorValue)
	}
}

func virtualServeRequestBody(input string) []byte {
	content := []byte("virtual site bundle")
	digest := sha256.Sum256(content)
	return []byte(`{"input":` + input + `,"transport":{"siteSourceBundle":{"workspacePath":"~/sites/demo","contentBase64":"` +
		base64.StdEncoding.EncodeToString(content) + `","format":"tar.gz","sha256":"` + hex.EncodeToString(digest[:]) + `"}}}`)
}

func TestVirtualSiteServeRequiresValidSourceBundle(t *testing.T) {
	service := virtualCapabilityService{workspacePath: t.TempDir()}
	input := `{"title":"Local Fleet Studio","sourceWorkspacePath":"~/sites/demo","mode":"publish"}`

	withoutBundle := service.response("site_serve", []byte(`{"input":`+input+`}`))
	if !strings.Contains(withoutBundle, `"status":"error"`) || service.sitePublished {
		t.Fatalf("expected serve without a source bundle to fail closed, got %s", withoutBundle)
	}

	tamperedBundle := []byte(strings.Replace(string(virtualServeRequestBody(input)), `"sha256":"`, `"sha256":"0000`, 1))
	tampered := service.response("site_serve", tamperedBundle)
	if !strings.Contains(tampered, `"status":"error"`) || service.sitePublished {
		t.Fatalf("expected serve with a tampered bundle hash to fail closed, got %s", tampered)
	}

	response := service.response("site_serve", virtualServeRequestBody(input))
	if !strings.Contains(response, `"status":"published"`) ||
		!strings.Contains(response, `"slug":"local-fleet-studio"`) ||
		!strings.Contains(response, `"mode":"publish"`) ||
		!strings.Contains(response, `"sourceSHA256"`) ||
		!strings.Contains(response, `"objectType":"website"`) ||
		!strings.Contains(response, `"effect":"published"`) ||
		!strings.Contains(response, `"url":"https://local-fleet-studio.device.example.test"`) {
		t.Fatalf("expected canonical serve publish result, got %s", response)
	}
	if strings.Contains(response, `"effect":"previewed"`) || strings.Contains(response, `"id":"site-1"`) {
		t.Fatalf("expected only the mode-conditional URL effect, got %s", response)
	}

	previewInput := `{"title":"Local Fleet Studio","sourceWorkspacePath":"~/sites/demo","mode":"preview","siteReference":"local-fleet-studio"}`
	previewResponse := service.response("site_serve", virtualServeRequestBody(previewInput))
	if !strings.Contains(previewResponse, `"status":"previewed"`) ||
		!strings.Contains(previewResponse, `"previewURL":"https://local-fleet-studio.device.example.test/__preview/preview-1"`) ||
		!strings.Contains(previewResponse, `"effect":"previewed"`) {
		t.Fatalf("expected canonical serve preview result, got %s", previewResponse)
	}
}

func TestVirtualSiteOperationsNeverCreateMissingSites(t *testing.T) {
	service := virtualCapabilityService{workspacePath: t.TempDir()}
	requests := map[string][]byte{
		"site_serve":   virtualServeRequestBody(`{"title":"Ghost","sourceWorkspacePath":"~/sites/demo","mode":"publish","siteReference":"missing-site"}`),
		"site_list":    []byte(`{"input":{"siteReference":"missing-site"}}`),
		"site_unserve": []byte(`{"input":{"siteReference":"missing-site"},"context":{"isApprovalContinuation":true}}`),
	}

	for toolName, requestBody := range requests {
		response := service.response(toolName, requestBody)
		if !strings.Contains(response, `"errorCode":"not_found"`) {
			t.Fatalf("expected %s to reject a missing site, got %s", toolName, response)
		}
		if service.site != nil {
			t.Fatalf("expected %s to leave the site store empty", toolName)
		}
	}
}

func TestVirtualSiteFixtureUsesExactIdentity(t *testing.T) {
	service := virtualCapabilityService{workspacePath: t.TempDir()}
	errorValue := service.loadInitialSite(&VirtualSiteFixture{
		SiteID:      "site-1",
		Slug:        "demo",
		Title:       "Local Fleet Studio",
		IsPublished: true,
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	for _, siteReference := range []string{"site-1", "demo"} {
		response := service.response("site_list", []byte(`{"input":{"siteReference":"`+siteReference+`"}}`))
		if !strings.Contains(response, `"siteID":"site-1"`) || !strings.Contains(response, `"status":"published"`) {
			t.Fatalf("expected fixture lookup by %s, got %s", siteReference, response)
		}
	}
	response := service.response("site_serve", virtualServeRequestBody(`{"title":"Local Fleet Studio","sourceWorkspacePath":"~/sites/demo","mode":"publish","siteReference":"other-site"}`))
	if !strings.Contains(response, `"errorCode":"not_found"`) {
		t.Fatalf("expected serve with an unknown reference to fail exact lookup, got %s", response)
	}

	for _, initialSite := range []*VirtualSiteFixture{
		{SiteID: " site-1", Slug: "demo", Title: "Local Fleet Studio"},
		{SiteID: "site-1", Slug: "Demo", Title: "Local Fleet Studio"},
		{SiteID: "site-1", Slug: "demo", Title: " Local Fleet Studio"},
	} {
		if errorValue := service.loadInitialSite(initialSite); errorValue == nil {
			t.Fatalf("expected malformed fixture to fail: %+v", initialSite)
		}
	}
}

func TestVirtualSiteToolsUseCanonicalThreeToolContracts(t *testing.T) {
	expectedToolNames := []string{"site_serve", "site_list", "site_unserve"}
	if toolNames := sitePrototypeCapabilityToolNames(); !slices.Equal(toolNames, expectedToolNames) {
		t.Fatalf("expected canonical site tool surface, got %v", toolNames)
	}

	listInputSchema := virtualCapabilityInputSchema("site_list")
	if strings.Contains(listInputSchema, `"siteID"`) || strings.Contains(listInputSchema, `"checkLive"`) {
		t.Fatalf("expected site_list to accept only an optional siteReference, got %s", listInputSchema)
	}

	expectedRequiredFields := map[string][]string{
		"site_serve":   {"siteID", "slug", "mode", "sourceSHA256"},
		"site_list":    {"sites"},
		"site_unserve": {"siteID", "slug", "unserved"},
	}
	for _, toolName := range expectedToolNames {
		descriptor := virtualCapabilityToolDescriptor(toolName)
		if descriptor.SideEffectClass != toolcontract.ToolSideEffectRead && len(descriptor.InputIntentSchema) == 0 {
			t.Fatalf("expected canonical %s input intent schema", toolName)
		}
		contract := virtualCapabilityToolResultContract(toolName)
		if contract == nil {
			t.Fatalf("expected %s result contract", toolName)
		}
		var resultSchema struct {
			Required []string `json:"required"`
		}
		if errorValue := json.Unmarshal(contract.Schema, &resultSchema); errorValue != nil ||
			!slices.Equal(resultSchema.Required, expectedRequiredFields[toolName]) {
			t.Fatalf("expected exact %s required result fields, got %s", toolName, contract.Schema)
		}
	}

	serveContract := virtualCapabilityToolResultContract("site_serve")
	if len(serveContract.Effects) != 2 ||
		serveContract.Effects[0].Effect != "previewed" ||
		serveContract.Effects[0].ResultField != "previewURL" ||
		serveContract.Effects[0].EffectIdentity != "url" ||
		serveContract.Effects[0].When == nil ||
		serveContract.Effects[0].When.ResultField != "mode" ||
		string(serveContract.Effects[0].When.Equals) != `"preview"` ||
		serveContract.Effects[1].Effect != "published" ||
		serveContract.Effects[1].ResultField != "publishedURL" ||
		serveContract.Effects[1].EffectIdentity != "url" ||
		serveContract.Effects[1].When == nil ||
		string(serveContract.Effects[1].When.Equals) != `"publish"` {
		t.Fatalf("expected mode-conditional serve effects, got %+v", serveContract.Effects)
	}
	listContract := virtualCapabilityToolResultContract("site_list")
	if len(listContract.Effects) != 0 {
		t.Fatalf("expected site_list to have no effects, got %+v", listContract.Effects)
	}
	unserveContract := virtualCapabilityToolResultContract("site_unserve")
	if len(unserveContract.Effects) != 1 ||
		unserveContract.Effects[0].ObjectType != "website" ||
		unserveContract.Effects[0].Effect != "deleted" ||
		unserveContract.Effects[0].ResultField != "siteID" ||
		unserveContract.Effects[0].EffectIdentity != "id" {
		t.Fatalf("expected exact site_unserve effect contract, got %+v", unserveContract.Effects)
	}

	if descriptor := virtualCapabilityToolDescriptor("site_unserve"); !descriptor.RequiresApproval {
		t.Fatal("expected site_unserve to require approval")
	}
	if descriptor := virtualCapabilityToolDescriptor("site_serve"); descriptor.SideEffectClass != toolcontract.ToolSideEffectSitePublish {
		t.Fatalf("expected site_serve site publish semantics, got %+v", descriptor)
	}
	expectedCompletionActions := map[string]string{
		"site_serve":   "serve_site",
		"site_unserve": "delete_site",
	}
	for toolName, action := range expectedCompletionActions {
		descriptor := virtualCapabilityToolDescriptor(toolName)
		if descriptor.CompletionEvidence == nil ||
			descriptor.CompletionEvidence.Action != action ||
			descriptor.CompletionEvidence.TargetKind != "site" {
			t.Fatalf("expected canonical %s completion evidence, got %+v", toolName, descriptor.CompletionEvidence)
		}
	}

	for _, removedToolName := range []string{"site_create", "site_status", "site_preview", "site_publish", "site_delete", "site_history", "site_rollback", "site_repair"} {
		if slices.Contains(sitePrototypeToolNames(), removedToolName) ||
			slices.Contains(sitePrototypeCapabilityToolNames(), removedToolName) ||
			virtualCapabilityToolResultContract(removedToolName) != nil {
			t.Fatalf("expected removed site tool %s to stay outside the scripted surface", removedToolName)
		}
	}
}

func TestVirtualDomainToolsUseGeneratedResultContracts(t *testing.T) {
	for _, toolName := range virtualGeneratedResultContractToolNames {
		generatedDescriptor, isFound := virtualCanonicalCapabilityToolDescriptor(toolName)
		if !isFound || generatedDescriptor.ResultContract == nil {
			t.Fatalf("expected generated result contract for %s", toolName)
		}
		generatedDocument, errorValue := json.Marshal(generatedDescriptor.ResultContract)
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		virtualDocument, errorValue := json.Marshal(virtualCapabilityToolResultContract(toolName))
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if string(virtualDocument) != string(generatedDocument) {
			t.Fatalf("expected %s to use its generated result contract", toolName)
		}
	}
}

func TestVirtualMessageToolsUseGeneratedCanonicalContracts(t *testing.T) {
	expectedRequiredFields := map[string][]string{
		"message_context": {"platform", "conversationID", "conversationType", "channelID", "channelName", "replyTargetID", "rootMessageID", "currentMessageID", "requesterPersonID", "requesterPlatformUserID", "botUserID", "botUsername"},
		"message_search":  {"scope", "queries", "authoredBy", "messageIDs", "candidates", "hasMore"},
		"message_send":    {"messageIDs", "deliveryStatus"},
		"message_update":  {"messageID", "deliveryStatus", "messageUpdated"},
		"message_delete":  {"messageIDs", "deliveryStatus"},
		"channel_update":  {"channelID", "updated"},
	}
	approvalGatedToolNames := map[string]bool{
		"message_send":   true,
		"message_update": false,
		"message_delete": true,
		"channel_update": true,
	}
	expectedEffects := map[string]agentruntime.CapabilityResourceEffectContract{
		"message_send":   {ObjectType: "message", Effect: "sent", ResultField: "messageIDs", EffectIdentity: "id"},
		"message_update": {ObjectType: "message", Effect: "updated", ResultField: "messageID", EffectIdentity: "id"},
		"message_delete": {ObjectType: "message", Effect: "deleted", ResultField: "messageIDs", EffectIdentity: "id"},
		"channel_update": {ObjectType: "channel", Effect: "updated", ResultField: "channelID", EffectIdentity: "id"},
	}

	for _, toolName := range virtualCanonicalMessageToolNames {
		descriptor := virtualCapabilityToolDescriptor(toolName)
		if strings.HasPrefix(descriptor.Description, "Virtual capability") || descriptor.ResultContract == nil {
			t.Fatalf("expected generated canonical descriptor for %s, got %+v", toolName, descriptor)
		}
		var resultSchema struct {
			Required []string `json:"required"`
		}
		if errorValue := json.Unmarshal(descriptor.ResultContract.Schema, &resultSchema); errorValue != nil {
			t.Fatal(errorValue)
		}
		if !slices.Equal(resultSchema.Required, expectedRequiredFields[toolName]) {
			t.Fatalf("expected canonical %s result fields, got %v", toolName, resultSchema.Required)
		}
		expectedEffect, hasEffect := expectedEffects[toolName]
		if !hasEffect {
			if len(descriptor.ResultContract.Effects) != 0 {
				t.Fatalf("expected read-only %s contract, got %+v", toolName, descriptor.ResultContract.Effects)
			}
			continue
		}
		if len(descriptor.InputIntentSchema) == 0 {
			t.Fatalf("expected canonical %s input intent schema", toolName)
		}
		if descriptor.RequiresApproval != approvalGatedToolNames[toolName] {
			t.Fatalf("expected %s approval gate to be %v, got %+v", toolName, approvalGatedToolNames[toolName], descriptor)
		}
		if len(descriptor.ResultContract.Effects) != 1 || descriptor.ResultContract.Effects[0] != expectedEffect {
			t.Fatalf("expected canonical %s mutation contract, got %+v", toolName, descriptor)
		}
	}
}

func TestVirtualCapabilityDescriptorMergePreservesCanonicalInputIntentSchema(t *testing.T) {
	descriptor := mergeVirtualCapabilityToolDescriptor(
		virtualCapabilityToolDescriptor("task_update"),
		agentruntime.CapabilityToolDescriptor{Name: "task_update", RequiresApproval: true},
	)
	if len(descriptor.InputIntentSchema) == 0 || !descriptor.RequiresApproval {
		t.Fatalf("expected canonical intent and configured approval, got %+v", descriptor)
	}

	overrideSchema := json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"}},"additionalProperties":false}`)
	descriptor = mergeVirtualCapabilityToolDescriptor(
		descriptor,
		agentruntime.CapabilityToolDescriptor{Name: "task_update", InputIntentSchema: overrideSchema},
	)
	if string(descriptor.InputIntentSchema) != string(overrideSchema) {
		t.Fatalf("expected explicit input intent override, got %s", descriptor.InputIntentSchema)
	}
}

func TestVirtualMessageServiceReturnsCanonicalContextSearchDeleteAndChannelResults(t *testing.T) {
	service := virtualCapabilityService{}

	contextResult, contextEffects := virtualCapabilityResponseResult(t, service.response("message_context", []byte(`{"input":{}}`)))
	if contextResult["platform"] != "mattermost" || contextResult["conversationID"] != "virtual-conversation-1" || len(contextEffects) != 0 {
		t.Fatalf("unexpected message context result=%+v effects=%+v", contextResult, contextEffects)
	}

	searchResult, searchEffects := virtualCapabilityResponseResult(t, service.response("message_search", []byte(`{"input":{"scope":"currentChannel","queries":["공지"],"authoredBy":"assistant"}}`)))
	if !slices.Equal(stringSliceValue(searchResult["messageIDs"]), []string{"virtual-platform-message-001"}) ||
		searchResult["scope"] != "currentChannel" ||
		len(searchEffects) != 0 {
		t.Fatalf("unexpected message search result=%+v effects=%+v", searchResult, searchEffects)
	}

	approvalResponse := service.response("message_delete", []byte(`{"input":{"messageIDs":["virtual-platform-message-001"]},"context":{}}`))
	if !strings.Contains(approvalResponse, `"errorCode":"approval_required"`) {
		t.Fatalf("expected message delete approval, got %s", approvalResponse)
	}
	deleteResult, deleteEffects := virtualCapabilityResponseResult(t, service.response(
		"message_delete",
		[]byte(`{"input":{"messageIDs":["virtual-platform-message-001","virtual-platform-message-002"]},"context":{"isApprovalContinuation":true}}`),
	))
	if deleteResult["deliveryStatus"] != "deleted" ||
		!slices.Equal(stringSliceValue(deleteResult["messageIDs"]), []string{"virtual-platform-message-001", "virtual-platform-message-002"}) ||
		len(deleteEffects) != 2 {
		t.Fatalf("unexpected message delete result=%+v effects=%+v", deleteResult, deleteEffects)
	}

	channelResult, channelEffects := virtualCapabilityResponseResult(t, service.response(
		"channel_update",
		[]byte(`{"input":{"channelID":"virtual-channel-1","header":"분기 공지"},"context":{"isApprovalContinuation":true}}`),
	))
	if channelResult["channelID"] != "virtual-channel-1" || channelResult["updated"] != true || len(channelEffects) != 1 ||
		channelEffects[0].ObjectType != "channel" || channelEffects[0].ID != "virtual-channel-1" {
		t.Fatalf("unexpected channel update result=%+v effects=%+v", channelResult, channelEffects)
	}
}

func virtualCapabilityResponseResult(t *testing.T, response string) (map[string]any, []toolcontract.ResourceEffect) {
	t.Helper()
	var document struct {
		Result  map[string]any                `json:"result"`
		Effects []toolcontract.ResourceEffect `json:"effects"`
	}
	if errorValue := json.Unmarshal([]byte(response), &document); errorValue != nil {
		t.Fatal(errorValue)
	}
	return document.Result, document.Effects
}

func TestVirtualDocumentReadReturnsCanonicalWorkspaceContent(t *testing.T) {
	workspacePath := t.TempDir()
	documentsPath := filepath.Join(workspacePath, "documents")
	if errorValue := os.MkdirAll(documentsPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	markdownPath := filepath.Join(documentsPath, "review.md")
	if errorValue := os.WriteFile(markdownPath, []byte("# 분기 결산\n상태: 초안"), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	docxPath := filepath.Join(documentsPath, "review.docx")
	writeDOCX(t, docxPath, map[string]string{
		"[Content_Types].xml":          `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"word/document.xml":            `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>분기 결산</w:t></w:r><w:r><w:t>검토 완료</w:t></w:r></w:p></w:body></w:document>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="test" Target="document.xml"/></Relationships>`,
	})
	service := virtualCapabilityService{workspacePath: workspacePath}

	testCases := []struct {
		path     string
		contains []string
	}{
		{path: "/workspace/documents/review.md", contains: []string{"분기 결산", "상태: 초안"}},
		{path: "/workspace/documents/review.docx", contains: []string{"분기 결산", "검토 완료"}},
	}
	for _, testCase := range testCases {
		result, effects := virtualCapabilityResponseResult(t, service.response(
			"document_read",
			[]byte(`{"input":{"path":`+quote(testCase.path)+`}}`),
		))
		if result["status"] != "ok" || result["path"] != testCase.path || result["format"] != "markdown" || result["truncated"] != false {
			t.Fatalf("unexpected canonical document result for %s: %+v", testCase.path, result)
		}
		content := stringValue(result["content"])
		for _, fragment := range testCase.contains {
			if !strings.Contains(content, fragment) {
				t.Fatalf("document result for %s is missing %q: %+v", testCase.path, fragment, result)
			}
		}
		if len(effects) != 0 {
			t.Fatalf("document_read must remain read-only, got %+v", effects)
		}
	}
}

func writeCanonicalDOCX(t *testing.T, path string) {
	writeDOCX(t, path, map[string]string{
		"[Content_Types].xml":          `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"word/document.xml":            `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Test</w:t></w:r></w:p></w:body></w:document>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="document.xml"/></Relationships>`,
	})
}

func writeDOCX(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	file, errorValue := os.Create(path)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	archive := zip.NewWriter(file)
	for name, content := range entries {
		entry, entryError := archive.Create(name)
		if entryError != nil {
			_ = file.Close()
			t.Fatal(entryError)
		}
		if _, entryError = entry.Write([]byte(content)); entryError != nil {
			_ = file.Close()
			t.Fatal(entryError)
		}
	}
	if errorValue := archive.Close(); errorValue != nil {
		_ = file.Close()
		t.Fatal(errorValue)
	}
	if errorValue := file.Close(); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func TestVirtualCapabilityCatalogUsesOperationSchemas(t *testing.T) {
	var catalog struct {
		DeviceCapabilities []struct {
			Name        string `json:"name"`
			InputSchema struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			} `json:"inputSchema"`
		} `json:"deviceCapabilities"`
	}
	if errorValue := json.Unmarshal([]byte(virtualCapabilityCatalogResponse(map[string]bool{"task_update": true, "task_delete": true})), &catalog); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(catalog.DeviceCapabilities) != 2 {
		t.Fatalf("expected two descriptors, got %+v", catalog.DeviceCapabilities)
	}
	for _, descriptor := range catalog.DeviceCapabilities {
		if _, hasTaskHint := descriptor.InputSchema.Properties["taskHint"]; !hasTaskHint || !slices.Contains(descriptor.InputSchema.Required, "taskHint") {
			t.Fatalf("%s input schema must require taskHint", descriptor.Name)
		}
		if _, hasQuery := descriptor.InputSchema.Properties["query"]; hasQuery {
			t.Fatalf("%s input schema must not expose query", descriptor.Name)
		}
		if _, hasPersonHint := descriptor.InputSchema.Properties["targetPersonHint"]; hasPersonHint {
			t.Fatalf("%s input schema must not expose targetPersonHint", descriptor.Name)
		}
	}
	updateSchema := catalog.DeviceCapabilities[1].InputSchema.Properties
	if _, hasTitle := updateSchema["title"]; !hasTitle {
		updateSchema = catalog.DeviceCapabilities[0].InputSchema.Properties
	}
	if _, hasTitle := updateSchema["title"]; !hasTitle {
		t.Fatal("task_update input schema must expose title")
	}
	if _, hasEndsAt := updateSchema["endsAt"]; !hasEndsAt {
		t.Fatal("task_update input schema must expose endsAt")
	}
}

func TestWebSearchAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), WebSearchAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected web search acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	if countEvents(turnResult.Events, "tool.web_search.requested") != 1 {
		t.Fatalf("expected one web_search request, got events: %s", summarizeEvents(turnResult.Events))
	}
	if !strings.Contains(turnResult.FinishMessage, "BlueclawSearchStubToken") {
		t.Fatalf("expected final reply to contain search stub token, got %q", turnResult.FinishMessage)
	}
}

func TestFileWriteAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), FileWriteAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected file write scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if turnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected completed turn, got %s", turnResult.TaskStatus)
	}
	if countEvents(turnResult.Events, "tool.file_write.requested") != 1 {
		t.Fatalf("expected one file_write request, got events: %s", summarizeEvents(turnResult.Events))
	}
	if countEvents(turnResult.Events, "tool.file_deliver.requested") != 1 {
		t.Fatalf("expected one file_deliver request, got events: %s", summarizeEvents(turnResult.Events))
	}
	if countEvents(turnResult.Events, "tool.shell.requested") != 0 {
		t.Fatalf("file_write result contract must avoid redundant terminal verification, got events: %s", summarizeEvents(turnResult.Events))
	}
}

func TestFileWriteAcceptanceRejectsWrongPersistedContent(t *testing.T) {
	scenario := FileWriteAcceptanceScenario(t.TempDir())
	scenario.Turns[0].ActionResponses[0] = actionCallTool("file_write", `{"path":"work/customer-support/faq-revision.json","content":"{}\n"}`)

	_, errorValue := RunVirtualSession(context.Background(), scenario)
	if errorValue == nil || !strings.Contains(errorValue.Error(), "FAQ 개편") {
		t.Fatalf("expected attached JSON content validation failure, got %v", errorValue)
	}
}

func TestVirtualSessionAcceptsReactionOnlyTurn(t *testing.T) {
	scenario := VirtualSessionScenario{
		Name:                  "reaction-only",
		ArtifactDirectoryPath: t.TempDir(),
		AddressingResponse:    `{"target":"anyone","shouldRespond":false,"reactionEmoji":"eyes","dutyMatch":false,"dutyName":"","dutyConfidence":0}`,
		Turns: []VirtualTurn{{
			Prompt:           "참고로 공유합니다.",
			ExpectedResponse: VirtualResponseReact,
			ConversationType: "channel",
		}},
	}

	result, errorValue := RunVirtualSession(context.Background(), scenario)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(result.TurnResults[0].Reactions) != 1 || result.TurnResults[0].Reactions[0].EmojiName != "eyes" {
		t.Fatalf("expected eyes reaction, got %+v", result.TurnResults[0].Reactions)
	}
}

func TestGWSDisabled(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), GWSDisabledScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected gws disabled scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if eventsContain(turnResult.Events, "tool.google.drive.import_pptx.requested", "google.drive.import_pptx") {
		t.Fatal("disabled google tool must not enter the model palette or runtime")
	}
}

func TestVirtualCalendarMutationUsesExactEventHint(t *testing.T) {
	service := virtualCapabilityService{}
	addResponse := service.calendarResponse("event_add", []byte(`{"input":{"title":"비용 테스트 일정","startISO":"2026-07-16T10:00:00+09:00","endISO":"2026-07-16T11:00:00+09:00","people":["지원팀"]},"context":{"requesterPersonID":"person-1","requesterName":"이수현","requesterEmail":"soohyun@example.com"}}`))
	if !strings.Contains(addResponse, `"eventID":"calendar-event-001"`) ||
		!strings.Contains(addResponse, `"objectType":"calendar"`) ||
		!strings.Contains(addResponse, `"effect":"created"`) ||
		!strings.Contains(addResponse, `"name":"지원팀"`) ||
		!strings.Contains(addResponse, `"name":"이수현"`) {
		t.Fatalf("expected canonical created event and effect, got %s", addResponse)
	}
	updateResponse := service.calendarResponse("event_update", []byte(`{"input":{"eventHint":"calendar-event-001","startISO":"2026-07-16T14:00:00+09:00","endISO":"2026-07-16T15:00:00+09:00"}}`))
	if !strings.Contains(updateResponse, `"status":"ok"`) || !strings.Contains(updateResponse, `T14:00:00+09:00`) {
		t.Fatalf("expected exact-ID hint update, got %s", updateResponse)
	}
	titleUpdateResponse := service.calendarResponse("event_update", []byte(`{"input":{"eventHint":"비용 테스트 일정","title":"비용 테스트 일정 완료"}}`))
	if !strings.Contains(titleUpdateResponse, `"status":"ok"`) || !strings.Contains(titleUpdateResponse, `"title":"비용 테스트 일정 완료"`) {
		t.Fatalf("expected exact-title hint update, got %s", titleUpdateResponse)
	}
	noPatchResponse := service.calendarResponse("event_update", []byte(`{"input":{"eventHint":"calendar-event-001"}}`))
	if !strings.Contains(noPatchResponse, `"errorCode":"invalid_input"`) {
		t.Fatalf("expected hint-only update to fail, got %s", noPatchResponse)
	}
	queryResponse := service.calendarResponse("event_update", []byte(`{"input":{"query":"비용 테스트","title":"새 일정 이름"}}`))
	if !strings.Contains(queryResponse, `"status":"error"`) || !strings.Contains(queryResponse, `not found`) {
		t.Fatalf("expected query update without eventHint to fail, got %s", queryResponse)
	}
	deleteResponse := service.calendarResponse("event_delete", []byte(`{"input":{"eventHint":"calendar-event-001"},"context":{"isApprovalContinuation":true}}`))
	if !strings.Contains(deleteResponse, `"eventID":"calendar-event-001"`) ||
		!strings.Contains(deleteResponse, `"deleted":true`) ||
		!strings.Contains(deleteResponse, `"effect":"deleted"`) {
		t.Fatalf("expected canonical deleted event and effect, got %s", deleteResponse)
	}
}

func TestVirtualCalendarListHonorsWindowQueryAndLimit(t *testing.T) {
	service := virtualCapabilityService{}
	for _, input := range []string{
		`{"title":"비용 점검 A","startISO":"2026-07-16T10:00:00+09:00","endISO":"2026-07-16T11:00:00+09:00"}`,
		`{"title":"채용 점검","startISO":"2026-07-16T12:00:00+09:00","endISO":"2026-07-16T13:00:00+09:00"}`,
		`{"title":"비용 점검 B","startISO":"2026-07-17T10:00:00+09:00","endISO":"2026-07-17T11:00:00+09:00"}`,
	} {
		service.calendarResponse("event_add", []byte(`{"input":`+input+`}`))
	}
	response := service.calendarResponse("event_list", []byte(`{"input":{"startISO":"2026-07-16T00:00:00+09:00","endISO":"2026-07-17T00:00:00+09:00","query":"비용","limit":1}}`))
	if !strings.Contains(response, `"eventID":"calendar-event-001"`) ||
		strings.Contains(response, `"eventID":"calendar-event-002"`) ||
		strings.Contains(response, `"eventID":"calendar-event-003"`) {
		t.Fatalf("expected bounded calendar listing, got %s", response)
	}
	for _, input := range []string{
		`{"startISO":"2026-07-16T00:00:00+09:00"}`,
		`{"limit":1.5}`,
	} {
		response = service.calendarResponse("event_list", []byte(`{"input":`+input+`}`))
		if !strings.Contains(response, `"errorCode":"invalid_input"`) {
			t.Fatalf("expected invalid bounded listing for %s, got %s", input, response)
		}
	}
}

func TestVirtualTaskUpdateRequiresHintAndPatch(t *testing.T) {
	service := virtualCapabilityService{}
	service.taskResponse("task_add", []byte(`{"input":{"title":"고객지원 결산"}}`))
	response := service.taskResponse("task_update", []byte(`{"input":{"taskHint":"task-1"}}`))
	if !strings.Contains(response, `"errorCode":"invalid_input"`) {
		t.Fatalf("expected hint-only task update to fail, got %s", response)
	}
}

func TestVirtualCapabilityCatalogUsesRuntimeRegistryContract(t *testing.T) {
	var catalog struct {
		DeviceCapabilities []struct {
			Name           string                                     `json:"name"`
			InputSchema    json.RawMessage                            `json:"inputSchema"`
			ResultContract *agentruntime.CapabilityToolResultContract `json:"resultContract"`
		} `json:"deviceCapabilities"`
	}
	document := virtualCapabilityCatalogResponse(map[string]bool{
		"event_list":   true,
		"event_update": true,
		"event_delete": true,
	})
	if errorValue := json.Unmarshal([]byte(document), &catalog); errorValue != nil {
		t.Fatalf("expected valid capability catalog, got %v: %s", errorValue, document)
	}
	if len(catalog.DeviceCapabilities) != 3 {
		t.Fatalf("expected runtime device capability descriptor, got %+v", catalog.DeviceCapabilities)
	}
	var schema struct {
		Properties           map[string]json.RawMessage `json:"properties"`
		Required             []string                   `json:"required"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
		MinimumProperties    int                        `json:"minProperties"`
	}
	if errorValue := json.Unmarshal(catalog.DeviceCapabilities[2].InputSchema, &schema); errorValue != nil {
		t.Fatalf("expected calendar update schema, got %v", errorValue)
	}
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties ||
		schema.MinimumProperties != 2 ||
		len(schema.Required) != 1 ||
		schema.Required[0] != "eventHint" ||
		schema.Properties["query"] != nil {
		t.Fatalf("expected exact calendar update schema, got %s", catalog.DeviceCapabilities[2].InputSchema)
	}
	updateContract := catalog.DeviceCapabilities[2].ResultContract
	if updateContract == nil || len(updateContract.Effects) != 1 ||
		updateContract.Effects[0].ObjectType != "calendar" ||
		updateContract.Effects[0].ResultField != "eventID" {
		t.Fatalf("expected exact calendar update result contract, got %+v", updateContract)
	}
}

func TestSkillLifecycleAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), SkillLifecycleAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected skill lifecycle acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turn results, got %d", len(result.TurnResults))
	}
	firstTurnResult := result.TurnResults[0]
	secondTurnResult := result.TurnResults[1]
	if countEvents(firstTurnResult.Events, "tool.skill_add.requested") != 1 {
		t.Fatalf("expected one skill_add request; events: %s", summarizeEvents(firstTurnResult.Events))
	}
	if countEvents(secondTurnResult.Events, "tool.skill_remove.requested") != 1 {
		t.Fatalf("expected one skill_remove request; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	skillDirectoryPath := filepath.Join(result.ArtifactDirectoryPath, "workspace", ".agents", "skills", "memo-helper")
	if _, errorValue := os.Stat(skillDirectoryPath); !os.IsNotExist(errorValue) {
		t.Fatalf("expected memo-helper skill directory to be removed, got %v", errorValue)
	}
}

func TestTaskHistoryQuestionAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), TaskHistoryQuestionAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected task history question acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turn results, got %d", len(result.TurnResults))
	}
	secondTurnResult := result.TurnResults[1]
	if countEvents(secondTurnResult.Events, "tool.conversation_history.requested") != 1 {
		t.Fatalf("expected one conversation_history request; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !eventsContain(secondTurnResult.Events, "tool.conversation_history.result", "계약서 확인 요약 작업") {
		t.Fatalf("expected conversation_history result to include prior task prompt; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !strings.Contains(secondTurnResult.FinishMessage, "계약서 확인 요약") {
		t.Fatalf("expected final reply to mention prior task, got %q", secondTurnResult.FinishMessage)
	}
}

func TestMemoryExplicitToolAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), MemoryExplicitToolAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected memory explicit tool acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turn results, got %d", len(result.TurnResults))
	}
	firstTurnResult := result.TurnResults[0]
	secondTurnResult := result.TurnResults[1]
	if firstTurnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected first turn success, got %s", firstTurnResult.TaskStatus)
	}
	if secondTurnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected second turn success, got %s", secondTurnResult.TaskStatus)
	}
	if countEvents(firstTurnResult.Events, "tool.memory_remember.requested")+countEvents(secondTurnResult.Events, "tool.memory_remember.requested") != 1 {
		t.Fatalf("expected exactly one memory_remember request; first events: %s second events: %s", summarizeEvents(firstTurnResult.Events), summarizeEvents(secondTurnResult.Events))
	}
	if !eventsContain(firstTurnResult.Events, "tool.memory_remember.requested", "Korean") {
		t.Fatalf("expected memory_remember input to include Korean; events: %s", summarizeEvents(firstTurnResult.Events))
	}
	if countEvents(firstTurnResult.Events, "tool.memory_search.requested")+countEvents(secondTurnResult.Events, "tool.memory_search.requested") != 1 {
		t.Fatalf("expected exactly one memory_search request; first events: %s second events: %s", summarizeEvents(firstTurnResult.Events), summarizeEvents(secondTurnResult.Events))
	}
	if !strings.Contains(secondTurnResult.FinishMessage, "Korean") {
		t.Fatalf("expected final reply to mention Korean, got %q", secondTurnResult.FinishMessage)
	}
}

func TestFailureExplanationAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), FailureExplanationAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected failure explanation acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turn results, got %d", len(result.TurnResults))
	}
	firstTurnResult := result.TurnResults[0]
	secondTurnResult := result.TurnResults[1]
	if firstTurnResult.TaskStatus != task.TaskStatusFailed {
		t.Fatalf("expected first turn failure, got %s", firstTurnResult.TaskStatus)
	}
	if !strings.Contains(firstTurnResult.FailureReason, "permission denied") {
		t.Fatalf("expected failure reason to mention permission denied, got %q", firstTurnResult.FailureReason)
	}
	if secondTurnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected second turn success, got %s", secondTurnResult.TaskStatus)
	}
	if countEvents(secondTurnResult.Events, "tool.conversation_history.requested") != 1 {
		t.Fatalf("expected one conversation_history request in second turn; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !eventsContain(secondTurnResult.Events, "tool.conversation_history.result", "permission denied") {
		t.Fatalf("expected conversation_history result to include failure reason; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !strings.Contains(secondTurnResult.FinishMessage, "permission denied") {
		t.Fatalf("expected final reply to mention permission denied, got %q", secondTurnResult.FinishMessage)
	}
}

func TestAskChoiceReplyAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AskChoiceReplyAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected ask choice reply acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turns, got %+v", result)
	}
}

func TestDirectMessageSendConfirmAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), DirectMessageSendConfirmAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected direct message send confirm acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turns, got %+v", result)
	}
	firstTurnResult := result.TurnResults[0]
	secondTurnResult := result.TurnResults[1]
	if !eventsContain(firstTurnResult.Events, "confirmation.requested", "external_send") {
		t.Fatalf("expected confirmation request before send; events: %s", summarizeEvents(firstTurnResult.Events))
	}
	if eventsContain(firstTurnResult.Events, "tool.message_send.result", "virtual-platform-message-001") {
		t.Fatalf("expected nothing delivered before the requester confirmed; events: %s", summarizeEvents(firstTurnResult.Events))
	}
	if countEventsWithFragment(secondTurnResult.Events, "tool.message_send.result", `"messageIDs":["virtual-platform-message-001"]`) != 1 {
		t.Fatalf("expected exactly one message to reach the platform; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !eventsContain(secondTurnResult.Events, "tool.message_send.result", "virtual-platform-message-001") {
		t.Fatalf("expected send result message id observation; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !eventsContain(secondTurnResult.Events, "tool.message_send.result", `"messageIDs":["virtual-platform-message-001"]`) {
		t.Fatalf("expected canonical messageIDs result; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !strings.Contains(secondTurnResult.FinishMessage, "보냈습니다") {
		t.Fatalf("expected successful delivery reply, got %q", secondTurnResult.FinishMessage)
	}
}

func TestChannelPostAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), ChannelPostAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected channel post acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected confirmation and execution turns, got %+v", result)
	}
	if eventsContain(result.TurnResults[0].Events, "tool.message_send.result", "virtual-platform-message-001") {
		t.Fatalf("expected nothing posted before the requester confirmed; events: %s", summarizeEvents(result.TurnResults[0].Events))
	}
	turnResult := result.TurnResults[1]
	if countEventsWithFragment(turnResult.Events, "tool.message_send.result", `"messageIDs":["virtual-platform-message-001"]`) != 1 {
		t.Fatalf("expected exactly one message to reach the platform, got events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "tool.message_send.requested", `"targetType":"channel"`) {
		t.Fatalf("expected channel delivery target; events: %s", summarizeEvents(turnResult.Events))
	}
	if eventsContain(turnResult.Events, "tool.message_send.requested", `"targetType":"directMessage"`) {
		t.Fatalf("expected no direct message target; events: %s", summarizeEvents(turnResult.Events))
	}
}

func TestPlatformMessageEditAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), PlatformMessageEditAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected platform message edit acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected the edit to finish in one turn, got %+v", result)
	}
	turnResult := result.TurnResults[0]
	if eventsContain(turnResult.Events, "approval.pending_call", `"message_update"`) {
		t.Fatalf("expected no approval hold on an edit of the assistant's own message; events: %s", summarizeEvents(turnResult.Events))
	}
	if countEventsWithFragment(turnResult.Events, "tool.message_update.result", `"deliveryStatus":"updated"`) != 1 {
		t.Fatalf("expected exactly one edit to reach the platform; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "tool.message_update.requested", `"messageID":"virtual-platform-message-001"`) {
		t.Fatalf("expected message ID in update input; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "tool.message_update.requested", `"oldText":"오후 5시"`) {
		t.Fatalf("expected the quoted span in update input; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "tool.message_update.requested", `"newText":"오후 6시"`) {
		t.Fatalf("expected the replacement span in update input; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "tool.message_update.result", `"messageUpdated":true`) {
		t.Fatalf("expected canonical update result; events: %s", summarizeEvents(turnResult.Events))
	}
}

func TestAttachmentMaterialRead(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AttachmentMaterialReadScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected attachment material read scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "tool.image_read.requested", `"path":"/workspace/circles/staff/inbox/virtual/virtual-conversation-1/virtual-message-001/mascot.png"`) {
		t.Fatalf("expected image_read to use the exact workspace path; events: %s", summarizeEvents(turnResult.Events))
	}
	if eventsContain(turnResult.Events, "tool.shell.requested", "shell") {
		t.Fatalf("expected attachment read not to search the workspace; events: %s", summarizeEvents(turnResult.Events))
	}
	if turnResult.UserModelImagePartCount == 0 {
		t.Fatalf("expected image_read result to reach the model as a user image part; context: %s", turnResult.ModelContext)
	}
	if len(turnResult.Attachments) != 0 {
		t.Fatalf("expected image_read result not to be reattached, got %+v", turnResult.Attachments)
	}
}

func TestAttachmentHTMLPreviewRecovery(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AttachmentHTMLPreviewRecoveryScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected attachment html preview recovery scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if eventsContain(turnResult.Events, "tool.shell.requested", "shell") {
		t.Fatalf("expected html attachment preview not to search the workspace; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "tool.file_preview.result", "Virtual HTML Title") {
		t.Fatalf("expected html preview content in tool result; events: %s", summarizeEvents(turnResult.Events))
	}
}

func TestAttachmentHTMLPreviousPreviewRecovery(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AttachmentHTMLPreviousPreviewRecoveryScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected previous attachment html preview recovery scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if eventsContain(turnResult.Events, "tool.shell.requested", "shell") {
		t.Fatalf("expected previous html attachment preview not to search the workspace; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "tool.file_preview.result", "Virtual HTML Title") {
		t.Fatalf("expected previous html preview content in tool result; events: %s", summarizeEvents(turnResult.Events))
	}
}

func TestAttachmentCurrentImageInput(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AttachmentCurrentImageInputScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected current image input scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if turnResult.ModelImagePartCount == 0 {
		t.Fatalf("expected current image attachment to reach model input; context: %s", turnResult.ModelContext)
	}
	if turnResult.UserModelImagePartCount == 0 {
		t.Fatalf("expected current image attachment to reach the model as a user image part; context: %s", turnResult.ModelContext)
	}
	if eventsContain(turnResult.Events, "tool.image_read.requested", "image_read") {
		t.Fatalf("expected current image input not to require image_read; events: %s", summarizeEvents(turnResult.Events))
	}
	if eventsContain(turnResult.Events, "tool.shell.requested", "shell") {
		t.Fatalf("expected current image input not to search the workspace; events: %s", summarizeEvents(turnResult.Events))
	}
	if len(turnResult.Attachments) != 0 {
		t.Fatalf("expected current image input not to be reattached, got %+v", turnResult.Attachments)
	}
}

func firstNonEmptyTestString(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}

func truthyEnvironmentValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func toolEventCount(events []task.TaskEvent) int {
	count := 0
	for _, event := range events {
		if strings.HasPrefix(event.Name, "tool.") {
			count++
		}
	}
	return count
}

func failureEventCount(events []task.TaskEvent) int {
	count := 0
	for _, event := range events {
		normalizedName := strings.ToLower(event.Name)
		if strings.Contains(normalizedName, "fail") || strings.Contains(normalizedName, "error") {
			count++
		}
	}
	return count
}

func activeScheduleCount(taskSchedules []task.TaskSchedule) int {
	count := 0
	for _, taskSchedule := range taskSchedules {
		if taskSchedule.NextRunAt != nil {
			count++
		}
	}
	return count
}

func eventBodies(events []task.TaskEvent, name string) []string {
	bodies := []string{}
	for _, event := range events {
		if event.Name == name {
			bodies = append(bodies, event.Body)
		}
	}
	return bodies
}
