package agentruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/loop"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestToolCatalogHidesPolicyDeniedCapabilityTools(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{{
		Name:           "site_serve",
		Description:    "Create a site.",
		PolicyResource: "tool:site_serve",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"],"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"member"},
			ResourceAccessRules: []policy.ResourceAccessPolicy{{
				Resource: "tool:site_serve",
				Actions:  []string{"execute"},
				Circles:  []string{"admin"},
			}},
		},
	})

	if strings.Contains(toolRegistry.Descriptions(), "site_serve") {
		t.Fatalf("expected denied site tool to be omitted from catalog, got %s", toolRegistry.Descriptions())
	}
}

func TestToolCatalogKeepsCapabilityInputSchemaAuthoritative(t *testing.T) {
	taskAddSchema := json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"endDate":{"type":"string"}},"required":["title"],"additionalProperties":false}`)
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{{
		Name:        "task_add",
		InputSchema: taskAddSchema,
	}})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"task_add"})

	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	actionSchema := loop.ActionSchemaForToolSet(toolSet, false, nil, false)

	if !strings.Contains(actionSchema, `"title"`) || !strings.Contains(actionSchema, `"endDate"`) {
		t.Fatalf("expected registered task_add schema, got %s", actionSchema)
	}
	if strings.Contains(actionSchema, `"prompt"`) {
		t.Fatalf("expected no inferred legacy task_add fields, got %s", actionSchema)
	}
}

func TestCapabilityToolPreservesValidatedTaskResultEffects(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{
		"provider":"internkim",
		"selectedBackend":"device",
		"toolName":"task_add",
		"outcome":"succeeded",
		"status":"ok",
		"result":{"taskID":"task-1"},
		"effects":[{"objectType":"task","effect":"created","id":"task-1"}]
	}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name: "task_add",
		ResultContract: &CapabilityToolResultContract{
			Schema: json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"}},"required":["taskID"],"additionalProperties":false}`),
			Effects: []CapabilityResourceEffectContract{{
				ObjectType:     "task",
				Effect:         "created",
				ResultField:    "taskID",
				EffectIdentity: "id",
			}},
		},
	}})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"task_add"})
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "task_add",
		Input:    json.RawMessage(`{}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() || len(result.Effects) != 1 || result.Effects[0].ID != "task-1" {
		t.Fatalf("expected validated task effect, got %+v", result)
	}
}

func TestCapabilityToolRejectsMismatchedTaskResultIdentity(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{
		"provider":"internkim",
		"selectedBackend":"device",
		"toolName":"task_update",
		"outcome":"succeeded",
		"status":"ok",
		"result":{"taskID":"task-1"},
		"effects":[{"objectType":"task","effect":"created","id":"task-1"}]
	}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "task_add",
		ResultContract: &CapabilityToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"}},"required":["taskID"],"additionalProperties":false}`)},
	}})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"task_add"})
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{ToolName: "task_add", Input: json.RawMessage(`{}`)})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureStage() != "capability_result_identity" {
		t.Fatalf("expected identity failure, got %+v", result)
	}
}

