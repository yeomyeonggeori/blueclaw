package app

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/persona"
	"github.com/yeomyeonggeori/blueclaw/internal/skill"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

// A skill states the tools it needs. One naming a tool this runtime was never
// offered cannot do what it says, and the agent would otherwise find out
// mid-task, so it is said once at start and carried in the skill inventory.
func logSkillsMissingTheirTools(logger *slog.Logger, unavailableSkills []skill.UnavailableSkill) {
	if logger == nil {
		return
	}
	for _, unavailableSkill := range unavailableSkills {
		if len(unavailableSkill.MissingToolNames) == 0 {
			continue
		}
		logger.Error("skill.tools.missing", "skill", unavailableSkill.Name, "missingTools", strings.Join(unavailableSkill.MissingToolNames, ", "), "path", unavailableSkill.Path)
	}
}

func logRejectedPersonaDocuments(logger *slog.Logger, rejectedDocuments []instructionDocument) {
	for _, rejectedDocument := range rejectedDocuments {
		logger.Warn("application.persona_document_rejected", "path", rejectedDocument.Source.Path, "error", rejectedDocument.Error.Error())
	}
}

func loadAgentInstructionPrompt(runtimeConfiguration config.RuntimeConfiguration) string {
	return loadAgentInstructionBundle(runtimeConfiguration).Prompt
}

// A skill naming an environment variable this host has not set is left out of
// the bundle: it is in the prompt only where it can run. The inventory carries
// it separately with the variable it lacks, so the omission is visible.
type agentInstructions struct {
	Bundle            agentcontract.InstructionBundle
	UnavailableSkills []skill.UnavailableSkill
	RejectedDocuments []instructionDocument
}

// What a skill may name: the tools a product's catalog offered this runtime, plus
// the ones the runtime and the kernel answer themselves.
func offeredToolNamesOf(runtimeConfiguration config.RuntimeConfiguration) []string {
	offeredToolNames := append([]string{}, toolcontract.KernelToolNames()...)
	offeredToolNames = append(offeredToolNames, agentruntime.LocalToolNames()...)
	for _, toolDescriptor := range runtimeConfiguration.Capabilities.ToolDescriptors {
		offeredToolNames = append(offeredToolNames, strings.TrimSpace(toolDescriptor.Name))
	}
	return offeredToolNames
}

func loadAgentInstructionBundle(runtimeConfiguration config.RuntimeConfiguration) agentcontract.InstructionBundle {
	return loadAgentInstructions(runtimeConfiguration).Bundle
}

func loadAgentInstructions(runtimeConfiguration config.RuntimeConfiguration) agentInstructions {
	parts := []string{}
	sources := []agentcontract.InstructionSource{}
	skillInstructions := []agentcontract.SkillInstruction{}
	unavailableSkills := []skill.UnavailableSkill{}
	rejectedDocuments := []instructionDocument{}
	includedSkillByName := map[string]bool{}
	offeredToolNames := offeredToolNamesOf(runtimeConfiguration)
	for _, rootPath := range instructionRootPaths(runtimeConfiguration) {
		for _, instructionDocument := range readInstructionDocuments(rootPath) {
			if instructionDocument.Error != nil {
				rejectedDocuments = append(rejectedDocuments, instructionDocument)
				continue
			}
			if instructionDocument.Prompt == "" {
				continue
			}
			parts = append(parts, instructionDocument.Prompt)
			sources = append(sources, instructionDocument.Source)
		}
		if instructionDocument, instructionSource := readLegacyInstructionDocument(rootPath); instructionDocument != "" {
			parts = append(parts, instructionDocument)
			sources = append(sources, instructionSource)
		}
		discovered := readSkillInstructions(rootPath, agentruntime.BundledSkillRootPath(rootPath), offeredToolNames)
		for _, skillInstruction := range discovered.Selectable {
			skillName := strings.TrimSpace(skillInstruction.Name)
			if includedSkillByName[skillName] {
				continue
			}
			if skillName != "" {
				includedSkillByName[skillName] = true
			}
			skillInstructions = append(skillInstructions, skillInstruction)
		}
		unavailableSkills = append(unavailableSkills, discovered.Unavailable...)
	}
	if !includedSkillByName["agent-browser"] {
		sources = append(sources, agentcontract.InstructionSource{
			Path:      ".agents/skills/agent-browser/SKILL.md",
			SkillName: "agent-browser",
			Missing:   true,
		})
	}
	return agentInstructions{
		Bundle: agentcontract.InstructionBundle{
			Prompt:  strings.Join(parts, "\n\n"),
			Sources: sources,
			Skills:  skillInstructions,
		},
		UnavailableSkills: uniqueUnavailableSkills(unavailableSkills, includedSkillByName),
		RejectedDocuments: rejectedDocuments,
	}
}

