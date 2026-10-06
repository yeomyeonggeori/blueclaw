package acpharness

import (
	"context"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

const checkpointMarkerKey = "example.com/checkpoint"

func thoughtChunk(text string, meta map[string]any) acp.SessionUpdate {
	return acp.SessionUpdate{AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{Content: acp.TextBlock(text), Meta: meta}}
}

func routedCheckpoints(t *testing.T, markerKey string, update acp.SessionUpdate) []agentcontract.AgentCheckpoint {
	t.Helper()
	executed := []daemonExecutedTool{}
	harness := New(&inProcessAgentProcess{agent: &externalAgent{toolCallUpdates: []acp.SessionUpdate{update}}}, newPublishedToolCatalog(t), nil)
	harness.UseCheckpointMarker(markerKey)
	checkpoints := []agentcontract.AgentCheckpoint{}

	if _, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		ExistingTaskRunID: "run-7",
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
		CheckpointSender: func(_ context.Context, checkpoint agentcontract.AgentCheckpoint) error {
			checkpoints = append(checkpoints, checkpoint)
			return nil
		},
	}); errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}
	return checkpoints
}

func TestAThoughtChunkTheAgentMarkedAsACheckpointReachesTheCheckpointSender(t *testing.T) {
	checkpoints := routedCheckpoints(t, checkpointMarkerKey, thoughtChunk("노트를 남기는 중입니다", map[string]any{checkpointMarkerKey: map[string]any{"toolName": "note_write"}}))

	if len(checkpoints) != 1 || checkpoints[0].Message != "노트를 남기는 중입니다" || checkpoints[0].ToolName != "note_write" || checkpoints[0].TaskRunID != "run-7" {
		t.Fatalf("expected the checkpoint with its words, tool and run, got %+v", checkpoints)
	}
}

func TestAnUnmarkedThoughtChunkIsNotSaidToThePerson(t *testing.T) {
	checkpoints := routedCheckpoints(t, checkpointMarkerKey, thoughtChunk("음, 어디 보자", nil))

	if len(checkpoints) != 0 {
		t.Fatalf("a thought is not a progress message, got %+v", checkpoints)
	}
}

func TestAHarnessGivenNoMarkerSendsNoCheckpoints(t *testing.T) {
	checkpoints := routedCheckpoints(t, "", thoughtChunk("노트를 남기는 중입니다", map[string]any{checkpointMarkerKey: map[string]any{}}))

	if len(checkpoints) != 0 {
		t.Fatalf("without a marker nothing says which thoughts are checkpoints, got %+v", checkpoints)
	}
}
