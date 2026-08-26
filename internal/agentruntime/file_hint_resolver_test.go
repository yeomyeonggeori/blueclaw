package agentruntime

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestFileReadResolvesAttachmentFileHint(t *testing.T) {
	workspacePath := t.TempDir()
	relativePath := "inbox/mattermost/post-1/report.txt"
	writeTestFile(t, filepath.Join(workspacePath, "private", "people", "person-1", relativePath), "attachment content")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"member"}},
		VisibleContext: agentcontract.VisibleContext{CurrentMaterials: []agentcontract.VisibleContextMaterial{{
			FileHint:   "attachment:mattermost:file-1",
			MaterialID: "mattermost:file-1",
			Path:       relativePath,
		}}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_read",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"fileHint": "attachment:mattermost:file-1",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() || !strings.Contains(result.ContentText(), "attachment content") {
		t.Fatalf("expected attachment fileHint read success, got %s", result.ContentText())
	}
}

func TestFilePreviewResolvesArtifactFileHint(t *testing.T) {
	workspacePath := t.TempDir()
	relativePath := "private/people/person-1/tmp/artifacts/report.md"
	writeTestFile(t, filepath.Join(workspacePath, filepath.FromSlash(relativePath)), "# Artifact report")
	fileHint := "artifact:task-run-1:" + url.QueryEscape(relativePath)
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"member"}},
		VisibleContext: agentcontract.VisibleContext{Materials: []agentcontract.VisibleContextMaterial{{
			FileHint:    fileHint,
			Path:        filepath.Join(workspacePath, filepath.FromSlash(relativePath)),
			Filename:    "report.md",
			IsAvailable: true,
		}}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_preview",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"fileHint": fileHint,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() || !strings.Contains(result.ContentText(), "Artifact report") {
		t.Fatalf("expected artifact fileHint preview success, got %s", result.ContentText())
	}
}

func TestFileHintRejectsUnknownAndForgedValues(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"member"}},
	})
	for _, fileHint := range []string{
		"attachment:mattermost:forged",
		"artifact:task-run-1:%2Fworkspace%2Fprivate%2Fpeople%2Fperson-1%2Fsecret.txt",
	} {
		result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
			ToolName: "file_read",
			Input: toolcontract.MarshalToolInput(map[string]string{
				"fileHint": fileHint,
			}),
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if !result.Failed() || !strings.Contains(result.ContentText(), "fileHint") {
			t.Fatalf("expected forged fileHint %q to fail, got %s", fileHint, result.ContentText())
		}
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
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"member"}},
	})

	readResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "file_read",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"path":     "documents/notes.txt",
			"fileHint": "attachment:mattermost:forged",
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

func TestFileToolSchemasExposeFileHint(t *testing.T) {
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
		if _, isFound := schema.Properties["fileHint"]; !isFound {
			t.Fatalf("expected %s schema to expose fileHint", toolName)
		}
	}
}

func TestFileHintResolverUsesVisibleCurrentAndPreviousMaterials(t *testing.T) {
	request := ToolCatalogRequest{VisibleContext: agentcontract.VisibleContext{
		CurrentMaterials: []agentcontract.VisibleContextMaterial{{FileHint: "attachment:current", Path: "current.txt"}},
		Materials:        []agentcontract.VisibleContextMaterial{{FileHint: "attachment:previous", Path: "previous.txt"}},
	}}
	for _, testCase := range []struct {
		fileHint string
		path     string
	}{
		{fileHint: "attachment:current", path: "current.txt"},
		{fileHint: "attachment:previous", path: "previous.txt"},
	} {
		resolvedPath, _, errorValue := resolveFileHintReference(request, "", "", testCase.fileHint)
		if errorValue != nil || resolvedPath != testCase.path {
			t.Fatalf("expected %s to resolve to %s, got %s and %v", testCase.fileHint, testCase.path, resolvedPath, errorValue)
		}
	}
}

func TestFileHintResolverDoesNotDecodePaths(t *testing.T) {
	request := ToolCatalogRequest{VisibleContext: agentcontract.VisibleContext{Materials: []agentcontract.VisibleContextMaterial{{
		FileHint: "artifact:task-run-1:report%2Ffinal.md",
		Path:     "artifacts/report.md",
	}}}}
	resolvedPath, _, errorValue := resolveFileHintReference(request, "", "", "artifact:task-run-1:report/final.md")
	if errorValue == nil || resolvedPath != "" {
		t.Fatalf("expected differently encoded artifact hint to remain unresolved, got %s and %v", resolvedPath, errorValue)
	}
}

func TestFileHintResolverPreservesExplicitMaterialID(t *testing.T) {
	resolvedPath, resolvedMaterialID, errorValue := resolveFileHintReference(ToolCatalogRequest{}, "", "material-1", "attachment:unknown")
	if errorValue != nil || resolvedPath != "" || resolvedMaterialID != "material-1" {
		t.Fatalf("expected explicit materialID to take precedence, got %q %q and %v", resolvedPath, resolvedMaterialID, errorValue)
	}
}

func TestFileHintResolverDoesNotRequireFileExistence(t *testing.T) {
	request := ToolCatalogRequest{VisibleContext: agentcontract.VisibleContext{CurrentMaterials: []agentcontract.VisibleContextMaterial{{
		FileHint: "attachment:mattermost:missing",
		Path:     "inbox/missing.txt",
	}}}}
	resolvedPath, _, errorValue := resolveFileHintReference(request, "", "", "attachment:mattermost:missing")
	if errorValue != nil || resolvedPath != "inbox/missing.txt" {
		t.Fatalf("expected trusted material path resolution before stat, got %s and %v", resolvedPath, errorValue)
	}
	if _, errorValue := os.Stat(filepath.Join(t.TempDir(), resolvedPath)); errorValue == nil {
		t.Fatal("expected fixture path to remain absent")
	}
}
