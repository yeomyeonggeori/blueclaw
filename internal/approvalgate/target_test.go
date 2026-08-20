package approvalgate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type recordingTargetResolver struct {
	resolution      ApprovalTargetResolution
	failure         error
	receivedRequest ApprovalTargetRequest
	callCount       int
}

func (resolver *recordingTargetResolver) ResolveApprovalTarget(_ context.Context, request ApprovalTargetRequest) (ApprovalTargetResolution, error) {
	resolver.receivedRequest = request
	resolver.callCount++
	if resolver.failure != nil {
		return ApprovalTargetResolution{}, resolver.failure
	}
	return resolver.resolution, nil
}

func resolvedTargetResolver() *recordingTargetResolver {
	return &recordingTargetResolver{resolution: ApprovalTargetResolution{Target: ApprovalTarget{
		InputField: "eventHint",
		ID:         "event-1",
		Title:      "상하이 edatec 미팅",
		StartsAt:   "2026-08-18T14:00:00+09:00",
	}}}
}

func unresolvedTargetResolver() *recordingTargetResolver {
	return &recordingTargetResolver{resolution: ApprovalTargetResolution{
		Failure: toolcontract.ToolFailureWithOutput(
			toolcontract.FailureNotFound,
			toolcontract.FailureCodes.NotFound,
			"target_resolution",
			"no calendar event matched eventHint; the candidates list what exists, so use one of them or take another route",
			json.RawMessage(`{"errorCode":"calendar_event_hint_unresolved","candidates":[{"eventID":"event-1","title":"상하이 edatec 미팅"}]}`),
		),
	}}
}

func hintApprovalRequest(taskRunID string) mcpserver.ApprovalRequest {
	approvalRequest := approvalRequestFixture(taskRunID)
	approvalRequest.ToolInput = json.RawMessage(`{"eventHint":"NVIDIA·젯슨 공급 미팅"}`)
	approvalRequest.RequesterEmail = "staff@example.com"
	return approvalRequest
}

func TestAnApprovalQuestionNamesTheTargetTheHintResolvedTo(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	languageModel := &wordingLanguageModel{question: "상하이 edatec 미팅 일정을 삭제할까요?"}
	gate.UseLanguageModel(languageModel)
	gate.UseApprovalTargetResolver(resolvedTargetResolver())

	outcome, errorValue := gate.AwaitApproval(context.Background(), hintApprovalRequest(taskRun.TaskRunID))
	if errorValue != nil {
		t.Fatalf("expected the gate to answer: %v", errorValue)
	}
	if outcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("a resolved target is still a destructive call the requester decides on, got %+v", outcome)
	}

	wordingContext := marshalRequestMessages(languageModel.lastRequest)
	for _, expectedFragment := range []string{"상하이 edatec 미팅", "2026-08-18T14:00:00+09:00"} {
		if !strings.Contains(wordingContext, expectedFragment) {
			t.Fatalf("the question is worded from the resolved target, expected %q in %s", expectedFragment, wordingContext)
		}
	}
	if !strings.Contains(heldCallEventBodyNamed(t, taskRunService, taskRun.TaskRunID, "confirmation.requested"), "상하이 edatec 미팅 일정을 삭제할까요?") {
		t.Fatal("the requester is asked about the entity the hint resolved to")
	}
}

func TestTheApprovalQuestionDoesNotRepeatTheSearchPhraseWhenATargetResolved(t *testing.T) {
	gate, _, taskRun := gateFixture(t)
	languageModel := &wordingLanguageModel{question: "상하이 edatec 미팅 일정을 삭제할까요?"}
	gate.UseLanguageModel(languageModel)
	gate.UseApprovalTargetResolver(resolvedTargetResolver())

	gate.AwaitApproval(context.Background(), hintApprovalRequest(taskRun.TaskRunID))

	if wordingContext := marshalRequestMessages(languageModel.lastRequest); strings.Contains(wordingContext, "NVIDIA·젯슨 공급 미팅") {
		t.Fatalf("the caller's search phrase names nothing once a target resolved, got %s", wordingContext)
	}
}

