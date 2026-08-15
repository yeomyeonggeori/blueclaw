package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/yeomyeonggeori/blueclaw/internal/skill"
)

const maximumSkillNameLength = 64
const weakDescriptionRuneCount = 40
const longSkillBodyLineCount = 500
const maximumSkillSearchPromptLength = 20000

var skillNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

var supportedSkillFrontmatterKeys = map[string]bool{
	"compatibility":   true,
	"description":     true,
	"license":         true,
	"metadata":        true,
	"name":            true,
	"tool-references": true,
}

type skillAddInput struct {
	Name      string               `json:"name"`
	Content   string               `json:"content"`
	Resources []skillResourceInput `json:"resources"`
}

type skillResourceInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    uint32 `json:"mode"`
}

type skillAddResult struct {
	Name          string   `json:"name"`
	Path          string   `json:"path"`
	Status        string   `json:"status"`
	Written       bool     `json:"written"`
	ResourcePaths []string `json:"resourcePaths"`
	Warnings      []string `json:"warnings"`
}

type skillRemoveResult struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Status  string `json:"status"`
	Removed bool   `json:"removed"`
}

var (
	skillAddInputSchema          = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":64,"pattern":"^[a-z0-9][a-z0-9-]{0,63}$"},"content":{"type":"string","minLength":1,"pattern":"\\S"},"resources":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string","minLength":1,"pattern":"\\S"},"content":{"type":"string"},"mode":{"type":"integer","minimum":0}},"required":["path","content"],"additionalProperties":false}}},"required":["name","content"],"additionalProperties":false}`)
	skillAddInputIntentSchema    = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":64,"pattern":"^[a-z0-9][a-z0-9-]{0,63}$"},"content":{"type":"string","minLength":1,"pattern":"\\S"},"resources":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string","minLength":1,"pattern":"\\S"},"content":{"type":"string"},"mode":{"type":"integer","minimum":0}},"additionalProperties":false}}},"additionalProperties":false}`)
	skillAddResultSchema         = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,"pattern":"\\S"},"path":{"type":"string","minLength":1,"pattern":"\\S"},"status":{"type":"string","enum":["created","updated"]},"written":{"const":true},"resourcePaths":{"type":"array","items":{"type":"string","minLength":1,"pattern":"\\S"},"uniqueItems":true},"warnings":{"type":"array","items":{"type":"string"}}},"required":["name","path","status","written","resourcePaths","warnings"],"additionalProperties":false}`)
	skillRemoveInputSchema       = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":64,"pattern":"^[a-z0-9][a-z0-9-]{0,63}$"}},"required":["name"],"additionalProperties":false}`)
	skillRemoveInputIntentSchema = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":64,"pattern":"^[a-z0-9][a-z0-9-]{0,63}$"}},"additionalProperties":false}`)
	skillRemoveResultSchema      = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,"pattern":"\\S"},"path":{"type":"string","minLength":1,"pattern":"\\S"},"status":{"const":"removed"},"removed":{"const":true}},"required":["name","path","status","removed"],"additionalProperties":false}`)
)

func (toolCatalogBuilder *ToolCatalogBuilder) registerSkillManagementTools(toolRegistry *toolcontract.ToolSet) {
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[skillAddInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        "skill_add",
			Description: "Create or update a user-managed SKILL.md under /workspace/.agents/skills/<name>.",
			InputSchema: skillAddInputSchema,
		},
		Handler: toolCatalogBuilder.addSkillTool,
		Result:  toolcontract.IdentityToolResult,
	})
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[skillRemoveInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        "skill_remove",
			Description: "Remove a user-managed skill under /workspace/.agents/skills/<name>.",
			InputSchema: skillRemoveInputSchema,
		},
		Handler: toolCatalogBuilder.removeSkillTool,
		Result:  toolcontract.IdentityToolResult,
	})
}

type skillRemoveInput struct {
	Name string `json:"name"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) addSkillTool(toolContext context.Context, input skillAddInput) (toolcontract.ToolResult, error) {
	if toolCatalogBuilder.isProductionServiceOwnedSkillWorkspace() {
		return toolcontract.ToolFailureResult(toolcontract.FailurePermissionDenied, toolcontract.FailureCodes.AccessDenied, "actor_permission_denied", "skill_add cannot modify the service-owned skill workspace; use a requester-writable skill workspace"), nil
	}
	skillName := strings.TrimSpace(input.Name)
	if errorValue := toolCatalogBuilder.validateManageableSkillName(skillName); errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "skill_add", errorValue.Error()), nil
	}
	skillBundle, warnings, errorValue := validateSkillDocument(skillName, input.Content)
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "skill_add", errorValue.Error()), nil
	}
	resourcePaths, errorValue := validateSkillResources(input.Resources)
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "skill_add", errorValue.Error()), nil
	}
	warnings = append(warnings, skillResourceWarnings(skillBundle.Instruction, resourcePaths)...)
	skillDirectoryPath := toolCatalogBuilder.userManagedSkillDirectoryPath(skillName)
	status := "updated"
	if _, errorValue := os.Stat(skillDirectoryPath); os.IsNotExist(errorValue) {
		status = "created"
	}
	if errorValue := os.MkdirAll(skillDirectoryPath, 0700); errorValue != nil {
		return toolcontract.ToolResult{}, errorValue
	}
	if errorValue := os.WriteFile(filepath.Join(skillDirectoryPath, "SKILL.md"), normalizedSkillDocument(input.Content), 0600); errorValue != nil {
		return toolcontract.ToolResult{}, errorValue
	}
	writtenResourcePaths, errorValue := toolCatalogBuilder.writeSkillResources(skillDirectoryPath, input.Resources)
	if errorValue != nil {
		return toolcontract.ToolResult{}, errorValue
	}
	toolCatalogBuilder.refreshSkills(toolContext)
	resultDocument := json.RawMessage(marshalToolResult(skillAddResult{
		Name:          skillName,
		Path:          toolCatalogBuilder.agentWorkspacePath(filepath.Join(skillDirectoryPath, "SKILL.md")),
		Status:        status,
		Written:       true,
		ResourcePaths: writtenResourcePaths,
		Warnings:      warnings,
	}))
	return toolcontract.ToolSuccessData(string(resultDocument), resultDocument), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) removeSkillTool(toolContext context.Context, input skillRemoveInput) (toolcontract.ToolResult, error) {
	if toolCatalogBuilder.isProductionServiceOwnedSkillWorkspace() {
		return toolcontract.ToolFailureResult(toolcontract.FailurePermissionDenied, toolcontract.FailureCodes.AccessDenied, "actor_permission_denied", "skill_remove cannot modify the service-owned skill workspace; use a requester-writable skill workspace"), nil
	}
	skillName := strings.TrimSpace(input.Name)
	if errorValue := toolCatalogBuilder.validateManageableSkillName(skillName); errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "skill_remove", errorValue.Error()), nil
	}
	skillDirectoryPath := toolCatalogBuilder.userManagedSkillDirectoryPath(skillName)
	if _, errorValue := os.Stat(skillDirectoryPath); os.IsNotExist(errorValue) {
		return toolcontract.ToolFailureData(toolcontract.FailureNotFound, toolcontract.FailureCodes.NotFound, "skill_remove", "user-managed skill was not found", json.RawMessage(marshalToolResult(map[string]string{
			"name":   skillName,
			"path":   toolCatalogBuilder.agentWorkspacePath(skillDirectoryPath),
			"status": "missing",
		}))), nil
	}
	if errorValue := os.RemoveAll(skillDirectoryPath); errorValue != nil {
		return toolcontract.ToolResult{}, errorValue
	}
	toolCatalogBuilder.refreshSkills(toolContext)
	resultDocument := json.RawMessage(marshalToolResult(skillRemoveResult{
		Name:    skillName,
		Path:    toolCatalogBuilder.agentWorkspacePath(skillDirectoryPath),
		Status:  "removed",
		Removed: true,
	}))
	return toolcontract.ToolSuccessData(string(resultDocument), resultDocument), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) userManagedSkillDirectoryPath(skillName string) string {
	return filepath.Join(toolCatalogBuilder.workspaceRootPath, ".agents", "skills", skillName)
}

