package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
)

const taskAddDescriptor = `{
	"name": "task_add",
	"canonicalName": "task_add",
	"namespace": "task",
	"answeredBy": "record",
	"modelName": "task_add",
	"modelVisibility": "model",
	"description": "Create a task.",
	"privacyClass": "workspace_task",
	"policyResource": "tool:task_add",
	"sideEffectClass": "workspace_write",
	"inputSchema": {"type": "object", "properties": {"title": {"type": "string"}}, "additionalProperties": false},
	"outputSchema": {"type": "object", "properties": {"result": {}}, "additionalProperties": false},
	"availability": {"state": "ok"},
	"idempotency": {"supported": false, "required": false, "scope": "operation"}
}`

const companyDescriptor = `{
	"name": "message_send",
	"canonicalName": "message_send",
	"namespace": "message",
	"answeredBy": "company",
	"modelName": "message_send",
	"modelVisibility": "model",
	"description": "Send a message.",
	"privacyClass": "message",
	"policyResource": "tool:message_send",
	"sideEffectClass": "external_send",
	"inputSchema": {"type": "object", "properties": {"message": {"type": "string"}}, "additionalProperties": false},
	"outputSchema": {"type": "object", "properties": {"result": {}}, "additionalProperties": false},
	"availability": {"state": "ok"},
	"idempotency": {"supported": false, "required": false, "scope": "operation"}
}`

type carriedMessages struct {
	listedFor       string
	calledFor       string
	calledArguments json.RawMessage
}

func documentOf(t *testing.T, written string) map[string]any {
	t.Helper()
	var document map[string]any
	if errorValue := json.Unmarshal([]byte(written), &document); errorValue != nil {
		t.Fatal(errorValue)
	}
	return document
}

func aCatalogOnAServer(t *testing.T) (*RecordCatalog, *carriedMessages) {
	t.Helper()
	carried := &carriedMessages{}

	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "internkim", Version: "1"}, nil)
	server.AddReceivingMiddleware(func(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
		return func(ctx context.Context, method string, request sdkmcp.Request) (sdkmcp.Result, error) {
			switch asked := request.(type) {
			case *sdkmcp.ListToolsRequest:
				carried.listedFor = requesterOf(asked.Params.Meta)
			case *sdkmcp.CallToolRequest:
				carried.calledFor = requesterOf(asked.Params.Meta)
				carried.calledArguments, _ = json.Marshal(asked.Params.Arguments)
			}
			return next(ctx, method, request)
		}
	})
	server.AddTool(&sdkmcp.Tool{
		Name:        "task_add",
		Description: "Create a task.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"additionalProperties":false}`),
		Meta:        sdkmcp.Meta{DescriptorMetaKey: documentOf(t, taskAddDescriptor)},
	}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return &sdkmcp.CallToolResult{
			Content:           []sdkmcp.Content{&sdkmcp.TextContent{Text: `{"tool":"task_add","result":{"taskID":"task-1"}}`}},
			StructuredContent: json.RawMessage(`{"tool":"task_add","result":{"taskID":"task-1"}}`),
		}, nil
	})
	server.AddTool(&sdkmcp.Tool{
		Name:        "message_send",
		Description: "Send a message.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}},"additionalProperties":false}`),
		Meta:        sdkmcp.Meta{DescriptorMetaKey: documentOf(t, companyDescriptor)},
	}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "{}"}}}, nil
	})
	server.AddTool(&sdkmcp.Tool{
		Name:        "somebody_elses_tool",
		Description: "Belongs to another server.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "{}"}}}, nil
	})

	httpServer := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *sdkmcp.Server { return server },
		&sdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	))
	catalog := NewRecordCatalog(capability.Configuration{Endpoint: httpServer.URL})
	t.Cleanup(func() {
		_ = catalog.Close()
		httpServer.Close()
	})
	return catalog, carried
}

func requesterOf(meta sdkmcp.Meta) string {
	named, isCarried := meta[RequesterMetaKey]
	if !isCarried {
		return ""
	}
	email, _ := named.(string)
	return email
}

func TestRecordCatalogDiscoversWhatTheServerDescribes(t *testing.T) {
	catalog, carried := aCatalogOnAServer(t)

	discovered, errorValue := catalog.DiscoverTools(context.Background(), "Sample@Example.test")
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if carried.listedFor != "sample@example.test" {
		t.Fatalf("the listing named %q", carried.listedFor)
	}
	byName := map[string]capability.ToolDescriptor{}
	for _, descriptor := range discovered {
		byName[descriptor.ModelName] = descriptor
	}
	if len(byName) != 2 {
		t.Fatalf("discovery read %d descriptors: %v", len(byName), byName)
	}
	if byName["task_add"].AnsweredBy != capability.AnsweredByRecord ||
		byName["message_send"].AnsweredBy != capability.AnsweredByCompany {
		t.Fatalf("the descriptors read as %+v", byName)
	}
	if _, isDiscovered := byName["somebody_elses_tool"]; isDiscovered {
		t.Fatal("a tool carrying no descriptor was discovered")
	}
}

func TestRecordCatalogCallsAToolAsTheRequester(t *testing.T) {
	catalog, carried := aCatalogOnAServer(t)

	result, errorValue := catalog.CallTool(
		context.Background(),
		"Sample@Example.test",
		"task_add",
		json.RawMessage(`{"title":"쓰기"}`),
	)
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if carried.calledFor != "sample@example.test" {
		t.Fatalf("the call named %q", carried.calledFor)
	}
	if string(carried.calledArguments) != `{"title":"쓰기"}` {
		t.Fatalf("the arguments arrived as %s", carried.calledArguments)
	}
	if result.IsError {
		t.Fatalf("the call answered an error: %+v", result)
	}
	answered := documentOf(t, string(result.StructuredContent))
	if !reflect.DeepEqual(answered, documentOf(t, `{"tool":"task_add","result":{"taskID":"task-1"}}`)) {
		t.Fatalf("the structured answer was %s", result.StructuredContent)
	}
}

func TestRecordCatalogReadsTheDescriptorOffAListedTool(t *testing.T) {
	read, isCarried := descriptorCarriedBy(&sdkmcp.Tool{
		Name: "task_add",
		Meta: sdkmcp.Meta{DescriptorMetaKey: documentOf(t, taskAddDescriptor)},
	})

	if !isCarried {
		t.Fatal("the descriptor on _meta was not read")
	}
	if read.PolicyResource != "tool:task_add" || read.Availability.State != "ok" {
		t.Fatalf("the descriptor lost fields on the way in: %+v", read)
	}
	if len(read.InputSchema) == 0 || len(read.OutputSchema) == 0 {
		t.Fatalf("the schemas arrived as %s and %s", read.InputSchema, read.OutputSchema)
	}
}

func TestADescriptorNamingAnotherToolIsNotDiscovered(t *testing.T) {
	if _, isCarried := descriptorCarriedBy(&sdkmcp.Tool{
		Name: "task_delete",
		Meta: sdkmcp.Meta{DescriptorMetaKey: documentOf(t, taskAddDescriptor)},
	}); isCarried {
		t.Fatal("a descriptor naming another tool was discovered")
	}
}
