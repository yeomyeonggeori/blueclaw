//go:build !nobundledharness

package approvalgate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/approval"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func TestTheWordingIsHandedTheInputsTheToolDeclaresAndTheModelsDraft(t *testing.T) {
	gate, _, taskRun := gateFixture(t)
	languageModel := &wordingLanguageModel{question: "명령을 실행할까요?"}
	gate.UseQuestionWorder(approval.NewWorder(languageModel))
	request := approvalRequestFixture(taskRun.TaskRunID)
	request.ToolName = "bash"
	request.ToolInput = json.RawMessage(`{"command":"rm -r build","approvalReason":"빌드를 다시 하기 위해"}`)
	request.ApprovalInputFields = []string{"command"}
	request.ModelDraft = "build 폴더를 지울까요?"

	gate.AwaitApproval(context.Background(), request)

	wordingContext := marshalRequestMessages(languageModel.lastRequest)
	if strings.Contains(wordingContext, "approvalReason") || !strings.Contains(wordingContext, "rm -r build") || !strings.Contains(wordingContext, "build 폴더를 지울까요?") {
		t.Fatalf("expected the declared input and the draft to reach the wording and the reason to stay out, got %s", wordingContext)
	}
}

func TestTheTurnGateHandsTheToolsApprovalFactsToTheWording(t *testing.T) {
	gate, _, taskRun := gateFixture(t)
	languageModel := &wordingLanguageModel{question: "삭제할까요?"}
	gate.UseQuestionWorder(approval.NewWorder(languageModel))
	toolSet := toolcontract.NewToolSet([]string{"file_delete"})
	toolSet.AllowTestReplacement()
	registerDeletingTool(t, toolSet)
	toolSet.UseToolCallGate(gate.TurnGate(TurnContext{RequesterPersonID: "person-1"}))

	toolSet.Invoke(toolcontract.WithTaskRunID(context.Background(), taskRun.TaskRunID), toolcontract.ToolInvocation{ToolName: "file_delete", Input: json.RawMessage(`{"path":"~/a.md","note":"x"}`)})

	wordingContext := marshalRequestMessages(languageModel.lastRequest)
	for _, expectedFragment := range []string{`"covers":"every file in the workspace"`, `"path":"~/a.md"`} {
		if !strings.Contains(wordingContext, expectedFragment) {
			t.Fatalf("expected %q in %s", expectedFragment, wordingContext)
		}
	}
	if strings.Contains(wordingContext, `"note"`) {
		t.Fatalf("an input the tool did not declare is not what the action does, got %s", wordingContext)
	}
}

func registerDeletingTool(t *testing.T, toolSet *toolcontract.ToolSet) {
	t.Helper()
	errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
		ID:                   "test:file_delete",
		Name:                 "file_delete",
		Description:          "file_delete",
		Visibility:           toolcontract.ToolVisibilityModel,
		InputSchema:          json.RawMessage(`{"type":"object"}`),
		RequiresApproval:     true,
		ApprovalScope:        "workspace_files",
		ApprovalScopeSummary: "every file in the workspace",
		ApprovalInputFields:  []string{"path"},
		SideEffectClass:      toolcontract.ToolSideEffectStateChange,
		ResultContract:       &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object"}`)},
	}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolSuccessData("done", json.RawMessage(`{}`)), nil
	})
	if errorValue != nil {
		t.Fatalf("expected the tool to register: %v", errorValue)
	}
}

func TestTheChoicesATargetCarriesAreHandedToTheWording(t *testing.T) {
	gate, _, taskRun, _, _ := choiceGate(t, ApprovalAnswer{})
	languageModel := &wordingLanguageModel{question: "지금 업데이트할까요?"}
	gate.UseQuestionWorder(approval.NewWorder(languageModel))

	gate.AwaitApproval(context.Background(), hostUpdateRequest(taskRun.TaskRunID))

	wordingContext := marshalRequestMessages(languageModel.lastRequest)
	if !strings.Contains(wordingContext, `"key":"offHours"`) || !strings.Contains(wordingContext, laterStartsAt) {
		t.Fatalf("the question offers exactly the choices the host computed, got %s", wordingContext)
	}
}