func TestCapabilityToolRejectsMismatchedIdentityWithoutResultContract(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{
		"provider":"internkim",
		"selectedBackend":"device",
		"toolName":"task_update",
		"outcome":"succeeded",
		"status":"ok",
		"result":{}
	}`}
	descriptor := completeTestCapabilityToolDescriptor(CapabilityToolDescriptor{Name: "task_add"})
	descriptor.ResultContract = nil
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.capabilityClient = capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}

	result, errorValue := toolCatalogBuilder.invokeCapabilityOperation(
		context.Background(),
		"task_add",
		descriptor,
		ToolCatalogRequest{},
		json.RawMessage(`{}`),
	)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureStage() != "capability_result_identity" {
		t.Fatalf("expected identity failure without a result contract, got %+v", result)
	}
}

func TestContractedCapabilityPreservesApprovalDenial(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{
		"provider":"internkim",
		"selectedBackend":"device",
		"toolName":"task_delete",
		"outcome":"denied",
		"status":"denied",
		"isError":true,
		"errorCode":"approval_required",
		"failureStage":"authorization",
		"result":{"errorCode":"approval_required"}
	}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name: "task_delete",
		ResultContract: &CapabilityToolResultContract{
			Schema: json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"},"deleted":{"const":true}},"required":["taskID","deleted"],"additionalProperties":false}`),
			Effects: []CapabilityResourceEffectContract{{
				ObjectType:     "task",
				Effect:         "deleted",
				ResultField:    "taskID",
				EffectIdentity: "id",
			}},
		},
	}})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"task_delete"})
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{ToolName: "task_delete", Input: json.RawMessage(`{}`)})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureStage() != "authorization" || result.Failure == nil || !result.Failure.RequiresApproval {
		t.Fatalf("expected approval denial, got %+v", result)
	}
}

func TestPlatformDMSendAvailabilityDependsOnTrustedContext(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{{
		Name:             "message_send",
		Description:      "Send a direct message",
		RequiresApproval: true,
	}})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"message_send"})

	immediateToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	if strings.Contains(immediateToolSet.Descriptions(), "ask approval before invoking") {
		t.Fatalf("expected immediate DM to be available for runtime gating, got %s", immediateToolSet.Descriptions())
	}
	toolDefinition, isFound := immediateToolSet.ToolDefinition("message_send")
	if !isFound || !toolDefinition.RequiresApproval {
		t.Fatalf("expected immediate DM definition to require approval, got found=%v definition=%+v", isFound, toolDefinition)
	}
	scheduledToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default", IsScheduledRun: true})
	if strings.Contains(scheduledToolSet.Descriptions(), "ask approval before invoking") {
		t.Fatalf("expected scheduled DM to be available, got %s", scheduledToolSet.Descriptions())
	}
	approvedToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default", IsApprovalContinuation: true})
	if strings.Contains(approvedToolSet.Descriptions(), "ask approval before invoking") {
		t.Fatalf("expected approved continuation DM to be available, got %s", approvedToolSet.Descriptions())
	}
}

func TestCapabilityToolRequestIncludesTrustedExecutionContext(t *testing.T) {
	descriptor := completeTestCapabilityToolDescriptor(CapabilityToolDescriptor{
		Name:          "message_send",
		CanonicalName: "message_send",
		PrivacyClass:  "platform_message",
		Idempotency:   CapabilityIdempotency{Supported: true, Scope: "operation"},
	})
	requestDocument := capabilityToolRequest(context.Background(), descriptor, ToolCatalogRequest{
		TaskSource:              TaskLaunchSourceScheduled,
		IsScheduledRun:          true,
		IsApprovalContinuation:  true,
		RequesterPersonID:       "person-1",
		RequesterPlatformUserID: "mattermost-user-1",
		ConversationID:          "conversation-1",
		ConversationChannelID:   "channel-1",
		ReplyTargetID:           "reply-target-1",
		Platform:                "mattermost",
	}, preparedCapabilityToolPayload{Input: json.RawMessage(`{"targetType":"directMessage","personHint":"Dana","message":"test"}`)})
	contextDocument, isFound := requestDocument["context"].(map[string]any)
	if !isFound {
		t.Fatalf("expected context document, got %+v", requestDocument)
	}
	if contextDocument["taskSource"] != string(TaskLaunchSourceScheduled) || contextDocument["isScheduledRun"] != true || contextDocument["isApprovalContinuation"] != true {
		t.Fatalf("expected trusted execution context, got %+v", contextDocument)
	}
	if contextDocument["replyTargetID"] != "reply-target-1" {
		t.Fatalf("expected reply target in context, got %+v", contextDocument)
	}
}

