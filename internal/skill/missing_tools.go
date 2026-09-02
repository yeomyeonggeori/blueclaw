package skill

import "strings"

// A skill states the tools it needs in its tool-references. A host that offers a
// catalog without one of them has a skill that cannot do what it says, and the
// agent finds out mid-task; this is what lets it be said at start instead.
func (skillBundle SkillBundle) MissingToolNames(offeredToolNames []string) []string {
	offered := map[string]bool{}
	for _, toolName := range offeredToolNames {
		if trimmed := strings.TrimSpace(toolName); trimmed != "" {
			offered[trimmed] = true
		}
	}

	missingToolNames := []string{}
	for _, reference := range skillBundle.ToolReferences {
		toolName := strings.TrimSpace(string(reference))
		if toolName == "" || offered[toolName] {
			continue
		}
		missingToolNames = append(missingToolNames, toolName)
	}
	return missingToolNames
}
