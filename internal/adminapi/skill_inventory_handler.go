package adminapi

import (
	"encoding/json"
	"net/http"

	"github.com/yeomyeonggeori/blueclaw/internal/skill"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type SkillInventory struct {
	Loaded      []agentcontract.SkillInstruction
	Unavailable []skill.UnavailableSkill
}

type SkillInventoryHandler struct {
	InventoryLoader func() SkillInventory
}

type SkillInventoryEntry struct {
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	Path           string   `json:"path"`
	ToolReferences []string `json:"toolReferences,omitempty"`
}

type SkillInventoryDocument struct {
	Skills            []SkillInventoryEntry    `json:"skills"`
	UnavailableSkills []skill.UnavailableSkill `json:"unavailableSkills"`
}

// Which skills a running agent actually loaded, and where each one was read
// from. A host that ships a skill root the agent never opens looks healthy
// everywhere else, so this is the answer to "is the skill there". A skill this
// host cannot satisfy is listed under unavailableSkills with the environment
// variable it lacks, because a skill that is simply gone from both lists is a
// different failure than one deliberately left out.
func (handler SkillInventoryHandler) HandleListSkills(responseWriter http.ResponseWriter, _ *http.Request) {
	document := SkillInventoryDocument{Skills: []SkillInventoryEntry{}, UnavailableSkills: []skill.UnavailableSkill{}}
	if handler.InventoryLoader != nil {
		inventory := handler.InventoryLoader()
		for _, skillInstruction := range inventory.Loaded {
			document.Skills = append(document.Skills, SkillInventoryEntry{
				Name:           skillInstruction.Name,
				Description:    skillInstruction.Description,
				Path:           skillInstruction.Source.Path,
				ToolReferences: skillInstruction.ToolReferences,
			})
		}
		document.UnavailableSkills = append(document.UnavailableSkills, inventory.Unavailable...)
	}
	responseWriter.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(responseWriter).Encode(document)
}