func TestAHintThatResolvesToNothingIsReportedToTheAgentInsteadOfAskedAbout(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UseLanguageModel(&wordingLanguageModel{question: "삭제할까요?"})
	gate.UseApprovalTargetResolver(unresolvedTargetResolver())

	outcome, errorValue := gate.AwaitApproval(context.Background(), hintApprovalRequest(taskRun.TaskRunID))
	if errorValue != nil {
		t.Fatalf("expected the gate to answer: %v", errorValue)
	}
	if outcome.Decision != mcpserver.ApprovalDecisionUnresolvedTarget {
		t.Fatalf("a hint that denotes nothing is not a destructive action to approve, got %+v", outcome)
	}
	if outcome.Failure.Failure == nil || !strings.Contains(outcome.Failure.Failure.UserSafeSummary, "no calendar event matched eventHint") {
		t.Fatalf("the agent gets the capability's own failure back, got %+v", outcome.Failure)
	}
	if !strings.Contains(string(outcome.Failure.Output.Data), "candidates") {
		t.Fatalf("the candidates are what let the agent take another route, got %s", outcome.Failure.Output.Data)
	}
	for _, questionEventName := range []string{"approval.pending_call", "confirmation.requested", "ask.requested"} {
		if hasTaskEventNamed(taskRunService, taskRun.TaskRunID, questionEventName) {
			t.Fatalf("nobody is asked about a target that does not exist, got %+v", taskEventNames(taskRunService, taskRun.TaskRunID))
		}
	}
}

func TestAnUnresolvedTargetLeavesTheTaskRunning(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UseApprovalTargetResolver(unresolvedTargetResolver())

	gate.AwaitApproval(context.Background(), hintApprovalRequest(taskRun.TaskRunID))

	pausedTaskRun, _ := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if pausedTaskRun.Status == task.TaskStatusWaitingApproval {
		t.Fatal("a task with nothing to approve keeps working instead of waiting for an answer nobody was asked for")
	}
}

func TestTheHeldCallCarriesBothTheRawHintAndTheIdentityItResolvedTo(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UseApprovalTargetResolver(resolvedTargetResolver())

	gate.AwaitApproval(context.Background(), hintApprovalRequest(taskRun.TaskRunID))

	heldCallBody := heldCallEventBody(t, taskRunService, taskRun.TaskRunID)
	for _, expectedFragment := range []string{"NVIDIA·젯슨 공급 미팅", "event-1"} {
		if !strings.Contains(heldCallBody, expectedFragment) {
			t.Fatalf("the ledger shows what was asked and what it narrowed to, expected %q in %s", expectedFragment, heldCallBody)
		}
	}
}

func TestAnApprovedCallIsCarriedOutAgainstTheResolvedIdentity(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UseApprovalTargetResolver(resolvedTargetResolver())
	gate.AwaitApproval(context.Background(), hintApprovalRequest(taskRun.TaskRunID))
	recordDecision(taskRunService, taskRun.TaskRunID, "confirm")

	approvedCall, isApproved := ApprovedPendingCall(taskRunService.ListTaskEvent(taskRun.TaskRunID))
	if !isApproved {
		t.Fatal("expected the approved call to be carried out")
	}
	if string(approvedCall.ToolInput) != `{"eventHint":"event-1"}` {
		t.Fatalf("consent binds to the entity the requester saw, not to the phrase, got %s", approvedCall.ToolInput)
	}
}

func TestTheApprovalDecisionStillMatchesTheCallTheModelReissuesUnchanged(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UseApprovalTargetResolver(resolvedTargetResolver())
	gate.AwaitApproval(context.Background(), hintApprovalRequest(taskRun.TaskRunID))
	recordDecision(taskRunService, taskRun.TaskRunID, "confirm")

	reissuedOutcome, _ := gate.AwaitApproval(context.Background(), hintApprovalRequest(taskRun.TaskRunID))
	if reissuedOutcome.Decision != mcpserver.ApprovalDecisionApproved {
		t.Fatalf("the model reissues the call it made, so the recorded decision has to match that call, got %+v", reissuedOutcome)
	}
}