func TestCapabilityToolRequestSeparatesModelInputFromTransport(t *testing.T) {
	input := json.RawMessage(`{"siteID":"site-1"}`)
	transport := map[string]any{"siteSourceBundle": map[string]any{"workspacePath": "/workspace/site"}}
	requestDocument := capabilityToolRequest(
		context.Background(),
		completeTestCapabilityToolDescriptor(CapabilityToolDescriptor{Name: "site_serve", CanonicalName: "site_serve"}),
		ToolCatalogRequest{},
		preparedCapabilityToolPayload{Input: input, Transport: transport},
	)

	if string(requestDocument["input"].(json.RawMessage)) != string(input) {
		t.Fatalf("expected unchanged model input, got %+v", requestDocument["input"])
	}
	if requestDocument["transport"].(map[string]any)["siteSourceBundle"] == nil {
		t.Fatalf("expected trusted transport payload, got %+v", requestDocument)
	}
}

func TestImageReadUsesExactPathInput(t *testing.T) {
	workspacePath := t.TempDir()
	imagePath := filepath.Join(workspacePath, "circles", "member", "inbox", "mattermost", "thread-1", "post-1", "mascot.png")
	writeTestFile(t, imagePath, "image")
	httpClient := &recordingHTTPClient{responseBody: `{"provider":"internkim","selectedBackend":"device","toolName":"image_read","outcome":"succeeded","status":"ok","result":{"status":"ok","path":"/workspace/circles/member/inbox/mattermost/thread-1/post-1/mascot.png","attachments":[{"devicePath":"/workspace/circles/member/inbox/mattermost/thread-1/post-1/mascot.png","filename":"mascot.png","contentType":"image/png","sizeBytes":5,"contentBase64":"aW1hZ2U="}]}}`}
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{canonicalReadDescriptor("image_read")})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"member"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "image_read",
		Input:    toolcontract.MarshalToolInput(map[string]string{"path": "/workspace/circles/member/inbox/mattermost/thread-1/post-1/mascot.png"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected image_read success, got %s", result.ContentText())
	}
	if !strings.Contains(httpClient.requestBody, `/workspace/circles/member/inbox/mattermost/thread-1/post-1/mascot.png`) {
		t.Fatalf("expected capability request to use exact path, got %s", httpClient.requestBody)
	}
}

func TestCanonicalReadRejectsMaterialIDInput(t *testing.T) {
	toolCatalogBuilder := newFileToolTestCatalogBuilder(t.TempDir())
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{
		canonicalReadDescriptor("document_read"),
		canonicalReadDescriptor("image_read"),
	})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"member"},
		},
	})

	for _, toolName := range []string{"document_read", "image_read"} {
		t.Run(toolName, func(t *testing.T) {
			result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
				ToolName: toolName,
				Input:    toolcontract.MarshalToolInput(map[string]string{"materialID": "mattermost:file-1"}),
			})
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if !result.Failed() || result.FailureStage() != "tool_input_schema" {
				t.Fatalf("expected %s materialID rejection, got %+v", toolName, result)
			}
		})
	}
}

func TestCanonicalReadDescriptorsExposePathOnlyInputAndResultContract(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{
		canonicalReadDescriptor("document_read"),
		canonicalReadDescriptor("image_read"),
	})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"document_read", "image_read"})
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	actionSchema := loop.ActionSchemaForToolSet(toolSet, false, nil, false)
	if strings.Contains(actionSchema, "materialID") || !strings.Contains(actionSchema, "path") {
		t.Fatalf("expected model action schema to expose exact path-only input, got %s", actionSchema)
	}

	for _, toolName := range []string{"document_read", "image_read"} {
		t.Run(toolName, func(t *testing.T) {
			descriptor, isFound := toolSet.ToolDefinition(toolName)
			if !isFound {
				t.Fatal("expected canonical read descriptor")
			}
			var inputSchema struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			}
			if errorValue := json.Unmarshal(descriptor.InputSchema, &inputSchema); errorValue != nil {
				t.Fatal(errorValue)
			}
			if _, isMaterialID := inputSchema.Properties["materialID"]; isMaterialID {
				t.Fatal("canonical read input must not expose materialID")
			}
			if len(inputSchema.Required) != 1 || inputSchema.Required[0] != "path" {
				t.Fatalf("expected path-only required input, got %+v", inputSchema.Required)
			}
			if descriptor.ResultContract == nil || len(descriptor.ResultContract.Effects) != 0 {
				t.Fatalf("expected canonical read result contract without effects, got %+v", descriptor.ResultContract)
			}
		})
	}
}

