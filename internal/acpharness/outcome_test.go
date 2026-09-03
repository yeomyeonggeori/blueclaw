package acpharness

import (
	"context"
	"errors"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/turnoutcome"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type stubOutcomeLanguageModel struct {
	responseContent string
	errorValue      error
}

func (stub stubOutcomeLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("not used")
}

func (stub stubOutcomeLanguageModel) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	return model.StructuredResponse{Content: stub.responseContent}, stub.errorValue
}

func runTurnWithOutcomeClassifier(t *testing.T, classifierModel model.LanguageModelProvider) agentcontract.AgentTurnResult {
	t.Helper()
	executed := []daemonExecutedTool{}
	agent := &externalAgent{toolNameToCall: "note_write", toolArguments: map[string]any{"text": "회의록"}}
	harness := New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), nil)
	harness.UseOutcomeClassifier(turnoutcome.NewClassifier(classifierModel))

	turnResult, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	})
	if errorValue != nil {
		t.Fatalf("expected the external agent turn to run: %v", errorValue)
	}
	return turnResult
}

func TestAnAgentThatEndsItsTurnReportingFailureIsNotRecordedAsCompleted(t *testing.T) {
	turnResult := runTurnWithOutcomeClassifier(t, stubOutcomeLanguageModel{
		responseContent: `{"outcome":"failed","reason":"the agent reported it could not do the work"}`,
	})

	if turnResult.TaskRun.Status != agentcontract.TaskStatusFailed {
		t.Fatalf("an ended turn is not a finished task; expected failed, got %q", turnResult.TaskRun.Status)
	}
	if turnResult.TaskRun.FailureReason == "" {
		t.Fatal("expected the ledger to record why the task failed")
	}
}

func TestAnUndecidableOutcomeIsRecordedAsFailedRatherThanCompleted(t *testing.T) {
	turnResult := runTurnWithOutcomeClassifier(t, stubOutcomeLanguageModel{errorValue: errors.New("the classifier is unreachable")})

	if turnResult.TaskRun.Status != agentcontract.TaskStatusFailed {
		t.Fatalf("expected an undecidable turn to be visible as failed, got %q", turnResult.TaskRun.Status)
	}
}

func TestAnAgentThatReportsSuccessIsRecordedAsCompleted(t *testing.T) {
	turnResult := runTurnWithOutcomeClassifier(t, stubOutcomeLanguageModel{
		responseContent: `{"outcome":"completed","reason":"the note was written"}`,
	})

	if turnResult.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("expected completed, got %q", turnResult.TaskRun.Status)
	}
}
