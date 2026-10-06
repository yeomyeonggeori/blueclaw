package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type recordCatalogStandIn struct {
	discovered      []capability.ToolDescriptor
	discoveryFailed error

	askedToDiscoverFor []string
	calledTool         string
	calledFor          string
	calledWith         json.RawMessage
	answered           mcp.ToolResult
}

func (standIn *recordCatalogStandIn) DiscoverTools(_ context.Context, requesterEmail string) ([]capability.ToolDescriptor, error) {
	standIn.askedToDiscoverFor = append(standIn.askedToDiscoverFor, requesterEmail)
	if standIn.discoveryFailed != nil {
		return nil, standIn.discoveryFailed
	}
	return standIn.discovered, nil
}

func (standIn *recordCatalogStandIn) CallTool(
	_ context.Context,
	requesterEmail string,
	toolName string,
	input json.RawMessage,
) (mcp.ToolResult, error) {
	standIn.calledFor = requesterEmail
	standIn.calledTool = toolName
	standIn.calledWith = input
	return standIn.answered, nil
}

func aDescriptor(name string, answeredBy string) capability.ToolDescriptor {
	return capability.ToolDescriptor{
		Name:              name,
		CanonicalName:     name,
		Namespace:         strings.Split(name, "_")[0],
		AnsweredBy:        answeredBy,
		ModelName:         name,
		ModelVisibility:   toolcontract.ToolVisibilityModel,
		Description:       "Does " + name + ".",
		PrivacyClass:      "workspace_task",
		InputSchema:       json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`),
		InputIntentSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"additionalProperties":false}`),
		OutputSchema:      json.RawMessage(`{"type":"object","properties":{"result":{}},"additionalProperties":false}`),
		ResultContract: &capability.ToolResultContract{
			Schema: json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"}},"required":["taskID"],"additionalProperties":false}`),
			Effects: []capability.ResourceEffectContract{{
				ObjectType:     "task",
				Effect:         "created",
				ResultField:    "taskID",
				EffectIdentity: "id",
			}},
		},
		PolicyResource:  "tool:" + name,
		SideEffectClass: toolcontract.ToolSideEffectWorkspaceWrite,
		Availability:    capability.Availability{State: "ok"},
		Idempotency:     capability.Idempotency{Scope: "operation"},
	}
}

func aBuilderWith(stamped ...capability.ToolDescriptor) (*ToolCatalogBuilder, *[]RecordCatalogDivergence) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{}, stamped)
	reported := []RecordCatalogDivergence{}
	toolCatalogBuilder.UseRecordCatalogDivergenceReporter(func(divergence RecordCatalogDivergence) {
		reported = append(reported, divergence)
	})
	return toolCatalogBuilder, &reported
}

func aRequestFrom(standIn *recordCatalogStandIn, requesterEmail string) ToolCatalogRequest {
	return ToolCatalogRequest{RequesterEmail: requesterEmail, RecordCatalog: standIn}
}

func TestDiscoveredRecordToolReplacesTheStampedOneOfTheSameName(t *testing.T) {
	standIn := &recordCatalogStandIn{discovered: []capability.ToolDescriptor{aDescriptor("task_add", capability.AnsweredByRecord)}}
	toolCatalogBuilder, _ := aBuilderWith(
		aDescriptor("task_add", capability.AnsweredByRecord),
		aDescriptor("message_send", capability.AnsweredByCompany),
	)

	toolSet := toolCatalogBuilder.BuildToolSet(aRequestFrom(standIn, "sample@example.test"))

	discovered, isFound := toolSet.ToolDefinition("task_add")
	if !isFound {
		t.Fatal("task_add was not registered")
	}
	if discovered.ProviderID != recordCatalogProviderID {
		t.Fatalf("task_add came from %q", discovered.ProviderID)
	}
	stamped, isFound := toolSet.ToolDefinition("message_send")
	if !isFound {
		t.Fatal("message_send was not registered")
	}
	if stamped.ProviderID != "capabilityd" {
		t.Fatalf("message_send came from %q", stamped.ProviderID)
	}
}