func TestCanonicalReadRejectsIdentityAndResultSchemaDrift(t *testing.T) {
	tests := []struct {
		name         string
		responseBody string
		failureStage string
	}{
		{
			name:         "missing provider",
			responseBody: `{"provider":"","selectedBackend":"device","toolName":"document_read","outcome":"succeeded","status":"ok","result":{"status":"ok","path":"/workspace/report.md","format":"markdown","content":"report","warnings":[],"truncated":false}}`,
			failureStage: "capability_result_identity",
		},
		{
			name:         "missing backend",
			responseBody: `{"provider":"internkim","selectedBackend":"","toolName":"document_read","outcome":"succeeded","status":"ok","result":{"status":"ok","path":"/workspace/report.md","format":"markdown","content":"report","warnings":[],"truncated":false}}`,
			failureStage: "capability_result_identity",
		},
		{
			name:         "wrong tool",
			responseBody: `{"provider":"internkim","selectedBackend":"device","toolName":"image_read","outcome":"succeeded","status":"ok","result":{"status":"ok","path":"/workspace/report.md","format":"markdown","content":"report","warnings":[],"truncated":false}}`,
			failureStage: "capability_result_identity",
		},
		{
			name:         "wrong outcome",
			responseBody: `{"provider":"internkim","selectedBackend":"device","toolName":"document_read","outcome":"failed","status":"ok","result":{"status":"ok","path":"/workspace/report.md","format":"markdown","content":"report","warnings":[],"truncated":false}}`,
			failureStage: "capability_result_identity",
		},
		{
			name:         "missing result field",
			responseBody: `{"provider":"internkim","selectedBackend":"device","toolName":"document_read","outcome":"succeeded","status":"ok","result":{"status":"ok","path":"/workspace/report.md","format":"markdown","content":"report","warnings":[]}}`,
			failureStage: "tool_result_contract",
		},
		{
			name:         "missing result",
			responseBody: `{"provider":"internkim","selectedBackend":"device","toolName":"document_read","outcome":"succeeded","status":"ok"}`,
			failureStage: "tool_result_contract",
		},
		{
			name:         "generic scalar result",
			responseBody: `{"provider":"internkim","selectedBackend":"device","toolName":"document_read","outcome":"succeeded","status":"ok","result":"report"}`,
			failureStage: "tool_result_contract",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			httpClient := &recordingHTTPClient{responseBody: testCase.responseBody}
			toolCatalogBuilder := capabilityReadTestCatalogBuilder(t)
			toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{canonicalReadDescriptor("document_read")})
			toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"document_read"})
			toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
				ProfileName:       "default",
				RequesterPersonID: "person-1",
				PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
			})

			result, errorValue := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{ToolName: "document_read", Input: json.RawMessage(`{"path":"/workspace/report.md"}`)})

			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if !result.Failed() || result.FailureStage() != testCase.failureStage {
				t.Fatalf("expected %s, got %+v", testCase.failureStage, result)
			}
		})
	}
}