func uniqueUnavailableSkills(unavailableSkills []skill.UnavailableSkill, includedSkillByName map[string]bool) []skill.UnavailableSkill {
	listedSkills := []skill.UnavailableSkill{}
	listedSkillByName := map[string]bool{}
	for _, unavailableSkill := range unavailableSkills {
		skillName := strings.TrimSpace(unavailableSkill.Name)
		if includedSkillByName[skillName] || listedSkillByName[skillName] {
			continue
		}
		listedSkillByName[skillName] = true
		listedSkills = append(listedSkills, unavailableSkill)
	}
	return listedSkills
}

func instructionRootPaths(runtimeConfiguration config.RuntimeConfiguration) []string {
	rootPathByPath := map[string]bool{}
	rootPaths := []string{}
	for _, rootPath := range []string{runtimeConfiguration.Terminal.WorkspaceRootPath, "/workspace", "."} {
		cleanRootPath := strings.TrimSpace(rootPath)
		if cleanRootPath == "" || rootPathByPath[cleanRootPath] {
			continue
		}
		rootPathByPath[cleanRootPath] = true
		rootPaths = append(rootPaths, cleanRootPath)
	}
	return rootPaths
}

type instructionDocument struct {
	Prompt string
	Source agentcontract.InstructionSource
	Error  error
}

func readInstructionDocuments(rootPath string) []instructionDocument {
	documents := []instructionDocument{}
	if identityPath, document, identity, errorValue := readIdentityDocument(rootPath); document != nil {
		if errorValue != nil {
			documents = append(documents, instructionDocument{Prompt: "", Source: instructionSource(identityPath, "", document), Error: errorValue})
		} else {
			documents = append(documents, instructionDocument{Prompt: persona.RenderIdentityInstruction(identity), Source: instructionSource(identityPath, "", document)})
		}
	}
	soulPath := filepath.Join(rootPath, persona.SoulFileName)
	if document, errorValue := os.ReadFile(soulPath); errorValue == nil {
		soul, document, isRestored, parseError := persona.ParseWithBackup(persona.ParseSoul, document, persona.BackupPath(rootPath, persona.SoulFileName))
		if isRestored {
			restorePersonaDocument(soulPath, document)
		}
		if parseError != nil {
			documents = append(documents, instructionDocument{Source: instructionSource(soulPath, "", document), Error: parseError})
		} else {
			documents = append(documents, instructionDocument{Prompt: persona.RenderSoulInstruction(soul), Source: instructionSource(soulPath, "", document)})
		}
	}
	return documents
}

func readIdentityDocument(rootPath string) (string, []byte, persona.Identity, error) {
	identityPath := filepath.Join(rootPath, persona.IdentityFileName)
	document, errorValue := os.ReadFile(identityPath)
	if errorValue != nil {
		return identityPath, nil, persona.Identity{}, errorValue
	}
	identity, document, isRestored, errorValue := persona.ParseWithBackup(persona.ParseIdentity, document, persona.BackupPath(rootPath, persona.IdentityFileName))
	if isRestored {
		restorePersonaDocument(identityPath, document)
	}
	return identityPath, document, identity, errorValue
}