func TestACompanyToolIsNeverTakenFromDiscovery(t *testing.T) {
	standIn := &recordCatalogStandIn{discovered: []capability.ToolDescriptor{
		aDescriptor("task_add", capability.AnsweredByRecord),
		aDescriptor("message_send", capability.AnsweredByCompany),
	}}
	toolCatalogBuilder, _ := aBuilderWith(
		aDescriptor("task_add", capability.AnsweredByRecord),
		aDescriptor("message_send", capability.AnsweredByCompany),
	)

	toolSet := toolCatalogBuilder.BuildToolSet(aRequestFrom(standIn, "sample@example.test"))

	messageSend, isFound := toolSet.ToolDefinition("message_send")
	if !isFound {
		t.Fatal("message_send was not registered")
	}
	if messageSend.ProviderID != "capabilityd" {
		t.Fatalf("message_send was taken over MCP, from %q", messageSend.ProviderID)
	}
}

func TestDiscoveryThatFailsLeavesTheStampedDescriptorsStanding(t *testing.T) {
	standIn := &recordCatalogStandIn{discoveryFailed: errors.New("the capability service is not listening")}
	toolCatalogBuilder, reported := aBuilderWith(aDescriptor("task_add", capability.AnsweredByRecord))

	toolSet := toolCatalogBuilder.BuildToolSet(aRequestFrom(standIn, "sample@example.test"))

	stamped, isFound := toolSet.ToolDefinition("task_add")
	if !isFound || stamped.ProviderID != "capabilityd" {
		t.Fatalf("task_add came from %q, found %v", stamped.ProviderID, isFound)
	}
	if len(*reported) != 1 || (*reported)[0].DiscoveryFailed == "" {
		t.Fatalf("the failure was reported as %+v", *reported)
	}
}

func TestDivergenceNamesEveryToolOnlyOneSideCarries(t *testing.T) {
	standIn := &recordCatalogStandIn{discovered: []capability.ToolDescriptor{
		aDescriptor("task_add", capability.AnsweredByRecord),
		aDescriptor("crm_contact_add", capability.AnsweredByRecord),
	}}
	toolCatalogBuilder, reported := aBuilderWith(
		aDescriptor("task_add", capability.AnsweredByRecord),
		aDescriptor("leave_request", capability.AnsweredByRecord),
	)

	toolCatalogBuilder.BuildToolSet(aRequestFrom(standIn, "sample@example.test"))

	if len(*reported) != 1 {
		t.Fatalf("divergence was reported %d times", len(*reported))
	}
	divergence := (*reported)[0]
	if !reflect.DeepEqual(divergence.DiscoveredOnly, []string{"crm_contact_add"}) {
		t.Fatalf("the discovered-only tools were %v", divergence.DiscoveredOnly)
	}
	if !reflect.DeepEqual(divergence.StampedOnly, []string{"leave_request"}) {
		t.Fatalf("the stamped-only tools were %v", divergence.StampedOnly)
	}
}

func TestACatalogTheTwoSidesAgreeOnIsNotReported(t *testing.T) {
	standIn := &recordCatalogStandIn{discovered: []capability.ToolDescriptor{aDescriptor("task_add", capability.AnsweredByRecord)}}
	toolCatalogBuilder, reported := aBuilderWith(aDescriptor("task_add", capability.AnsweredByRecord))

	toolCatalogBuilder.BuildToolSet(aRequestFrom(standIn, "sample@example.test"))

	if len(*reported) != 0 {
		t.Fatalf("a catalog both sides agree on was reported as %+v", *reported)
	}
}