func TestCanonicalReadRejectsEffects(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{"provider":"internkim","selectedBackend":"device","toolName":"document_read","outcome":"succeeded","status":"ok","result":{"status":"ok","path":"/workspace/report.md","format":"markdown","content":"report","warnings":[],"truncated":false},"effects":[{"objectType":"file","effect":"read","path":"/workspace/report.md"}]}`}
	toolCatalogBuilder := capabilityReadTestCatalogBuilder(t)
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{canonicalReadDescriptor("document_read")})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"document_read"})
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
	})

	result, errorValue := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{ToolName: "document_read", Input: json.RawMessage(`{"path":"/workspace/report.md"}`)})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureStage() != "tool_result_contract" {
		t.Fatalf("expected read effects rejection, got %+v", result)
	}
}

func TestCanonicalWebSearchAcceptsNormalizedResultContract(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{"provider":"example-gateway","selectedBackend":"remote","toolName":"web_search","outcome":"succeeded","status":"ok","result":{"provider":"example-gateway","remoteLLMInvolved":true,"compatibility":"example_gateway_server_tool_auto","query":"internkim","answer":"result","results":[{"title":"InternKim","url":"https://internkim.example","snippet":"An agent platform"}]}}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{canonicalWebSearchDescriptor()})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"web_search"})
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{ToolName: "web_search", Input: json.RawMessage(`{"query":"internkim"}`)})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected normalized web search result, got %+v", result)
	}
}

func TestCanonicalWebSearchRejectsReadEffects(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{"provider":"example-gateway","selectedBackend":"remote","toolName":"web_search","outcome":"succeeded","status":"ok","result":{"provider":"example-gateway","remoteLLMInvolved":true,"compatibility":"example_gateway_server_tool_auto","query":"internkim","answer":"result","results":[]},"effects":[{"objectType":"web","effect":"read","url":"https://internkim.example"}]}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{canonicalWebSearchDescriptor()})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"web_search"})
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{ToolName: "web_search", Input: json.RawMessage(`{"query":"internkim"}`)})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureStage() != "tool_result_contract" {
		t.Fatalf("expected web search effects rejection, got %+v", result)
	}
}

func canonicalReadDescriptor(toolName string) CapabilityToolDescriptor {
	resultSchema := `{"type":"object","additionalProperties":false,"properties":{"status":{"const":"ok","type":"string"},"path":{"minLength":1,"type":"string"},"format":{"const":"markdown","type":"string"},"content":{"type":"string"},"warnings":{"type":"array","items":{"type":"string"}},"truncated":{"type":"boolean"}},"required":["status","path","format","content","warnings","truncated"]}`
	inputSchema := `{"type":"object","additionalProperties":false,"properties":{"path":{"minLength":1,"type":"string"}},"required":["path"]}`
	if toolName == "image_read" {
		resultSchema = `{"type":"object","additionalProperties":false,"properties":{"status":{"const":"ok","type":"string"},"path":{"minLength":1,"type":"string"},"attachments":{"type":"array","minItems":1,"items":{"type":"object","additionalProperties":false,"properties":{"devicePath":{"minLength":1,"type":"string"},"filename":{"minLength":1,"type":"string"},"contentType":{"minLength":1,"type":"string"},"sizeBytes":{"type":"integer","minimum":0},"contentBase64":{"minLength":1,"type":"string"}},"required":["devicePath","filename","contentType","sizeBytes","contentBase64"]}}},"required":["status","path","attachments"]}`
	}
	return CapabilityToolDescriptor{
		Name:            toolName,
		CanonicalName:   toolName,
		Namespace:       strings.SplitN(toolName, ".", 2)[0],
		ModelName:       toolName,
		ModelVisibility: toolcontract.ToolVisibilityModel,
		Description:     "Canonical read test descriptor.",
		PrivacyClass:    "workspace_document",
		InputSchema:     json.RawMessage(inputSchema),
		OutputSchema:    json.RawMessage(`{"type":"object","additionalProperties":false}`),
		ResultContract:  &CapabilityToolResultContract{Schema: json.RawMessage(resultSchema)},
		PolicyResource:  "tool:" + toolName,
		SideEffectClass: "read",
		Availability:    CapabilityAvailability{State: "ok"},
		Idempotency:     CapabilityIdempotency{Scope: "operation"},
	}
}

func canonicalWebSearchDescriptor() CapabilityToolDescriptor {
	inputSchema := `{"type":"object","additionalProperties":false,"properties":{"query":{"type":"string"},"location":{"type":"string"},"language":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":10},"allowedDomains":{"type":"array","items":{"type":"string"}},"excludedDomains":{"type":"array","items":{"type":"string"}}},"required":["query"]}`
	resultSchema := `{"type":"object","additionalProperties":false,"properties":{"provider":{"type":"string"},"remoteLLMInvolved":{"type":"boolean"},"compatibility":{"type":"string"},"query":{"type":"string"},"answer":{"type":"string"},"results":{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"title":{"type":"string"},"url":{"type":"string"},"snippet":{"type":"string"},"source":{"type":"string"}},"required":["title","url","snippet"]}}},"required":["provider","remoteLLMInvolved","compatibility","query","answer","results"]}`
	return CapabilityToolDescriptor{
		Name:            "web_search",
		CanonicalName:   "web_search",
		Namespace:       "web",
		ModelName:       "web_search",
		ModelVisibility: toolcontract.ToolVisibilityModel,
		Description:     "Canonical web search test descriptor.",
		PrivacyClass:    "public_web",
		InputSchema:     json.RawMessage(inputSchema),
		OutputSchema:    json.RawMessage(`{"type":"object","additionalProperties":false}`),
		ResultContract:  &CapabilityToolResultContract{Schema: json.RawMessage(resultSchema)},
		PolicyResource:  "tool:web_search",
		SideEffectClass: "read",
		Availability:    CapabilityAvailability{State: "ok"},
		Idempotency:     CapabilityIdempotency{Scope: "operation"},
	}
}

func TestImageGenerateSendsRequesterWorkspacePathToBridge(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		expectedPath string
	}{
		{
			name:         "requester workspace",
			path:         "/workspace/private/people/person-1/generated.png",
			expectedPath: "/workspace/private/people/person-1/generated.png",
		},
		{
			name:         "circle workspace",
			path:         "/workspace/circles/finance/generated.png",
			expectedPath: "/workspace/circles/finance/generated.png",
		},
		{
			name:         "home relative path",
			path:         "~/generated.png",
			expectedPath: "/workspace/private/people/person-1/generated.png",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workspacePath := t.TempDir()
			httpClient := &recordingHTTPClient{responseBody: `{"provider":"internkim","selectedBackend":"device","toolName":"image_generate","outcome":"succeeded","content":"generated","status":"ok","result":{}}`}
			toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
			toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
				Name:           "image_generate",
				PolicyResource: "tool:image_generate",
				InputSchema:    json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"path":{"type":"string"}},"required":["prompt","path"],"additionalProperties":false}`),
			}})
			toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
				"default": {"image_generate"},
			}, nil)
			toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
				ProfileName:       "default",
				RequesterPersonID: "person-1",
				PersonAccess: policy.PersonAccess{
					PersonID: "person-1",
					Circles:  []string{"member"},
				},
			})

			result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
				ToolName: "image_generate",
				Input: toolcontract.MarshalToolInput(map[string]string{
					"prompt": "a generated test image",
					"path":   test.path,
				}),
			})
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if result.Failed() {
				t.Fatalf("expected image_generate success, got %s", result.ContentText())
			}
			if httpClient.requestPath != "/v1/tools/image_generate/invoke" || !strings.Contains(httpClient.requestBody, test.expectedPath) {
				t.Fatalf("expected image_generate bridge request with %s, got path=%s body=%s", test.expectedPath, httpClient.requestPath, httpClient.requestBody)
			}
		})
	}
}

func TestCapabilityToolIdempotencyKeyOnlyForSendTools(t *testing.T) {
	ctx := toolcontract.WithObservationID(toolcontract.WithTaskRunID(context.Background(), "run-1"), "obs-3")
	sendDescriptor := CapabilityToolDescriptor{CanonicalName: "message_send", Idempotency: CapabilityIdempotency{Supported: true}}
	readDescriptor := CapabilityToolDescriptor{CanonicalName: "web_search"}
	sendKey := capabilityToolIdempotencyKey(ctx, sendDescriptor)
	if sendKey == "" {
		t.Fatal("expected idempotency key for send tool")
	}
	if again := capabilityToolIdempotencyKey(ctx, sendDescriptor); again != sendKey {
		t.Fatalf("idempotency key not deterministic: %q vs %q", sendKey, again)
	}
	differentObservation := toolcontract.WithObservationID(toolcontract.WithTaskRunID(context.Background(), "run-1"), "obs-4")
	if other := capabilityToolIdempotencyKey(differentObservation, sendDescriptor); other == sendKey {
		t.Fatal("expected different observation to produce different key")
	}
	if nonSend := capabilityToolIdempotencyKey(ctx, readDescriptor); nonSend != "" {
		t.Fatalf("expected no key for non-send tool, got %q", nonSend)
	}
	missing := toolcontract.WithTaskRunID(context.Background(), "run-1")
	if noObservation := capabilityToolIdempotencyKey(missing, sendDescriptor); noObservation != "" {
		t.Fatalf("expected no key without observation id, got %q", noObservation)
	}
}

func TestToolCatalogQuarantinesCapabilityDescriptorCollidingWithKernelTool(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("expected a colliding capability descriptor to quarantine instead of panicking, got panic: %v", recovered)
		}
	}()
	toolCatalogBuilder := NewToolCatalogBuilder()
	reportedProviders := []toolcontract.QuarantinedToolProvider{}
	toolCatalogBuilder.UseCapabilityQuarantineReporter(func(quarantinedProvider toolcontract.QuarantinedToolProvider) {
		reportedProviders = append(reportedProviders, quarantinedProvider)
	})
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{{
		Name:        toolcontract.FileReadToolName,
		Description: "Colliding capability tool.",
	}})

	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	if !toolRegistry.IsRegistered(toolcontract.FileReadToolName) {
		t.Fatal("expected the trusted kernel tool to remain registered after the capability collision")
	}
	if len(reportedProviders) != 1 || reportedProviders[0].ProviderID != "capabilityd" {
		t.Fatalf("expected the capabilityd provider to be quarantined, got %+v", reportedProviders)
	}
}

func TestCapabilityCatalogParametersListsRequiredAndOptional(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"status":{"type":"string"},"startDate":{"type":"string"}},"required":["prompt"]}`)
	got := capabilityCatalogParameters(schema)
	want := "{ prompt string (required), startDate string, status string }"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if capabilityCatalogParameters(nil) != "" {
		t.Fatal("nil schema should yield no parameters")
	}
}

