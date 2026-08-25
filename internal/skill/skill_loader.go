package skill

import (
	"os"
	"path/filepath"
	"strings"
)

type SkillLoader struct{}

func (skillLoader SkillLoader) LoadSkillBundle(directoryPath string) (SkillBundle, error) {
	documentPath := filepath.Join(directoryPath, "SKILL.md")
	document, errorValue := os.ReadFile(documentPath)
	if errorValue != nil {
		return SkillBundle{}, errorValue
	}

	metadata, instruction := parseSkillDocument(string(document))
	if strings.TrimSpace(metadata.Name) == "" {
		metadata.Name = filepath.Base(directoryPath)
	}
	if strings.TrimSpace(metadata.Description) == "" {
		metadata.Description = firstMarkdownParagraph(instruction)
	}

	return SkillBundle{
		Name:           metadata.Name,
		Description:    metadata.Description,
		ToolReferences: metadata.ToolReferences,
		Instruction:    strings.TrimSpace(instruction),
		DirectoryPath:  directoryPath,
	}, nil
}

type skillMetadata struct {
	Name           string
	Description    string
	ToolReferences []ToolReference
}

func parseSkillDocument(document string) (skillMetadata, string) {
	trimmedDocument := strings.TrimSpace(document)
	if !strings.HasPrefix(trimmedDocument, "---\n") {
		return skillMetadata{}, document
	}
	remainingDocument := strings.TrimPrefix(trimmedDocument, "---\n")
	frontmatter, instruction, hasFrontmatter := strings.Cut(remainingDocument, "\n---")
	if !hasFrontmatter {
		return skillMetadata{}, document
	}
	return parseSkillFrontmatter(frontmatter), strings.TrimSpace(instruction)
}

func parseSkillFrontmatter(frontmatter string) skillMetadata {
	metadata := skillMetadata{}
	section := ""
	for _, line := range strings.Split(frontmatter, "\n") {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
		}
		if strings.HasPrefix(trimmedLine, "- ") {
			metadata = appendSkillToolReference(metadata, section, strings.TrimPrefix(trimmedLine, "- "))
			continue
		}
		key, value, hasKey := strings.Cut(trimmedLine, ":")
		if !hasKey {
			if section == "description" {
				metadata.Description = joinSkillDescription(metadata.Description, cleanSkillScalar(trimmedLine))
			}
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if value == "" {
			section = key
			continue
		}
		metadata = setSkillMetadataValue(metadata, key, value)
		section = key
	}
	metadata.ToolReferences = uniqueTrimmedSkillValues(metadata.ToolReferences)
	return metadata
}

// Agent Skills keeps client-specific data under metadata, and allowed-tools
// carries another harness's vocabulary rather than this one's, so a skill that
// has to conform to the specification says which tools it needs here.
const vendorToolReferencesKey = "kim.intern.tool-references"

func setSkillMetadataValue(metadata skillMetadata, key string, value string) skillMetadata {
	switch key {
	case "name":
		metadata.Name = cleanSkillScalar(value)
	case "description":
		metadata.Description = joinSkillDescription(metadata.Description, cleanSkillScalar(value))
	case "tool-references", vendorToolReferencesKey:
		metadata.ToolReferences = append(metadata.ToolReferences, parseSkillToolReferences(value)...)
	}
	return metadata
}

func appendSkillToolReference(metadata skillMetadata, section string, value string) skillMetadata {
	toolReference := ToolReference(cleanSkillScalar(value))
	switch section {
	case "tool-references", vendorToolReferencesKey:
		metadata.ToolReferences = append(metadata.ToolReferences, toolReference)
	}
	return metadata
}

func parseSkillToolReferences(value string) []ToolReference {
	toolNames := parseSkillSpaceSeparatedList(value)
	toolReferences := make([]ToolReference, 0, len(toolNames))
	for _, toolName := range toolNames {
		toolReferences = append(toolReferences, ToolReference(toolName))
	}
	return toolReferences
}

func parseSkillList(value string) []string {
	trimmedValue := strings.TrimSpace(value)
	if strings.HasPrefix(trimmedValue, "[") && strings.HasSuffix(trimmedValue, "]") {
		trimmedValue = strings.TrimSuffix(strings.TrimPrefix(trimmedValue, "["), "]")
	}
	values := []string{}
	for _, part := range strings.Split(trimmedValue, ",") {
		cleanedPart := cleanSkillScalar(part)
		if cleanedPart != "" {
			values = append(values, cleanedPart)
		}
	}
	return values
}

func parseSkillSpaceSeparatedList(value string) []string {
	if strings.Contains(value, ",") || strings.HasPrefix(strings.TrimSpace(value), "[") {
		return parseSkillList(value)
	}
	values := []string{}
	for _, part := range strings.Fields(value) {
		cleanedPart := cleanSkillScalar(part)
		if cleanedPart != "" {
			values = append(values, cleanedPart)
		}
	}
	return values
}

func cleanSkillScalar(value string) string {
	return strings.Trim(strings.TrimSpace(value), `"'`)
}

func uniqueTrimmedSkillValues(values []ToolReference) []ToolReference {
	trimmedValues := []ToolReference{}
	seenValues := map[string]bool{}
	for _, value := range values {
		trimmedValue := strings.TrimSpace(string(value))
		if trimmedValue == "" || seenValues[trimmedValue] {
			continue
		}
		seenValues[trimmedValue] = true
		trimmedValues = append(trimmedValues, ToolReference(trimmedValue))
	}
	return trimmedValues
}

func firstMarkdownParagraph(document string) string {
	lines := []string{}
	for _, line := range strings.Split(document, "\n") {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			if len(lines) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(trimmedLine, "#") {
			continue
		}
		lines = append(lines, trimmedLine)
	}
	return strings.Join(lines, " ")
}

func joinSkillDescription(left string, right string) string {
	if strings.TrimSpace(left) == "" {
		return strings.TrimSpace(right)
	}
	if strings.TrimSpace(right) == "" {
		return strings.TrimSpace(left)
	}
	return strings.TrimSpace(left) + " " + strings.TrimSpace(right)
}