func (toolCatalogBuilder *ToolCatalogBuilder) isBundledSkillName(skillName string) bool {
	_, errorValue := os.Stat(filepath.Join(toolCatalogBuilder.bundledSkillRootPath(), skillName, "SKILL.md"))
	return errorValue == nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) validateManageableSkillName(skillName string) error {
	if errorValue := validateUserManagedSkillName(skillName); errorValue != nil {
		return errorValue
	}
	if toolCatalogBuilder.isBundledSkillName(skillName) {
		return errors.New("bundled skills cannot be created, overwritten, or removed")
	}
	return nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) isProductionServiceOwnedSkillWorkspace() bool {
	return filepath.Clean(toolCatalogBuilder.workspaceRootPath) == "/workspace"
}

func (toolCatalogBuilder *ToolCatalogBuilder) refreshSkills(ctx context.Context) {
	if toolCatalogBuilder.skillChangeHandler != nil {
		toolCatalogBuilder.skillChangeHandler(ctx)
	}
}

func validateUserManagedSkillName(skillName string) error {
	if skillName == "" {
		return errors.New("skill name is required")
	}
	if len(skillName) > maximumSkillNameLength || !skillNamePattern.MatchString(skillName) {
		return errors.New("skill name must use lowercase letters, digits, and hyphens only, up to 64 characters")
	}
	return nil
}

