package adminapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestSkillInventoryNamesWhereEachSkillWasRead(t *testing.T) {
	handler := SkillInventoryHandler{InstructionBundleLoader: func() agentcontract.InstructionBundle {
		return agentcontract.InstructionBundle{Skills: []agentcontract.SkillInstruction{{
			Name:           "presentation",
			Description:    "builds decks",
			ToolReferences: []string{"shell"},
			Source:         agentcontract.InstructionSource{Path: "/delivery/skills/presentation/SKILL.md"},
		}}}
	}}

	recorder := httptest.NewRecorder()
	handler.HandleListSkills(recorder, httptest.NewRequest("GET", "/admin/api/skills", nil))

	document := SkillInventoryDocument{}
	if errorValue := json.Unmarshal(recorder.Body.Bytes(), &document); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(document.Skills) != 1 {
		t.Fatalf("expected the loaded skill, got %d", len(document.Skills))
	}
	if document.Skills[0].Path != "/delivery/skills/presentation/SKILL.md" {
		t.Fatalf("a host that ships a root the agent never opens is told apart by the path: %q", document.Skills[0].Path)
	}
}

func TestSkillInventoryAnswersWithoutALoader(t *testing.T) {
	recorder := httptest.NewRecorder()
	SkillInventoryHandler{}.HandleListSkills(recorder, httptest.NewRequest("GET", "/admin/api/skills", nil))

	if recorder.Body.String() != "{\"skills\":[]}\n" {
		t.Fatalf("expected an empty inventory, got %q", recorder.Body.String())
	}
}
