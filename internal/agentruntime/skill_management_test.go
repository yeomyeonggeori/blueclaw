package agentruntime

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestSkillSearchToolUsesSharedRetriever(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseSkillSearch(skillSearchTestRetriever{}, func() agentcontract.InstructionBundle {
		return agentcontract.InstructionBundle{Skills: []agentcontract.SkillInstruction{{
			Name:           "mail",
			Description:    "Read, search, summarize, reply to, and send email messages.",
			ToolReferences: []string{"file_read"},
		}}}
	})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "skill_search",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"queries": []map[string]string{{"description": "Search and read recent email messages."}},
			"limit":   5,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected skill_search success, got %s", result.ContentText())
	}
	var resultDocument skillSearchToolOutput
	if errorValue := json.Unmarshal([]byte(result.ContentText()), &resultDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	if resultDocument.Mode != skillSearchModeSearch || len(resultDocument.Skills) != 1 || resultDocument.Skills[0].Name != "mail" {
		t.Fatalf("expected mail skill result, got %+v", resultDocument)
	}
	if !containsTestString(resultDocument.Skills[0].ToolReferences, "file_read") {
		t.Fatalf("expected referenced operations in result, got %+v", resultDocument.Skills[0].ToolReferences)
	}
}

func TestSkillSearchToolExactNameIncludesToolReferences(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseSkillSearch(skillSearchTestRetriever{}, func() agentcontract.InstructionBundle {
		return agentcontract.InstructionBundle{Skills: []agentcontract.SkillInstruction{{
			Name:           "site-prototype",
			Description:    "Create sites.",
			ToolReferences: []string{"file_read"},
			Source:         agentcontract.InstructionSource{Path: "skills/site-prototype/SKILL.md"},
		}}}
	})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "skill_search",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"queries": []map[string]string{{"description": "site-prototype"}},
			"limit":   5,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if strings.Contains(result.ContentText(), `"sourcePath"`) || strings.Contains(result.ContentText(), `"score"`) || !strings.Contains(result.ContentText(), "file_read") {
		t.Fatalf("expected exact skill metadata, got %s", result.ContentText())
	}
}

func TestSkillAddCreatesUserManagedSkillAndRefreshes(t *testing.T) {
	workspacePath := t.TempDir()
	refreshCount := 0
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseSkillChangeHandler(func(context.Context) {
		refreshCount++
	})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "skill_add",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"name":    "research-helper",
			"content": userSkillDocument("research-helper"),
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected skill_add success, got %s", result.ContentText())
	}
	if len(result.Output.Data) == 0 {
		t.Fatal("expected structured skill_add result data")
	}
	if refreshCount != 1 {
		t.Fatalf("expected skill index refresh, got %d", refreshCount)
	}
	skillDocumentPath := filepath.Join(workspacePath, ".agents", "skills", "research-helper", "SKILL.md")
	document, errorValue := os.ReadFile(skillDocumentPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.Contains(string(document), "Research helper handles source lookups.") {
		t.Fatalf("expected skill document to be written, got %s", string(document))
	}
	if strings.Contains(result.ContentText(), workspacePath) || !strings.Contains(result.ContentText(), "/workspace/.agents/skills/research-helper/SKILL.md") {
		t.Fatalf("expected agent workspace path in result, got %s", result.ContentText())
	}
	resultDocument := decodeSkillAddResult(t, result.ContentText())
	if resultDocument.Name != "research-helper" || resultDocument.Status != "created" {
		t.Fatalf("expected structured skill_add result, got %+v", resultDocument)
	}
}

func TestSkillAddWritesAllowedResources(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	content := `---
name: report-helper
description: Help create reports from source material when the user asks for report writing.
---
Use references/reporting.md and scripts/build_report.sh when needed.
`

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "skill_add",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"name":    "report-helper",
			"content": content,
			"resources": []map[string]any{
				{"path": "references/reporting.md", "content": "# Reporting"},
				{"path": "scripts/build_report.sh", "content": "echo ok", "mode": 0o700},
				{"path": "assets/template.txt", "content": "template"},
			},
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected skill_add success, got %s", result.ContentText())
	}
	for _, path := range []string{"references/reporting.md", "scripts/build_report.sh", "assets/template.txt"} {
		if _, errorValue := os.Stat(filepath.Join(workspacePath, ".agents", "skills", "report-helper", path)); errorValue != nil {
			t.Fatalf("expected resource %s: %v", path, errorValue)
		}
	}
	resultDocument := decodeSkillAddResult(t, result.ContentText())
	if len(resultDocument.ResourcePaths) != 3 {
		t.Fatalf("expected resource paths in result, got %+v", resultDocument)
	}
}

