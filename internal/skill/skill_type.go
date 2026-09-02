package skill

type ToolReference string

type SkillBundle struct {
	Name                         string          `json:"name"`
	Description                  string          `json:"description,omitempty"`
	ToolReferences               []ToolReference `json:"toolReferences,omitempty"`
	RequiredEnvironmentVariables []string        `json:"requiredEnvironmentVariables,omitempty"`
	Instruction                  string          `json:"instruction"`
	DirectoryPath                string          `json:"directoryPath"`
}

func (skillBundle SkillBundle) ReferencedToolNames() []string {
	toolNames := make([]string, 0, len(skillBundle.ToolReferences))
	for _, toolReference := range skillBundle.ToolReferences {
		toolNames = append(toolNames, string(toolReference))
	}
	return toolNames
}