func validateSkillDocument(skillName string, content string) (skill.SkillBundle, []string, error) {
	trimmedContent := strings.TrimSpace(content)
	if trimmedContent == "" {
		return skill.SkillBundle{}, nil, errors.New("skill content is required")
	}
	if errorValue := validateSkillFrontmatter(trimmedContent); errorValue != nil {
		return skill.SkillBundle{}, nil, errorValue
	}
	skillBundle, errorValue := loadSkillDocumentForValidation(skillName, trimmedContent)
	if errorValue != nil {
		return skill.SkillBundle{}, nil, errorValue
	}
	if skillBundle.Name != skillName {
		return skill.SkillBundle{}, nil, errors.New("skill document name must match the requested skill name")
	}
	if strings.TrimSpace(skillBundle.Description) == "" {
		return skill.SkillBundle{}, nil, errors.New("skill document must provide description or a first markdown paragraph")
	}
	if strings.TrimSpace(skillBundle.Instruction) == "" {
		return skill.SkillBundle{}, nil, errors.New("skill document body is required")
	}
	return skillBundle, skillQualityWarnings(skillBundle), nil
}

func validateSkillFrontmatter(content string) error {
	if !strings.HasPrefix(content, "---\n") {
		return errors.New("skill document must start with YAML frontmatter")
	}
	remainingContent := strings.TrimPrefix(content, "---\n")
	frontmatter, _, hasFrontmatter := strings.Cut(remainingContent, "\n---")
	if !hasFrontmatter {
		return errors.New("skill frontmatter is malformed")
	}
	for _, line := range strings.Split(frontmatter, "\n") {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" || strings.HasPrefix(trimmedLine, "- ") || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		key, _, hasKey := strings.Cut(trimmedLine, ":")
		if !hasKey || !supportedSkillFrontmatterKeys[strings.TrimSpace(key)] {
			return errors.New("skill frontmatter contains an unsupported field")
		}
	}
	return nil
}

func loadSkillDocumentForValidation(skillName string, content string) (skill.SkillBundle, error) {
	rootPath, errorValue := os.MkdirTemp("", "blueclaw-skill-validation-*")
	if errorValue != nil {
		return skill.SkillBundle{}, errorValue
	}
	defer os.RemoveAll(rootPath)
	skillDirectoryPath := filepath.Join(rootPath, skillName)
	if errorValue := os.MkdirAll(skillDirectoryPath, 0700); errorValue != nil {
		return skill.SkillBundle{}, errorValue
	}
	if errorValue := os.WriteFile(filepath.Join(skillDirectoryPath, "SKILL.md"), []byte(content), 0600); errorValue != nil {
		return skill.SkillBundle{}, errorValue
	}
	return (skill.SkillLoader{}).LoadSkillBundle(skillDirectoryPath)
}

func validateSkillResources(resources []skillResourceInput) ([]string, error) {
	resourcePaths := []string{}
	for _, resource := range resources {
		resourcePath, errorValue := validateSkillResourcePath(resource.Path)
		if errorValue != nil {
			return nil, errorValue
		}
		resourcePaths = append(resourcePaths, resourcePath)
	}
	return resourcePaths, nil
}

func validateSkillResourcePath(path string) (string, error) {
	trimmedPath := filepath.ToSlash(strings.TrimSpace(path))
	if trimmedPath == "" {
		return "", errors.New("skill resource path is required")
	}
	if filepath.IsAbs(trimmedPath) || strings.HasPrefix(trimmedPath, "/") {
		return "", errors.New("skill resource path must be relative")
	}
	cleanPath := filepath.ToSlash(filepath.Clean(trimmedPath))
	if cleanPath == "." || cleanPath == "SKILL.md" || cleanPath == "skill.md" {
		return "", errors.New("skill resource path cannot target SKILL.md")
	}
	if strings.HasPrefix(cleanPath, "../") || cleanPath == ".." {
		return "", errors.New("skill resource path cannot escape the skill directory")
	}
	if hasHiddenPathComponent(cleanPath) {
		return "", errors.New("skill resource path cannot contain hidden path components")
	}
	if !hasAllowedSkillResourcePrefix(cleanPath) {
		return "", errors.New("skill resource path must be under scripts, references, or assets")
	}
	return cleanPath, nil
}

