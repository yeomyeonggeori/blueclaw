package adminapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/skill"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestSkillInventoryNamesWhereEachSkillWasRead(t *testing.T) {
	handler := SkillInventoryHandler{InventoryLoader: func() SkillInventory {
		return SkillInventory{Loaded: []agentcontract.SkillInstruction{{
			Name:           "presentation",
			Description:    "builds decks",
			ToolReferences: []string{"bash"},
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

	if recorder.Body.String() != "{\"skills\":[],\"unavailableSkills\":[]}\n" {
		t.Fatalf("expected an empty inventory, got %q", recorder.Body.String())
	}
}

// A skill this host cannot satisfy is not simply gone: the inventory says
// which variable it wanted, so an operator can tell "not shipped" from "shipped
// and deliberately left out of the prompt".
func TestSkillInventoryNamesWhatAnUnavailableSkillLacks(t *testing.T) {
	handler := SkillInventoryHandler{InventoryLoader: func() SkillInventory {
		return SkillInventory{Unavailable: []skill.UnavailableSkill{{
			Name:                        "internkim-api",
			Path:                        "/delivery/skills/internkim-api/SKILL.md",
			MissingEnvironmentVariables: []string{"INTERNKIM_TOKEN"},
		}}}
	}}

	recorder := httptest.NewRecorder()
	handler.HandleListSkills(recorder, httptest.NewRequest("GET", "/admin/api/skills", nil))

	document := SkillInventoryDocument{}
	if errorValue := json.Unmarshal(recorder.Body.Bytes(), &document); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(document.Skills) != 0 {
		t.Fatalf("an unavailable skill must not be listed as loaded, got %d", len(document.Skills))
	}
	if len(document.UnavailableSkills) != 1 || document.UnavailableSkills[0].Name != "internkim-api" {
		t.Fatalf("expected internkim-api listed as unavailable, got %+v", document.UnavailableSkills)
	}
	missingVariableNames := document.UnavailableSkills[0].MissingEnvironmentVariables
	if len(missingVariableNames) != 1 || missingVariableNames[0] != "INTERNKIM_TOKEN" {
		t.Fatalf("expected the variable it lacks to be named, got %v", missingVariableNames)
	}
}

// The same for a file: a host missing the font its PDF skill embeds is told
// which paths would have done, on the surface an operator opens rather than in
// the struct.
func TestSkillInventoryNamesTheFilesThatWouldHaveDone(t *testing.T) {
	handler := SkillInventoryHandler{InventoryLoader: func() SkillInventory {
		return SkillInventory{Unavailable: []skill.UnavailableSkill{{
			Name:                "pdf",
			Path:                "/opt/internkim/skills/pdf/SKILL.md",
			MissingAnyFilePaths: []string{"/usr/share/fonts/truetype/nanum/NanumGothic.ttf", "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc"},
		}}}
	}}

	recorder := httptest.NewRecorder()
	handler.HandleListSkills(recorder, httptest.NewRequest("GET", "/admin/api/skills", nil))

	if !strings.Contains(recorder.Body.String(), `"missingAnyFilePaths":["/usr/share/fonts/truetype/nanum/NanumGothic.ttf"`) {
		t.Fatalf("the reason must reach the wire, not only the struct: %s", recorder.Body.String())
	}
	document := SkillInventoryDocument{}
	if errorValue := json.Unmarshal(recorder.Body.Bytes(), &document); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(document.UnavailableSkills) != 1 || len(document.UnavailableSkills[0].MissingAnyFilePaths) != 2 {
		t.Fatalf("expected both paths that would have done, got %+v", document.UnavailableSkills)
	}
}
