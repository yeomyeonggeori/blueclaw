package agentruntime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/persona"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestPersonaUpdateToolPreservesNestedSoulFields(t *testing.T) {
	workspaceRoot := t.TempDir()
	initial, errorValue := persona.CanonicalSoul(persona.Soul{WorkingStyle: []string{"steady"}, Tone: &persona.Tone{Register: "polite", Traits: []string{"warm"}}})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := persona.SaveDocument(workspaceRoot, persona.SoulFileName, initial); errorValue != nil {
		t.Fatal(errorValue)
	}
	builder := NewToolCatalogBuilder()
	builder.workspaceRootPath = workspaceRoot
	builder.UseAllowedToolNamesByProfile(nil, []string{"persona_update"})
	toolSet := builder.BuildToolSet(ToolCatalogRequest{ProfileName: "default", PersonAccess: policy.PersonAccess{PersonID: "person-1", Circles: []string{policy.AdminCircleID}}})
	result, errorValue := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{ToolName: "persona_update", Input: toolcontract.MarshalToolInput(map[string]any{"target": "soul", "patch": map[string]any{"tone": map[string]any{"register": "formal"}}})})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatal("foreground persona_update must reject soul mutation")
	}
}

func TestPersonaToolSchemaRejectsIdentityFields(t *testing.T) {
	var schema map[string]any
	if errorValue := json.Unmarshal(personaToolSchema, &schema); errorValue != nil {
		t.Fatal(errorValue)
	}
	properties := schema["properties"].(map[string]any)
	patch := properties["patch"].(map[string]any)
	if _, exists := patch["anyOf"]; !exists {
		t.Fatal("persona patch schema must derive target document schemas")
	}
}