func TestAToolWithNoResolvableTargetIsStillAskedAboutFromItsInput(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UseLanguageModel(&wordingLanguageModel{question: "메시지를 보낼까요?"})
	gate.UseApprovalTargetResolver(&recordingTargetResolver{})

	outcome, _ := gate.AwaitApproval(context.Background(), hintApprovalRequest(taskRun.TaskRunID))

	if outcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("a tool that resolves nothing ahead keeps the behaviour it has today, got %+v", outcome)
	}
	if !hasTaskEventNamed(taskRunService, taskRun.TaskRunID, "approval.pending_call") {
		t.Fatalf("expected the call to be held as before, got %+v", taskEventNames(taskRunService, taskRun.TaskRunID))
	}
}

func TestAResolverThatCannotBeReachedAsksFromTheInputRatherThanBlockingTheDelete(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UseLanguageModel(&wordingLanguageModel{question: "삭제할까요?"})
	gate.UseApprovalTargetResolver(&recordingTargetResolver{failure: errors.New("capabilityd is unreachable")})

	outcome, _ := gate.AwaitApproval(context.Background(), hintApprovalRequest(taskRun.TaskRunID))

	if outcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("a capabilityd hiccup must not block every delete, got %+v", outcome)
	}
	if !hasTaskEventNamed(taskRunService, taskRun.TaskRunID, "approval.pending_call") {
		t.Fatalf("expected the call to be held from its input as before, got %+v", taskEventNames(taskRunService, taskRun.TaskRunID))
	}
}

func TestTheResolverIsAskedAsTheRequesterWhoseCallItIs(t *testing.T) {
	gate, _, taskRun := gateFixture(t)
	resolver := resolvedTargetResolver()
	gate.UseApprovalTargetResolver(resolver)

	gate.AwaitApproval(context.Background(), hintApprovalRequest(taskRun.TaskRunID))

	if resolver.receivedRequest.RequesterEmail != "staff@example.com" {
		t.Fatalf("resolution follows the same ownership tiebreak the call itself would, got %+v", resolver.receivedRequest)
	}
	if resolver.receivedRequest.ToolName != "calendar_delete" || string(resolver.receivedRequest.ToolInput) != `{"eventHint":"NVIDIA·젯슨 공급 미팅"}` {
		t.Fatalf("the resolver is asked about the call the model made, got %+v", resolver.receivedRequest)
	}
}

func TestACallTheRequesterAlreadyApprovedForTheWholeTaskResolvesNothingAgain(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	resolver := resolvedTargetResolver()
	gate.UseApprovalTargetResolver(resolver)
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "approval.scope_granted", `{"scope":"calendar"}`)

	gate.AwaitApproval(context.Background(), hintApprovalRequest(taskRun.TaskRunID))

	if resolver.callCount != 0 {
		t.Fatalf("a call nobody will be asked about needs no question to word, got %d resolutions", resolver.callCount)
	}
}

func TestAnUnresolvableTargetStopsTheCallWithoutHoldingIt(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UseApprovalTargetResolver(unresolvedTargetResolver())
	turnGate := gate.TurnGate(TurnContext{RequesterPersonID: "person-1", RequesterEmail: "staff@example.com"})

	executed, result := invokeThroughGateInContext(t, toolcontract.WithTaskRunID(context.Background(), taskRun.TaskRunID), turnGate, "file_delete")

	if len(*executed) != 0 {
		t.Fatalf("a call whose target does not exist never runs, it ran %+v", *executed)
	}
	if result.Failure == nil || !strings.Contains(result.UserSafeFailureSummary(), "no calendar event matched eventHint") {
		t.Fatalf("the agent is handed the capability's own failure so it can choose another route, got %+v", result)
	}
	if hasTaskEventNamed(taskRunService, taskRun.TaskRunID, "approval.pending_call") {
		t.Fatalf("no approval is spent on a target that does not exist, got %+v", taskEventNames(taskRunService, taskRun.TaskRunID))
	}
}