func TestADiscoveredToolIsCalledOverMCPAsTheRequester(t *testing.T) {
	standIn := &recordCatalogStandIn{
		discovered: []capability.ToolDescriptor{aDescriptor("task_add", capability.AnsweredByRecord)},
		answered: mcp.ToolResult{
			StructuredContent: json.RawMessage(`{"tool":"task_add","result":{"taskID":"task-1"}}`),
		},
	}
	toolCatalogBuilder, _ := aBuilderWith(aDescriptor("task_add", capability.AnsweredByRecord))
	toolSet := toolCatalogBuilder.BuildToolSet(aRequestFrom(standIn, "sample@example.test"))

	result, errorValue := toolSet.InvokeInternal(context.Background(), toolcontract.ToolInvocation{
		ToolName: "task_add",
		Input:    json.RawMessage(`{"title":"쓰기"}`),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if standIn.calledTool != "task_add" || standIn.calledFor != "sample@example.test" {
		t.Fatalf("the record catalog was asked for %q by %q", standIn.calledTool, standIn.calledFor)
	}
	if string(standIn.calledWith) != `{"title":"쓰기"}` {
		t.Fatalf("the input arrived as %s", standIn.calledWith)
	}
	if result.Failed() {
		t.Fatalf("the call failed: %+v", result.Failure)
	}
	if len(result.Effects) != 1 || result.Effects[0].ID != "task-1" {
		t.Fatalf("the effects were projected as %+v", result.Effects)
	}
	if string(result.Output.Data) != `{"taskID":"task-1"}` {
		t.Fatalf("the result the contract sees was %s", result.Output.Data)
	}
}

func TestOneDiscoveryServesTheTurnsThatFollowIt(t *testing.T) {
	standIn := &recordCatalogStandIn{discovered: []capability.ToolDescriptor{aDescriptor("task_add", capability.AnsweredByRecord)}}
	toolCatalogBuilder, _ := aBuilderWith(aDescriptor("task_add", capability.AnsweredByRecord))

	toolCatalogBuilder.BuildToolSet(aRequestFrom(standIn, "sample@example.test"))
	toolCatalogBuilder.BuildToolSet(aRequestFrom(standIn, "sample@example.test"))
	toolCatalogBuilder.BuildToolSet(aRequestFrom(standIn, "example@example.test"))

	if !reflect.DeepEqual(standIn.askedToDiscoverFor, []string{"sample@example.test", "example@example.test"}) {
		t.Fatalf("discovery was asked for %v", standIn.askedToDiscoverFor)
	}
}

func TestATurnNobodyAskedForDiscoversNothing(t *testing.T) {
	standIn := &recordCatalogStandIn{discovered: []capability.ToolDescriptor{aDescriptor("task_add", capability.AnsweredByRecord)}}
	toolCatalogBuilder, _ := aBuilderWith(aDescriptor("task_add", capability.AnsweredByRecord))

	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{})

	if len(standIn.askedToDiscoverFor) != 0 {
		t.Fatalf("discovery was asked for %v", standIn.askedToDiscoverFor)
	}
	stamped, isFound := toolSet.ToolDefinition("task_add")
	if !isFound || stamped.ProviderID != "capabilityd" {
		t.Fatalf("task_add came from %q, found %v", stamped.ProviderID, isFound)
	}
}

func aMessageSendDescriptor() capability.ToolDescriptor {
	descriptor := aDescriptor("message_send", capability.AnsweredByRecord)
	descriptor.InputSchema = json.RawMessage(`{"type":"object","properties":{"personHint":{"type":"string"},"message":{"type":"string"}},"required":["personHint","message"],"additionalProperties":false}`)
	descriptor.InputIntentSchema = json.RawMessage(`{"type":"object","properties":{"personHint":{"type":"string"},"message":{"type":"string"}},"additionalProperties":false}`)
	descriptor.RequiresApproval = true
	descriptor.SideEffectClass = toolcontract.ToolSideEffectExternalSend
	return descriptor
}

func aHoldOnAMessageToPerson(t *testing.T, taskRunService *task.TaskRunService) (string, string) {
	t.Helper()
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "send the message")
	hold := holdrecord.Open(taskRunService, taskRun.TaskRunID, agentcontract.HeldCall{
		ToolName:          "message_send",
		ToolInput:         json.RawMessage(`{"personHint":"박예시","message":"내일 만나요"}`),
		ApprovedToolInput: json.RawMessage(`{"personHint":"person-2","message":"내일 만나요"}`),
	}, nil)
	return taskRun.TaskRunID, hold.ID
}