func restorePersonaDocument(livePath string, document []byte) {
	if errorValue := os.WriteFile(livePath, document, 0o644); errorValue != nil {
		slog.Warn("persona.document_restore_write_failed", "path", livePath, "error", errorValue.Error())
		return
	}
	slog.Warn("persona.document_restored_from_backup", "path", livePath)
}

func readLegacyInstructionDocument(rootPath string) (string, agentcontract.InstructionSource) {
	for _, fileName := range []string{"AGENTS.md", "CLAUDE.md"} {
		path := filepath.Join(rootPath, fileName)
		document, errorValue := os.ReadFile(path)
		if errorValue == nil && strings.TrimSpace(string(document)) != "" {
			return strings.TrimSpace(string(document)), instructionSource(path, "", document)
		}
	}
	return "", agentcontract.InstructionSource{}
}

type discoveredSkills struct {
	Selectable  []agentcontract.SkillInstruction
	Unavailable []skill.UnavailableSkill
}

func readSkillInstructions(rootPath string, bundledSkillsPath string, offeredToolNames []string) discoveredSkills {
	discovered := discoveredSkills{Selectable: []agentcontract.SkillInstruction{}, Unavailable: []skill.UnavailableSkill{}}
	skillRegistry := skill.NewSkillRegistry()
	for _, skillRoot := range []string{filepath.Join(rootPath, ".agents", "skills"), bundledSkillsPath} {
		discoveredSkillBundles, errorValue := skillRegistry.DiscoverSkill(skillRoot)
		if errorValue != nil {
			continue
		}
		for _, skillBundle := range discoveredSkillBundles {
			documentPath := filepath.Join(skillBundle.DirectoryPath, "SKILL.md")
			document, readError := os.ReadFile(documentPath)
			if readError != nil {
				continue
			}
			missingVariableNames := skillBundle.MissingEnvironmentVariables()
			missingToolNames := skillBundle.MissingToolNames(offeredToolNames)
			if len(missingVariableNames) > 0 || len(missingToolNames) > 0 {
				discovered.Unavailable = append(discovered.Unavailable, skill.UnavailableSkill{
					Name:                        skillBundle.Name,
					Description:                 skillBundle.Description,
					Path:                        documentPath,
					MissingEnvironmentVariables: missingVariableNames,
					MissingToolNames:            missingToolNames,
				})
				continue
			}
			discovered.Selectable = append(discovered.Selectable, agentcontract.SkillInstruction{
				Name:           skillBundle.Name,
				Description:    skillBundle.Description,
				Prompt:         strings.TrimSpace((skill.SkillPromptBuilder{}).BuildSkillPrompt([]skill.SkillBundle{skillBundle})),
				ToolReferences: skillBundle.ReferencedToolNames(),
				Source:         instructionSource(documentPath, skillBundle.Name, document),
			})
		}
	}
	return discovered
}

func loadAgentIdentity(runtimeConfiguration config.RuntimeConfiguration) agentcontract.AgentIdentity {
	for _, rootPath := range instructionRootPaths(runtimeConfiguration) {
		_, document, identity, errorValue := readIdentityDocument(rootPath)
		if document == nil || errorValue != nil {
			continue
		}
		return persona.AgentIdentityOf(identity)
	}
	return agentcontract.AgentIdentity{}
}

func instructionSource(path string, skillName string, document []byte) agentcontract.InstructionSource {
	hash := sha256.Sum256(document)
	return agentcontract.InstructionSource{
		Path:      path,
		SkillName: skillName,
		ByteSize:  len(document),
		SHA256:    hex.EncodeToString(hash[:]),
	}
}

func skillIndexPath(runtimeConfiguration config.RuntimeConfiguration) string {
	workspaceRootPath := firstNonEmptyString(runtimeConfiguration.Terminal.WorkspaceRootPath, "/workspace")
	return filepath.Join(workspaceRootPath, ".blueclaw", "skill-index.json")
}
