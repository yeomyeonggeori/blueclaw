package skill

import "strings"

// SkillDirectoryPlaceholder is what a portable SKILL.md writes where its own
// directory belongs, since a skill cannot know where a host installed it.
const SkillDirectoryPlaceholder = "<skill>"

type SkillPromptBuilder struct{}

func (skillPromptBuilder SkillPromptBuilder) BuildSkillPrompt(skillBundles []SkillBundle) string {
	parts := []string{}
	for _, skillBundle := range skillBundles {
		parts = append(parts, ResolveSkillDirectoryPlaceholder(skillBundle.Instruction, skillBundle.DirectoryPath))
	}
	return strings.Join(parts, "\n\n")
}

func ResolveSkillDirectoryPlaceholder(instruction string, directoryPath string) string {
	if strings.TrimSpace(directoryPath) == "" {
		return instruction
	}
	return strings.ReplaceAll(instruction, SkillDirectoryPlaceholder, directoryPath)
}
