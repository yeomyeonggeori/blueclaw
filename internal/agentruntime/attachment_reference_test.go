package agentruntime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

// The reference vocabulary is a workspace path or an attachment's url. A url
// resolves through the conversation's own record and reads the imported file.
func TestFileReadResolvesAnAttachmentURL(t *testing.T) {
	workspacePath := t.TempDir()
	relativePath := "private/people/person-1/inbox/buzz/root-1/report.txt"
	writeTestFile(t, filepath.Join(workspacePath, filepath.FromSlash(relativePath)), "attachment content")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
		AttachmentMaterialResolver: staticAttachmentMaterialResolver{
			material: agentcontract.VisibleContextMaterial{
				MaterialID:  "buzz:report",
				URL:         "https://relay.test/media/report.txt",
				Path:        filepath.Join(workspacePath, filepath.FromSlash(relativePath)),
				Filename:    "report.txt",
				ContentType: "text/plain",
				IsAvailable: true,
			},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_read",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path": "https://relay.test/media/report.txt",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() || !strings.Contains(result.ContentText(), "attachment content") {
		t.Fatalf("expected the url to read the imported file, got %s", result.ContentText())
	}
}

func TestFileToolsPreserveExplicitPathResolutionAndAccess(t *testing.T) {
	workspacePath := t.TempDir()
	ownPath := filepath.Join(workspacePath, "private", "people", "person-1", "documents", "notes.txt")
	otherPath := filepath.Join(workspacePath, "private", "people", "person-2", "documents", "secret.txt")
	writeTestFile(t, ownPath, "own content")
	writeTestFile(t, otherPath, "secret content")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	readResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_read",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path": "documents/notes.txt",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if readResult.Failed() || !strings.Contains(readResult.ContentText(), "own content") {
		t.Fatalf("expected explicit path to remain readable, got %s", readResult.ContentText())
	}

	otherPersonHomePath := filepath.Join(workspacePath, "private", "people", "person-2")
	withoutDirectoryAccess(t, otherPersonHomePath)
	accessResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_read",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path": otherPath,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !accessResult.Failed() || accessResult.FailureCode() != toolcontract.FailureCodes.AccessDenied.String() {
		t.Fatalf("expected the OS denial on the other person's home to surface as access_denied, got %s", accessResult.ContentText())
	}
}

// The invented locator vocabulary must not resurface in the schemas the model reads.
func TestFileToolSchemasSpeakPathOrURLOnly(t *testing.T) {
	toolRegistry := newFileToolTestCatalogBuilder(t.TempDir()).BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	for _, toolName := range []string{"file_read", "file_preview"} {
		toolDefinition, isFound := findToolDefinition(toolRegistry.ListToolDefinitions(), toolName)
		if !isFound {
			t.Fatalf("expected %s definition", toolName)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if errorValue := json.Unmarshal(toolDefinition.InputSchema, &schema); errorValue != nil {
			t.Fatal(errorValue)
		}
		for _, forbidden := range []string{"fileHint", "materialID"} {
			if _, isFound := schema.Properties[forbidden]; isFound {
				t.Fatalf("expected %s schema to drop %s", toolName, forbidden)
			}
		}
		if _, isFound := schema.Properties["path"]; !isFound {
			t.Fatalf("expected %s schema to keep path", toolName)
		}
	}
}

func TestAttachmentURLReferenceIsExactWireGrammar(t *testing.T) {
	for reference, expected := range map[string]bool{
		"https://relay.test/media/abc.png":        true,
		"http://mattermost.local/api/v4/files/f1": true,
		"/workspace/private/people/p/report.md":   false,
		"documents/notes.txt":                     false,
		"attachment:buzz:image":                   false,
		"ftp://relay.test/file":                   false,
		"":                                        false,
	} {
		if isAttachmentURLReference(reference) != expected {
			t.Fatalf("isAttachmentURLReference(%q) should be %v", reference, expected)
		}
	}
}