func TestSkillRemoveDeletesOnlyUserManagedSkill(t *testing.T) {
	workspacePath := t.TempDir()
	skillDirectoryPath := filepath.Join(workspacePath, ".agents", "skills", "research-helper")
	if errorValue := os.MkdirAll(skillDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(skillDirectoryPath, "SKILL.md"), userSkillDocument("research-helper"))
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "skill_remove",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"name": "research-helper",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected skill_remove success, got %s", result.ContentText())
	}
	if len(result.Output.Data) == 0 {
		t.Fatal("expected structured skill_remove result data")
	}
	if _, errorValue := os.Stat(skillDirectoryPath); !os.IsNotExist(errorValue) {
		t.Fatalf("expected user-managed skill directory removed, got %v", errorValue)
	}
}

func TestSkillRemoveMissingSkillFails(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "skill_remove",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"name": "missing-skill",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureCode() != toolcontract.FailureCodes.NotFound.String() || !strings.Contains(string(result.Output.Data), `"status":"missing"`) {
		t.Fatalf("expected not found missing result, got %+v", result)
	}
}

func TestSkillManagementRejectsInvalidAndBundledNames(t *testing.T) {
	workspacePath := t.TempDir()
	for _, bundledSkillName := range []string{"presentation", "agent-browser"} {
		bundledSkillDirectoryPath := filepath.Join(workspacePath, "skills", bundledSkillName)
		if errorValue := os.MkdirAll(bundledSkillDirectoryPath, 0700); errorValue != nil {
			t.Fatal(errorValue)
		}
		if errorValue := os.WriteFile(filepath.Join(bundledSkillDirectoryPath, "SKILL.md"), []byte(userSkillDocument(bundledSkillName)), 0600); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	for _, input := range []map[string]string{
		{"name": "../escape", "content": userSkillDocument("escape")},
		{"name": "presentation", "content": userSkillDocument("presentation")},
	} {
		result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
			ToolName: "skill_add",
			Input:    toolcontract.MarshalToolInput(input),
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if !result.Failed() {
			t.Fatalf("expected skill_add to reject %+v", input)
		}
	}

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "skill_remove",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"name": "agent-browser",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected skill_remove to reject bundled skill, got %+v", result)
	}
}

func TestSkillAddRejectsMalformedOrCustomFrontmatter(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	for _, content := range []string{
		"---\nname: broken\ndescription: Broken",
		"---\nname: custom\nsummary: no\n---\nBody.",
		"---\nname: custom\ntags: [one]\n---\nBody.",
		"---\nname: custom\ntriggerHints: [one]\n---\nBody.",
		"---\nname: custom\ncustomToolDependency: [shell]\n---\nBody.",
		"---\nname: custom\nallowedProfiles: [default]\n---\nBody.",
		"---\nname: custom\nwhen_to_use: Use for custom work.\n---\nBody.",
		"---\nname: custom\nactivation: {}\n---\nBody.",
		"---\nname: custom\ncompletion: {}\n---\nBody.",
		"---\nname: custom\nrecommendedMinutes: 10\n---\nBody.",
		"---\nname: custom\nartifacts: {}\n---\nBody.",
	} {
		result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
			ToolName: "skill_add",
			Input: toolcontract.MarshalToolInput(map[string]string{
				"name":    "broken",
				"content": content,
			}),
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if !result.Failed() {
			t.Fatalf("expected malformed skill document to be rejected: %s", content)
		}
	}
}

func TestSkillAddAcceptsSupportedOptionalMetadataAndToolReferences(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	content := `---
name: metadata-helper
description: Help with metadata-backed standard skill imports.
license: MIT
metadata:
  category: productivity
  locale: ko-KR
tool-references: file_read
---
Use this skill when standard skill metadata should be preserved.
`

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "skill_add",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"name":    "metadata-helper",
			"content": content,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected standard optional metadata to be accepted, got %s", result.ContentText())
	}
}

func TestSkillAddRejectsInvalidResourcePaths(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	for _, resourcePath := range []string{
		"../escape.md",
		"/workspace/escape.md",
		"SKILL.md",
		".hidden/file.md",
		"notes/file.md",
	} {
		result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
			ToolName: "skill_add",
			Input: toolcontract.MarshalToolInput(map[string]any{
				"name":    "resource-helper",
				"content": userSkillDocument("resource-helper"),
				"resources": []map[string]string{{
					"path":    resourcePath,
					"content": "no",
				}},
			}),
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if !result.Failed() {
			t.Fatalf("expected resource path %q to be rejected", resourcePath)
		}
	}
}

