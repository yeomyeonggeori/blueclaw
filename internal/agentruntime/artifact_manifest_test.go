package agentruntime

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

func TestArtifactManifestBoundsNewestFirstAndFiltersConversation(t *testing.T) {
	workspaceRootPath := t.TempDir()
	workspaceDefaultPath := filepath.Join(workspaceRootPath, "private", "people", "person-1")
	taskRunService := taskstate.NewTaskRunService(taskstate.NewTaskEventService())
	taskArtifactService := taskstate.NewTaskArtifactService()
	baseModifiedAt := time.Date(2026, time.June, 12, 1, 0, 0, 0, time.UTC)

	for index := 0; index < 12; index++ {
		artifactPath := createManifestTestArtifact(t, workspaceDefaultPath, "deck-"+twoDigitString(index), baseModifiedAt.Add(time.Duration(index)*time.Minute))
		taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "make deck")
		taskRunService.AppendTaskEvent(taskRun.TaskRunID, "tool.file_promote.result", `{"path":"artifacts/deck-`+twoDigitString(index)+`/`+filepath.Base(artifactPath)+`"}`)
	}

	createManifestTestArtifact(t, workspaceDefaultPath, "other", baseModifiedAt.Add(24*time.Hour))
	otherTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-2", "make other deck")
	taskRunService.AppendTaskEvent(otherTaskRun.TaskRunID, "tool.file_promote.result", `{"path":"artifacts/other/other.pptx"}`)

	manifest := buildConversationArtifactManifest(agentcontract.AgentTurnRequest{
		ConversationID:       "conversation-1",
		WorkspaceRootPath:    workspaceRootPath,
		WorkspaceDefaultPath: workspaceDefaultPath,
		ExistingTaskRunID:    "",
		ArtifactManifest:     nil,
		RequesterPersonID:    "person-1",
	}, taskRunService, taskArtifactService)

	if len(manifest) != 10 {
		t.Fatalf("expected ten manifest entries, got %+v", manifest)
	}
	if !strings.Contains(manifest[0].RelativePath, "deck-11") {
		t.Fatalf("expected newest artifact first, got %+v", manifest)
	}
	if !strings.Contains(manifest[9].RelativePath, "deck-02") {
		t.Fatalf("expected bounded newest entries through deck-02, got %+v", manifest)
	}
	for _, entry := range manifest {
		if strings.Contains(entry.RelativePath, "other") || strings.Contains(entry.RelativePath, "deck-00") || strings.Contains(entry.RelativePath, "deck-01") {
			t.Fatalf("expected conversation-filtered newest entries, got %+v", manifest)
		}
	}
}

func TestArtifactManifestIncludesTaskArtifactLedgerPath(t *testing.T) {
	workspaceRootPath := t.TempDir()
	workspaceDefaultPath := filepath.Join(workspaceRootPath, "private", "people", "person-1")
	taskRunService := taskstate.NewTaskRunService(taskstate.NewTaskEventService())
	taskArtifactService := taskstate.NewTaskArtifactService()
	modifiedAt := time.Date(2026, time.June, 12, 2, 0, 0, 0, time.UTC)
	createManifestTestArtifact(t, workspaceDefaultPath, "ledger", modifiedAt)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "make deck")
	taskArtifactService.AddTaskArtifactBody(taskRun.TaskRunID, "tool.file_promote.result", `{"path":"artifacts/ledger/ledger.pptx"}`)

	manifest := buildConversationArtifactManifest(agentcontract.AgentTurnRequest{
		ConversationID:       "conversation-1",
		WorkspaceRootPath:    workspaceRootPath,
		WorkspaceDefaultPath: workspaceDefaultPath,
	}, taskRunService, taskArtifactService)

	if len(manifest) != 1 {
		t.Fatalf("expected one ledger manifest entry, got %+v", manifest)
	}
	if manifest[0].ProducingTool != "file_promote" || !strings.Contains(manifest[0].RelativePath, "ledger.pptx") {
		t.Fatalf("expected ledger artifact path and producing tool, got %+v", manifest)
	}
}

func createManifestTestArtifact(t *testing.T, workspaceDefaultPath string, slug string, modifiedAt time.Time) string {
	t.Helper()
	artifactDirectoryPath := filepath.Join(workspaceDefaultPath, "artifacts", slug)
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatalf("failed to create artifact directory: %v", errorValue)
	}
	artifactPath := filepath.Join(artifactDirectoryPath, slug+".pptx")
	if errorValue := os.WriteFile(artifactPath, []byte("pptx"), 0600); errorValue != nil {
		t.Fatalf("failed to write artifact: %v", errorValue)
	}
	if errorValue := os.Chtimes(artifactPath, modifiedAt, modifiedAt); errorValue != nil {
		t.Fatalf("failed to change artifact time: %v", errorValue)
	}
	return artifactPath
}

func twoDigitString(index int) string {
	if index < 10 {
		return "0" + strconv.Itoa(index)
	}
	return strconv.Itoa(index)
}