func TestARecordToolRunsTheInputTheHoldApprovedNotTheInputTheModelSendsLater(t *testing.T) {
	standIn := &recordCatalogStandIn{
		discovered: []capability.ToolDescriptor{aMessageSendDescriptor()},
		answered:   mcp.ToolResult{StructuredContent: json.RawMessage(`{"tool":"message_send","result":{}}`)},
	}
	toolCatalogBuilder, _ := aBuilderWith(aMessageSendDescriptor())
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	taskRunID, holdID := aHoldOnAMessageToPerson(t, taskRunService)
	toolSet := toolCatalogBuilder.BuildToolSet(aRequestFrom(standIn, "sample@example.test"))
	approvedContext := toolcontract.WithHoldID(toolcontract.WithTaskRunID(context.Background(), taskRunID), holdID)

	_, errorValue := toolSet.InvokeInternal(approvedContext, toolcontract.ToolInvocation{
		ToolName: "message_send",
		Input:    json.RawMessage(`{"personHint":"최견본","message":"지금 만나요"}`),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if agentcontract.CanonicalToolInput(standIn.calledWith) != `{"message":"내일 만나요","personHint":"person-2"}` {
		t.Fatalf("the record was called with %s, expected the input the hold approved", standIn.calledWith)
	}
}

func TestARecordToolWithNoHoldRunsTheInputItWasGiven(t *testing.T) {
	standIn := &recordCatalogStandIn{
		discovered: []capability.ToolDescriptor{aMessageSendDescriptor()},
		answered:   mcp.ToolResult{StructuredContent: json.RawMessage(`{"tool":"message_send","result":{}}`)},
	}
	toolCatalogBuilder, _ := aBuilderWith(aMessageSendDescriptor())
	toolCatalogBuilder.UseTaskRunService(task.NewTaskRunService(task.NewTaskEventService()))
	toolSet := toolCatalogBuilder.BuildToolSet(aRequestFrom(standIn, "sample@example.test"))

	_, errorValue := toolSet.InvokeInternal(context.Background(), toolcontract.ToolInvocation{
		ToolName: "message_send",
		Input:    json.RawMessage(`{"personHint":"최견본","message":"지금 만나요"}`),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if agentcontract.CanonicalToolInput(standIn.calledWith) != `{"message":"지금 만나요","personHint":"최견본"}` {
		t.Fatalf("the record was called with %s", standIn.calledWith)
	}
}

func TestAPlatformMessageCapabilityRunsTheInputTheHoldApproved(t *testing.T) {
	httpClient := &recordingHTTPClient{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:        "message_send",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"personHint":{"type":"string"},"message":{"type":"string"}},"required":["personHint","message"],"additionalProperties":false}`),
	}})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"message_send"})
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	taskRunID, holdID := aHoldOnAMessageToPerson(t, taskRunService)
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	approvedContext := toolcontract.WithHoldID(toolcontract.WithTaskRunID(context.Background(), taskRunID), holdID)

	_, errorValue := toolSet.Invoke(approvedContext, toolcontract.ToolInvocation{
		ToolName: "message_send",
		Input:    json.RawMessage(`{"personHint":"최견본","message":"지금 만나요"}`),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	var sent struct {
		Input json.RawMessage `json:"input"`
	}
	json.Unmarshal([]byte(httpClient.requestBody), &sent)
	if agentcontract.CanonicalToolInput(sent.Input) != `{"message":"내일 만나요","personHint":"person-2"}` {
		t.Fatalf("the capability was asked to run %s, expected the input the hold approved", sent.Input)
	}
}