func hasHiddenPathComponent(path string) bool {
	for _, component := range strings.Split(path, "/") {
		if strings.HasPrefix(component, ".") {
			return true
		}
	}
	return false
}

func hasAllowedSkillResourcePrefix(path string) bool {
	return strings.HasPrefix(path, "scripts/") ||
		strings.HasPrefix(path, "references/") ||
		strings.HasPrefix(path, "assets/")
}

func (toolCatalogBuilder *ToolCatalogBuilder) writeSkillResources(skillDirectoryPath string, resources []skillResourceInput) ([]string, error) {
	writtenResourcePaths := []string{}
	for _, resource := range resources {
		resourcePath, errorValue := validateSkillResourcePath(resource.Path)
		if errorValue != nil {
			return nil, errorValue
		}
		fileMode := os.FileMode(0600)
		if resource.Mode != 0 {
			fileMode = os.FileMode(resource.Mode)
		}
		resolvedPath := filepath.Join(skillDirectoryPath, filepath.FromSlash(resourcePath))
		if errorValue := os.MkdirAll(filepath.Dir(resolvedPath), 0700); errorValue != nil {
			return nil, errorValue
		}
		if errorValue := os.WriteFile(resolvedPath, []byte(resource.Content), fileMode); errorValue != nil {
			return nil, errorValue
		}
		writtenResourcePaths = append(writtenResourcePaths, toolCatalogBuilder.agentWorkspacePath(resolvedPath))
	}
	return writtenResourcePaths, nil
}

func skillQualityWarnings(skillBundle skill.SkillBundle) []string {
	warnings := []string{}
	if utf8.RuneCountInString(strings.TrimSpace(skillBundle.Description)) < weakDescriptionRuneCount {
		warnings = append(warnings, "description is short; include what the skill does and when to use it")
	}
	if len(strings.Split(strings.TrimSpace(skillBundle.Instruction), "\n")) > longSkillBodyLineCount {
		warnings = append(warnings, "skill body is long; move detailed material into references")
	}
	return warnings
}

func skillResourceWarnings(instruction string, resourcePaths []string) []string {
	warnings := []string{}
	normalizedInstruction := filepath.ToSlash(instruction)
	resourcePathByPrefix := resourcePathPrefixes(resourcePaths)
	if strings.Contains(normalizedInstruction, "references/") && !resourcePathByPrefix["references"] {
		warnings = append(warnings, "SKILL.md mentions references/ but no reference resources were supplied")
	}
	if strings.Contains(normalizedInstruction, "scripts/") && !resourcePathByPrefix["scripts"] {
		warnings = append(warnings, "SKILL.md mentions scripts/ but no script resources were supplied")
	}
	for _, resourcePath := range resourcePaths {
		if !strings.Contains(normalizedInstruction, resourcePath) {
			warnings = append(warnings, "resource "+resourcePath+" is not mentioned from SKILL.md")
		}
	}
	return warnings
}

func resourcePathPrefixes(resourcePaths []string) map[string]bool {
	prefixes := map[string]bool{}
	for _, resourcePath := range resourcePaths {
		prefix, _, _ := strings.Cut(resourcePath, "/")
		prefixes[prefix] = true
	}
	return prefixes
}

func normalizedSkillDocument(content string) []byte {
	return []byte(strings.TrimSpace(content) + "\n")
}

func (toolCatalogBuilder *ToolCatalogBuilder) bundledSkillRootPath() string {
	return BundledSkillRootPath(toolCatalogBuilder.workspaceRootPath)
}

// Bundled skills come from the host and the agent may not create, overwrite or remove
// one, so they can sit on a read-only share. The skills the agent writes stay in its own
// workspace. guest-init names the delivered path when the monitor offers a share.
func BundledSkillRootPath(workspaceRootPath string) string {
	if deliveredPath := strings.TrimSpace(os.Getenv("BLUECLAW_BUNDLED_SKILLS_PATH")); deliveredPath != "" {
		return deliveredPath
	}
	return filepath.Join(workspaceRootPath, "skills")
}
