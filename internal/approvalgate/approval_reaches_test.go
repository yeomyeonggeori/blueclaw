//go:build !nobundledharness

package approvalgate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/approval"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

func gatedCall(taskRunID string, toolName string, toolInput string) mcpserver.ApprovalRequest {
	return mcpserver.ApprovalRequest{
		RequesterPersonID: "person-1",
		TaskRunID:         taskRunID,
		ToolName:          toolName,
		ToolInput:         json.RawMessage(toolInput),
	}
}

func approveCall(t *testing.T, gate *Gate, taskRunService *task.TaskRunService, approvalRequest mcpserver.ApprovalRequest) {
	t.Helper()
	if outcome, _ := gate.AwaitApproval(context.Background(), approvalRequest); outcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("expected %s to be held for the requester, got %+v", approvalRequest.ToolName, outcome)
	}
	recordDecision(taskRunService, approvalRequest.TaskRunID, holdrecord.DecisionApprove)
	if outcome, _ := gate.AwaitApproval(context.Background(), approvalRequest); outcome.Decision != mcpserver.ApprovalDecisionApproved {
		t.Fatalf("expected the approved %s to run, got %+v", approvalRequest.ToolName, outcome)
	}
}

func TestApprovingABrowserCallOnOneOriginDoesNotLetACallOnAnotherThrough(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	approveCall(t, gate, taskRunService, gatedCall(taskRun.TaskRunID, "browser_open", `{"url":"https://a.example.com/form"}`))

	outcome, _ := gate.AwaitApproval(context.Background(), gatedCall(taskRun.TaskRunID, "browser_open", `{"url":"https://b.example.com/post"}`))

	if outcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("the requester approved a.example.com and was never shown b.example.com, got %+v", outcome)
	}
}

func TestApprovingABrowserCallOnOneOriginDoesNotLetAnotherCallOnItThrough(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	approveCall(t, gate, taskRunService, gatedCall(taskRun.TaskRunID, "browser_open", `{"url":"https://a.example.com/form"}`))

	outcome, _ := gate.AwaitApproval(context.Background(), gatedCall(taskRun.TaskRunID, "browser_open", `{"url":"https://a.example.com/delete-account"}`))

	if outcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("an approval answers the call it was asked about, not every call on the same site, got %+v", outcome)
	}
}

func TestSharingTheDataRoomWithOnePersonDoesNotShareItWithAnother(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	approveCall(t, gate, taskRunService, gatedCall(taskRun.TaskRunID, "dataroom_share_add", `{"circleID":"investors","email":"first@example.com"}`))

	outcome, _ := gate.AwaitApproval(context.Background(), gatedCall(taskRun.TaskRunID, "dataroom_share_add", `{"circleID":"investors","email":"second@example.com"}`))

	if outcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("the requester approved a share with first@example.com only, got %+v", outcome)
	}
}

func TestApprovingADataRoomLinkDoesNotApproveDeletingAChannel(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	approveCall(t, gate, taskRunService, gatedCall(taskRun.TaskRunID, "dataroom_link_add", `{"circleID":"investors","label":"Seed round","lifetimeHours":72,"canDownload":false}`))

	outcome, _ := gate.AwaitApproval(context.Background(), gatedCall(taskRun.TaskRunID, "circle_channel_delete", `{"circleID":"sales-team","name":"sales-team"}`))

	if outcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("approving a share link is no approval to delete a channel, got %+v", outcome)
	}
}

func TestAnApprovalDoesNotReachIntoAnotherTaskRun(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	approvedCall := gatedCall(taskRun.TaskRunID, "dataroom_share_add", `{"circleID":"investors","email":"first@example.com"}`)
	gate.AwaitApproval(context.Background(), approvedCall)
	recordDecision(taskRunService, taskRun.TaskRunID, holdrecord.DecisionApprove)
	laterTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "투자자에게 또 공유해줘")

	outcome, _ := gate.AwaitApproval(context.Background(), gatedCall(laterTaskRun.TaskRunID, "dataroom_share_add", `{"circleID":"investors","email":"first@example.com"}`))

	if outcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("an approval belongs to the run it was asked in, got %+v", outcome)
	}
}

func TestTheQuestionShowsTheCallItApprovesAndPromisesNothingAfterIt(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	languageModel := &wordingLanguageModel{question: "투자자 자료를 이 주소와 공유할까요?"}
	gate.UseQuestionWorder(approval.NewWorder(languageModel))

	gate.AwaitApproval(context.Background(), gatedCall(taskRun.TaskRunID, "dataroom_share_add", `{"circleID":"investors","email":"first@example.com"}`))

	question := heldCallEventBodyNamed(t, taskRunService, taskRun.TaskRunID, "confirmation.requested")
	for _, shownDetail := range []string{"circleID: investors", "email: first@example.com"} {
		if !strings.Contains(question, shownDetail) {
			t.Fatalf("the requester approves what is printed under the question, expected %q in %s", shownDetail, question)
		}
	}
	if wordingContext := marshalRequestMessages(languageModel.lastRequest); strings.Contains(wordingContext, `"approvalScope"`) {
		t.Fatalf("the model must not be handed a grant wider than this call to describe, got %s", wordingContext)
	}
}