func TestCapabilityRecoveryHintsCarryTheToolNamesTheCapabilityNamed(t *testing.T) {
	hints := capabilityRecoveryHints(json.RawMessage(`{"errorCode":"flow_owner_ambiguous","recoveryHints":[{"action":"ask_the_user_to_choose_a_candidate","toolNames":["ask_input"],"reason":"only the user can say which person they meant"}]}`))

	if len(hints) != 1 || len(hints[0].ToolNames) != 1 || hints[0].ToolNames[0] != "ask_input" {
		t.Fatalf("hints = %+v", hints)
	}
	if hints[0].Action != "ask_the_user_to_choose_a_candidate" {
		t.Fatalf("hints = %+v", hints)
	}
}

func TestCapabilityRecoveryHintsKeepTheLegacyRecoveryAction(t *testing.T) {
	hints := capabilityRecoveryHints(json.RawMessage(`{"recovery":{"kind":"companion_connect","delivery":"direct_message"}}`))

	if len(hints) != 1 || hints[0].Action != "companion_connect" {
		t.Fatalf("hints = %+v", hints)
	}
}

func TestCapabilityRecoveryHintsIgnoreAnEmptyHint(t *testing.T) {
	if hints := capabilityRecoveryHints(json.RawMessage(`{"recoveryHints":[{"reason":"no action, no tools"}]}`)); len(hints) != 0 {
		t.Fatalf("hints = %+v", hints)
	}
}

