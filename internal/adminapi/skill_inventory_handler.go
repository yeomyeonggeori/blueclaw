package adminapi

import (
	"encoding/json"
	"net/http"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type SkillInventoryHandler struct {
	InstructionBundleLoader func() agentcontract.InstructionBundle
}

type SkillInventoryEntry struct {
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	Path           string   `json:"path"`
	ToolReferences []string `json:"toolReferences,omitempty"`
}

type SkillInventoryDocument struct {
	Skills []SkillInventoryEntry `json:"skills"`
}

// Which skills a running agent actually loaded, and where each one was read
// from. A host that ships a skill root the agent never opens looks healthy
// everywhere else, so this is the answer to "is the skill there".
func (handler SkillInventoryHandler) HandleListSkills(responseWriter http.ResponseWriter, _ *http.Request) {
	document := SkillInventoryDocument{Skills: []SkillInventoryEntry{}}
	if handler.InstructionBundleLoader != nil {
		for _, skillInstruction := range handler.InstructionBundleLoader().Skills {
			document.Skills = append(document.Skills, SkillInventoryEntry{
				Name:           skillInstruction.Name,
				Description:    skillInstruction.Description,
				Path:           skillInstruction.Source.Path,
				ToolReferences: skillInstruction.ToolReferences,
			})
		}
	}
	responseWriter.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(responseWriter).Encode(document)
}
