package agentruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBundledSkillRootFollowsTheShareWhenOneIsDelivered(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()

	if toolCatalogBuilder.bundledSkillRootPath() != filepath.Join("/workspace", "skills") {
		t.Fatalf("without a delivered path the bundled skills stay in the workspace, got %q", toolCatalogBuilder.bundledSkillRootPath())
	}

	t.Setenv("BLUECLAW_BUNDLED_SKILLS_PATH", "/delivery/skills")
	if toolCatalogBuilder.bundledSkillRootPath() != "/delivery/skills" {
		t.Fatalf("expected the delivered bundled skills, got %q", toolCatalogBuilder.bundledSkillRootPath())
	}
}

func TestBundledSkillsStayUnwritableWhereverTheyLive(t *testing.T) {
	deliveredRoot := t.TempDir()
	if errorValue := os.MkdirAll(filepath.Join(deliveredRoot, "paperwork"), 0o755); errorValue != nil {
		t.Fatalf("expected a delivered skill fixture: %v", errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(deliveredRoot, "paperwork", "SKILL.md"), []byte("# paperwork\n"), 0o644); errorValue != nil {
		t.Fatalf("expected a delivered skill document: %v", errorValue)
	}
	t.Setenv("BLUECLAW_BUNDLED_SKILLS_PATH", deliveredRoot)

	toolCatalogBuilder := NewToolCatalogBuilder()
	if !toolCatalogBuilder.isBundledSkillName("paperwork") {
		t.Fatal("a skill delivered on the share is bundled, so the agent may not overwrite it")
	}
	if errorValue := toolCatalogBuilder.validateManageableSkillName("paperwork"); errorValue == nil {
		t.Fatal("expected a delivered bundled skill to be refused for creation or removal")
	}
}