// A capability that reads a file is handed its content, read here as the person
// who asked, so a test about what the capability answers still needs a file and
// somebody to read it as.
func capabilityReadTestCatalogBuilder(t *testing.T) *ToolCatalogBuilder {
	t.Helper()
	workspacePath := t.TempDir()
	if errorValue := os.WriteFile(filepath.Join(workspacePath, "report.md"), []byte("report"), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	terminalService := security.NewShellService(config.TerminalConfiguration{
		WorkspaceRootPath: workspacePath,
		Mode:              "virtualMachineGuest",
		TimeoutSecond:     30,
	})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolCatalogBuilder.UseWorkspaceActorFactory(security.NewDirectWorkspaceActorFactory(terminalService))
	return toolCatalogBuilder
}

// Which tools carry workspace files is written in the contract: any capability
// whose input schema declares the attachments field is served by the carry,
// so message_update gained it the moment its schema did, with no list to keep.
func TestAttachmentCarryingFollowsTheDescriptorSchema(testContext *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{
		{Name: "message_update", InputSchema: json.RawMessage(`{"type":"object","properties":{"messageID":{"type":"string"},"attachments":{"type":"array","items":{"type":"string"}}}}`)},
		{Name: "message_context", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
	})

	if !toolCatalogBuilder.capabilityToolCarriesWorkspaceAttachments("message_update") {
		testContext.Fatal("a schema that declares attachments must have its files carried")
	}
	if toolCatalogBuilder.capabilityToolCarriesWorkspaceAttachments("message_context") {
		testContext.Fatal("a schema without attachments must not trigger the carry")
	}
	if toolCatalogBuilder.capabilityToolCarriesWorkspaceAttachments("no_such_tool") {
		testContext.Fatal("an unregistered tool must not trigger the carry")
	}
}

// The path the model gives a read capability may be a fileHint or the exact
// URL it saw in a message's text; a POSIX miss falls back to the attachment
// resolver, which imports the file and hands the read its workspace path. The
// incident this guards: an image in a message outside the visible window was
// reachable only as a URL, and the model invented a filesystem path from it.
func TestCapabilityReadResolvesAnAttachmentReference(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{"provider":"internkim","selectedBackend":"device","toolName":"document_read","outcome":"succeeded","status":"ok","result":{"status":"ok","path":"/workspace/report.md","format":"markdown","content":"report","warnings":[],"truncated":false}}`}
	toolCatalogBuilder := capabilityReadTestCatalogBuilder(t)
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{canonicalReadDescriptor("document_read")})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"document_read"})
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
		AttachmentMaterialResolver: staticAttachmentMaterialResolver{
			material: agentcontract.VisibleContextMaterial{
				Platform:    "buzz",
				FileID:      "report",
				Path:        "/workspace/report.md",
				Filename:    "report.md",
				ContentType: "text/markdown",
				IsAvailable: true,
			},
		},
	})

	result, errorValue := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{ToolName: "document_read", Input: json.RawMessage(`{"path":"https://relay.test/media/report.md"}`)})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("a URL reference the resolver serves must read, got %+v", result)
	}
	if !strings.Contains(httpClient.requestBody, `"path":"/workspace/report.md"`) {
		t.Fatalf("expected the resolved workspace path to travel, got %s", httpClient.requestBody)
	}
}

func TestCapabilityReadKeepsTheRefusalWhenNothingResolves(t *testing.T) {
	toolCatalogBuilder := capabilityReadTestCatalogBuilder(t)
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: &recordingHTTPClient{}}, []CapabilityToolDescriptor{canonicalReadDescriptor("document_read")})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"document_read"})
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
	})

	result, errorValue := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{ToolName: "document_read", Input: json.RawMessage(`{"path":"/workspace/no-such-file.md"}`)})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureStage() != "document_read" {
		t.Fatalf("a genuinely missing path must keep its refusal, got %+v", result)
	}
}