func TestSkillAddReturnsQualityWarnings(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	content := `---
name: tiny-helper
description: Tiny.
---
Use references/missing.md.
`

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "skill_add",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"name":    "tiny-helper",
			"content": content,
			"resources": []map[string]string{{
				"path":    "assets/unmentioned.txt",
				"content": "asset",
			}},
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected warning-only skill_add success, got %s", result.ContentText())
	}
	resultDocument := decodeSkillAddResult(t, result.ContentText())
	for _, expectedWarning := range []string{
		"description is short; include what the skill does and when to use it",
		"SKILL.md mentions references/ but no reference resources were supplied",
		"resource assets/unmentioned.txt is not mentioned from SKILL.md",
	} {
		if !containsTestString(resultDocument.Warnings, expectedWarning) {
			t.Fatalf("expected warning %q, got %+v", expectedWarning, resultDocument.Warnings)
		}
	}
}

func TestSkillAddReturnsLongBodyAndMissingScriptWarnings(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	content := `---
name: long-helper
description: Help with long deterministic workflows when the user asks for a repeatable workflow.
---
Use scripts/missing.sh when needed.
` + strings.Repeat("step\n", longSkillBodyLineCount+1)

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "skill_add",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"name":    "long-helper",
			"content": content,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected warning-only skill_add success, got %s", result.ContentText())
	}
	resultDocument := decodeSkillAddResult(t, result.ContentText())
	for _, expectedWarning := range []string{
		"skill body is long; move detailed material into references",
		"SKILL.md mentions scripts/ but no script resources were supplied",
	} {
		if !containsTestString(resultDocument.Warnings, expectedWarning) {
			t.Fatalf("expected warning %q, got %+v", expectedWarning, resultDocument.Warnings)
		}
	}
}

func TestSkillManagementRejectsProductionServiceOwnedWorkspace(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath("/workspace")
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"skill_add"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "skill_add",
		Input: toolcontract.MarshalToolInput(map[string]string{
			"name":    "demo-skill",
			"content": "---\nname: demo-skill\ndescription: Demo skill for rejection testing.\n---\n# Demo\n",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.Failure.Stage != "actor_permission_denied" {
		t.Fatalf("expected actor_permission_denied for production skill_add, got %+v", result)
	}
}

func TestSkillSearchToolListsAllSkillsWithoutQueries(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseSkillSearch(skillSearchTestRetriever{}, func() agentcontract.InstructionBundle {
		return agentcontract.InstructionBundle{Skills: []agentcontract.SkillInstruction{
			{Name: "mail", Description: "Email skill.", ToolReferences: []string{"file_read"}},
			{Name: "site-prototype", Description: "Create sites.", ToolReferences: []string{"file_read"}},
		}}
	})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "skill_search",
		Input:    toolcontract.MarshalToolInput(map[string]any{}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected skill_search success, got %s", result.ContentText())
	}
	var resultDocument skillSearchToolOutput
	if errorValue := json.Unmarshal([]byte(result.ContentText()), &resultDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	if resultDocument.Mode != skillSearchModeList || resultDocument.TotalCount != 2 || len(resultDocument.Skills) != 2 || resultDocument.Skills[0].Name != "mail" || resultDocument.Skills[1].Name != "site-prototype" {
		t.Fatalf("expected full skill roster, got %+v", resultDocument.Skills)
	}
}

func TestSkillSearchToolNameLookupReturnsPromptBody(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseSkillSearch(skillSearchTestRetriever{}, func() agentcontract.InstructionBundle {
		return agentcontract.InstructionBundle{Skills: []agentcontract.SkillInstruction{{
			Name:           "site-prototype",
			Description:    "Create sites.",
			Prompt:         "Build the site, verify it, and attach promoted outputs.",
			ToolReferences: []string{"file_read"},
			Source:         agentcontract.InstructionSource{Path: "skills/site-prototype/SKILL.md"},
		}}}
	})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "skill_search",
		Input:    toolcontract.MarshalToolInput(map[string]any{"name": "SITE-PROTOTYPE"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected skill_search success, got %s", result.ContentText())
	}
	var resultDocument skillSearchToolOutput
	if errorValue := json.Unmarshal([]byte(result.ContentText()), &resultDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	if resultDocument.Mode != skillSearchModeName || len(resultDocument.Skills) != 1 || resultDocument.Skills[0].Prompt != "Build the site, verify it, and attach promoted outputs." {
		t.Fatalf("expected prompt body in name lookup, got %+v", resultDocument.Skills)
	}
	if resultDocument.Skills[0].SourcePath != "/workspace/skills/site-prototype/SKILL.md" {
		t.Fatalf("expected source path, got %+v", resultDocument.Skills[0])
	}
}
