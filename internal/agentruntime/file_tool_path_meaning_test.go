package agentruntime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func pathDescriptions(schemaDocument json.RawMessage) []string {
	var node any
	if json.Unmarshal(schemaDocument, &node) != nil {
		return nil
	}
	return collectPathDescriptions(node)
}

func collectPathDescriptions(node any) []string {
	object, isObject := node.(map[string]any)
	if !isObject {
		return nil
	}
	descriptions := []string{}
	if properties, hasProperties := object["properties"].(map[string]any); hasProperties {
		if path, hasPath := properties["path"].(map[string]any); hasPath {
			description, _ := path["description"].(string)
			descriptions = append(descriptions, description)
		}
		for _, property := range properties {
			descriptions = append(descriptions, collectPathDescriptions(property)...)
		}
	}
	return append(descriptions, collectPathDescriptions(object["items"])...)
}

func TestEveryFileToolDescribesItsPathTheWayTheShellReadsIt(t *testing.T) {
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(t.TempDir())
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"member"}},
	})

	for _, toolName := range []string{"write", toolcontract.ReadToolName, "file_read", "file_preview", "edit", "file_delete", toolcontract.FileDeliverToolName} {
		definition, isRegistered := toolRegistry.ToolDefinition(toolName)
		if !isRegistered {
			t.Fatalf("expected %s registered", toolName)
		}
		descriptions := pathDescriptions(definition.InputSchema)
		if len(descriptions) == 0 {
			t.Fatalf("expected %s to take a path", toolName)
		}
		for _, description := range descriptions {
			if !strings.Contains(description, filePathMeaning) {
				t.Fatalf("every file tool runs its path through the requester's shell, so %s must say so; a path called a workspace path is read as one under the workspace root, got %q", toolName, description)
			}
		}
	}
}
