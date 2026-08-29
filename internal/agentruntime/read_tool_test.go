package agentruntime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func readToolTestRequest() ToolCatalogRequest {
	return ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	}
}

func TestReadReturnsWorkspaceTextAsExactText(t *testing.T) {
	workspacePath := t.TempDir()
	writeTestFile(t, filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "notes.txt"), "one\ntwo\nthree")
	toolRegistry := newFileToolTestCatalogBuilder(workspacePath).BuildToolSet(readToolTestRequest())

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.ReadToolName,
		Input:    toolcontract.MarshalToolInput(map[string]any{"path": "tmp/notes.txt", "startLine": 2, "lineCount": 2}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected read success, got %s", result.ContentText())
	}
	resultData := map[string]any{}
	if errorValue := json.Unmarshal([]byte(result.ContentText()), &resultData); errorValue != nil {
		t.Fatal(errorValue)
	}
	if resultData["content"] != "two\nthree" || resultData["startLine"] != float64(2) {
		t.Fatalf("expected the exact text range, got %+v", resultData)
	}
}

func TestReadReturnsADocumentAsConvertedMarkdown(t *testing.T) {
	workspacePath := t.TempDir()
	writeTestFile(t, filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "brief.html"), "<h1>Quarterly Brief</h1>")
	toolRegistry := newFileToolTestCatalogBuilder(workspacePath).BuildToolSet(readToolTestRequest())

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.ReadToolName,
		Input:    toolcontract.MarshalToolInput(map[string]any{"path": "tmp/brief.html"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected read success, got %s", result.ContentText())
	}
	if !strings.Contains(result.ContentText(), "Quarterly Brief") || !strings.Contains(result.ContentText(), `"conversionStatus":"converted"`) {
		t.Fatalf("expected a converted document preview, got %s", result.ContentText())
	}
}

func TestReadReturnsAnImageAttachmentThroughTheImageBackend(t *testing.T) {
	workspacePath := t.TempDir()
	writeTestFile(t, filepath.Join(workspacePath, "circles", "staff", "inbox", "mattermost", "post-1", "mascot.png"), "image")
	httpClient := &recordingHTTPClient{responseBody: `{"provider":"internkim","selectedBackend":"device","toolName":"image_read","outcome":"succeeded","status":"ok","result":{"status":"ok","path":"/workspace/circles/staff/inbox/mattermost/post-1/mascot.png","attachments":[{"devicePath":"/workspace/circles/staff/inbox/mattermost/post-1/mascot.png","filename":"mascot.png","contentType":"image/png","sizeBytes":5,"contentBase64":"aW1hZ2U="}]}}`}
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{canonicalReadDescriptor("image_read")})
	request := readToolTestRequest()
	request.AttachmentMaterialResolver = staticAttachmentMaterialResolver{
		material: agentcontract.VisibleContextMaterial{
			Platform:    "mattermost",
			FileID:      "file-1",
			URL:         "https://mattermost.local/api/v4/files/file-1",
			Filename:    "mascot.png",
			ContentType: "image/png",
			Path:        "/workspace/circles/staff/inbox/mattermost/post-1/mascot.png",
		},
	}
	toolRegistry := toolCatalogBuilder.BuildToolSet(request)

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.ReadToolName,
		Input:    toolcontract.MarshalToolInput(map[string]any{"path": "https://mattermost.local/api/v4/files/file-1"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected read success, got %s", result.ContentText())
	}
	if len(result.Attachments) != 1 || !strings.HasPrefix(result.Attachments[0].ContentType, "image/") {
		t.Fatalf("expected an image attachment the model can see, got %+v", result.Attachments)
	}
}

// A persisted ledger and a resumed approval carry the name a call was recorded
// under, so the backends the read tool replaced stay callable while they are
// gone from the catalog the model reads.
func TestReadBackendsStayCallableWhileHiddenFromTheModel(t *testing.T) {
	workspacePath := t.TempDir()
	writeTestFile(t, filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "notes.txt"), "one")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{
		hiddenReadDescriptor("document_read"),
		hiddenReadDescriptor("image_read"),
	})
	toolRegistry := toolCatalogBuilder.BuildToolSet(readToolTestRequest())

	if !toolRegistry.CanExpose(toolcontract.ReadToolName) {
		t.Fatal("expected read to be the reading tool the model sees")
	}
	for _, toolName := range []string{"file_read", "file_preview", "document_read", "image_read"} {
		if !toolRegistry.IsRegistered(toolName) {
			t.Fatalf("expected %s to stay registered for recorded calls", toolName)
		}
		if toolRegistry.CanExpose(toolName) {
			t.Fatalf("expected %s to stay out of the model catalog", toolName)
		}
	}

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_read",
		Input:    toolcontract.MarshalToolInput(map[string]any{"path": "tmp/notes.txt"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected a recorded file_read call to keep working, got %s", result.ContentText())
	}
}

func hiddenReadDescriptor(toolName string) CapabilityToolDescriptor {
	descriptor := canonicalReadDescriptor(toolName)
	descriptor.ModelVisibility = toolcontract.ToolVisibilityInternal
	return descriptor
}
