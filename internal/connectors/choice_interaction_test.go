//go:build !nobundledharness

package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/agenttest"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

const roomQuestionInput = `{"question":"어느 회의실로 할까요?","choices":["회의실 A","회의실 B"]}`

func roomChoices() []holdrecord.Choice {
	return []holdrecord.Choice{{Key: "1", Label: "회의실 A"}, {Key: "2", Label: "회의실 B"}}
}

type countingReplyReader struct {
	reading    approvalreply.Reading
	errorValue error
	readCount  int
}

func (reader *countingReplyReader) Read(context.Context, approvalreply.Question, string, agentcontract.LLMCallObserver) (approvalreply.Reading, error) {
	reader.readCount++
	return reader.reading, reader.errorValue
}

type askedQuestion struct {
	fixture  *threadAskFixture
	holdID   string
	reader   *countingReplyReader
	taskRun  task.TaskRun
	statuses chan approvalgate.AskStatus
	answers  chan approvalgate.ApprovalAnswer
}

func directMessageQuestion(t *testing.T, reader *countingReplyReader) *askedQuestion {
	t.Helper()
	fixture := newThreadAskFixture(t, threadAskScript{})
	fixture.connectorRuntime.UseApprovalReplyReader(reader)
	fixture.connectorRuntime.UseRequesterDirectMessages(openRequesterDirectMessage, requesterAccountsOnThePlatform())
	fixture.connectorRuntime.identityService.RememberPlatformAccount(identity.PlatformAccountIdentity{Platform: "test", ExternalUserID: "sender-user", Email: "invited@example.com"})
	return questionOnTheFixture(fixture, reader, "hold-1")
}

func questionOnTheFixture(fixture *threadAskFixture, reader *countingReplyReader, holdID string) *askedQuestion {
	return &askedQuestion{
		fixture:  fixture,
		holdID:   holdID,
		reader:   reader,
		taskRun:  fixture.connectorRuntime.taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "schedule:morning"}, "scheduled run"),
		statuses: make(chan approvalgate.AskStatus, 1),
		answers:  make(chan approvalgate.ApprovalAnswer, 1),
	}
}

func (asked *askedQuestion) ask(toolInput string, choices []holdrecord.Choice) *askedQuestion {
	approvalRequest := mcpserver.ApprovalRequest{RequesterPersonID: "person-1", TaskRunID: asked.taskRun.TaskRunID, Platform: "test", ConversationID: "schedule:morning", ToolName: "ask_input", ToolInput: json.RawMessage(toolInput)}
	go func() {
		answer, status := asked.fixture.connectorRuntime.ThreadPermissionAsker().AskPermission(context.Background(), approvalRequest, approvalgate.PermissionQuestion{HoldID: asked.holdID, Confirmation: "어느 회의실로 할까요?", Choices: choices})
		asked.answers <- answer
		asked.statuses <- status
	}()
	return asked
}

func askInTheDirectMessage(t *testing.T, reader *countingReplyReader, toolInput string, choices []holdrecord.Choice) *askedQuestion {
	t.Helper()
	return directMessageQuestion(t, reader).ask(toolInput, choices)
}

func (asked *askedQuestion) awaitPosted(t *testing.T) *AskInteraction {
	t.Helper()
	asked.fixture.awaitQuestionOnTheThread(t)
	return asked.fixture.adapter.sentReplies[0].interaction
}

func (asked *askedQuestion) reply(t *testing.T, prompt string, answered *AnsweredChoice) {
	t.Helper()
	event := directMessageReply("message-2", prompt)
	event.AnsweredChoice = answered
	asked.fixture.await(t, asked.fixture.send(context.Background(), event))
}

func (asked *askedQuestion) awaitEnd(t *testing.T) (approvalgate.ApprovalAnswer, approvalgate.AskStatus) {
	t.Helper()
	select {
	case answer := <-asked.answers:
		return answer, <-asked.statuses
	case <-agenttest.WhenTheTestRunsOut(t):
		t.Fatal("the question never ended")
		return approvalgate.ApprovalAnswer{}, ""
	}
}

func (asked *askedQuestion) expectClosedWith(t *testing.T, choiceKey string) {
	t.Helper()
	resolved := asked.fixture.adapter.interactionsResolved()
	if len(resolved) != 1 || resolved[0].interactionID != "hold-1" || resolved[0].messageID != "dispatch-1" || resolved[0].choiceKey != choiceKey {
		t.Fatalf("published %+v, expected hold-1 closed on dispatch-1 with key %q", resolved, choiceKey)
	}
}

func TestAPlainApprovalQuestionCarriesApproveAndRejectByKeyForTheRequester(t *testing.T) {
	asked := askInTheDirectMessage(t, &countingReplyReader{}, `{}`, nil)

	interaction := asked.awaitPosted(t)

	if interaction == nil || interaction.Kind != approvalInteractionKind || interaction.InteractionID != "hold-1" || interaction.TargetPlatformUserID != "sender-user" {
		t.Fatalf("the approval question went out with %+v, expected an approval interaction for hold-1 addressed to the requester", interaction)
	}
	if len(interaction.Options) != 2 || interaction.Options[0] != (AskChoiceOption{Key: ApproveOptionID}) || interaction.Options[1] != (AskChoiceOption{Key: RejectOptionID}) {
		t.Fatalf("the approval question offered %+v, expected unlabelled approve and reject", interaction.Options)
	}
}

func TestAPressedOptionAnswersWithoutReadingTheReplyAndClosesTheCardOnIt(t *testing.T) {
	asked := askInTheDirectMessage(t, &countingReplyReader{}, roomQuestionInput, roomChoices())
	interaction := asked.awaitPosted(t)
	if interaction == nil || interaction.Kind != agentcontract.AskKindInput || len(interaction.Options) != 2 || interaction.Options[1].Label != "회의실 B" {
		t.Fatalf("the question went out with %+v, expected its choices as labelled options", interaction)
	}

	asked.reply(t, "회의실 B", &AnsweredChoice{InteractionID: "hold-1", Key: "2"})
	answer, status := asked.awaitEnd(t)

	if status != approvalgate.AskAnswered || answer.ChoiceKey != "2" || asked.reader.readCount != 0 {
		t.Fatalf("answered %+v (%s) after %d reads, expected choice 2 with no reading", answer, status, asked.reader.readCount)
	}
	asked.expectClosedWith(t, "2")
}

func TestAPressNamingAnotherQuestionOrAnUnofferedKeyIsReadLikeTypedWords(t *testing.T) {
	for _, answered := range []AnsweredChoice{{InteractionID: "hold-other", Key: ApproveOptionID}, {InteractionID: "hold-1", Key: "approve-twice"}} {
		asked := askInTheDirectMessage(t, &countingReplyReader{reading: approvalreply.Reading{ApprovedOptionID: ApproveOptionID}}, `{}`, nil)
		asked.awaitPosted(t)

		asked.reply(t, "네", &answered)
		answer, _ := asked.awaitEnd(t)

		if asked.reader.readCount != 1 || answer.Signal != agentcontract.ApprovalSignalApprove {
			t.Fatalf("%+v was read %d times and answered %+v, expected the reader to judge it once", answered, asked.reader.readCount, answer)
		}
		asked.expectClosedWith(t, ApproveOptionID)
	}
}

func TestATypedReplyReadAsAnOptionClosesTheCardOnThatOption(t *testing.T) {
	asked := askInTheDirectMessage(t, &countingReplyReader{reading: approvalreply.Reading{ApprovedOptionID: "2"}}, roomQuestionInput, roomChoices())
	asked.awaitPosted(t)

	asked.reply(t, "두 번째", nil)
	asked.awaitEnd(t)

	asked.expectClosedWith(t, "2")
}

func TestAnAnswerInOwnWordsClosesTheCardWithNoOption(t *testing.T) {
	asked := askInTheDirectMessage(t, &countingReplyReader{reading: approvalreply.Reading{ApprovedOptionID: approvalreply.OwnWordsOptionID}}, roomQuestionInput, roomChoices())
	asked.awaitPosted(t)

	asked.reply(t, "둘 다 말고 회의실 C", nil)
	answer, _ := asked.awaitEnd(t)

	if answer.ChoiceKey != "" || answer.Signal != agentcontract.ApprovalSignalApprove {
		t.Fatalf("own words answered %+v", answer)
	}
	asked.expectClosedWith(t, "")
}

func TestAnOperatorAnswerClosesTheCardToo(t *testing.T) {
	asked := askInTheDirectMessage(t, &countingReplyReader{}, `{}`, nil)
	asked.awaitPosted(t)

	if isAnswered, errorValue := asked.fixture.connectorRuntime.AnswerAwaitedHold(asked.taskRun.TaskRunID, agentcontract.ApprovalSignalReject); !isAnswered || errorValue != nil {
		t.Fatalf("the operator's answer was not taken: %v %v", isAnswered, errorValue)
	}
	asked.awaitEnd(t)

	asked.expectClosedWith(t, RejectOptionID)
}

func TestAnUnansweredQuestionClosesItsCardWithNoOptionWhenItExpires(t *testing.T) {
	asked := directMessageQuestion(t, &countingReplyReader{})
	asked.fixture.connectorRuntime.askingThreads.expiry = 200 * time.Millisecond

	asked.ask(`{}`, nil).awaitPosted(t)
	_, status := asked.awaitEnd(t)

	if status != approvalgate.AskExpired {
		t.Fatalf("the question ended %s, expected it to expire", status)
	}
	asked.expectClosedWith(t, "")
}

func TestACardThatCannotBeClosedDoesNotStopTheAnswer(t *testing.T) {
	asked := directMessageQuestion(t, &countingReplyReader{})
	asked.fixture.adapter.resolveInteractionError = errors.New("chatd answered 503")
	asked.ask(`{}`, nil).awaitPosted(t)

	asked.reply(t, "승인", &AnsweredChoice{InteractionID: "hold-1", Key: ApproveOptionID})
	answer, status := asked.awaitEnd(t)

	if status != approvalgate.AskAnswered || answer.Signal != agentcontract.ApprovalSignalApprove || len(asked.fixture.adapter.interactionsResolved()) != 1 {
		t.Fatalf("answered %+v (%s) after %d close attempts, expected the approval and one attempt", answer, status, len(asked.fixture.adapter.interactionsResolved()))
	}
}

func TestOnlyAnsweredQuestionsAndPlainApprovalsCarryAnInteraction(t *testing.T) {
	scheduledRun := heldCallInteraction("hold-1", []holdrecord.Choice{{Key: "now"}, {Key: "offHours", StartsAt: "2099-10-03T03:00:00+09:00"}}, json.RawMessage(`{}`))
	withoutHold := heldCallInteraction("", nil, json.RawMessage(`{}`))

	if scheduledRun != nil || withoutHold != nil {
		t.Fatalf("a scheduled-run choice gave %+v and a question with no hold gave %+v, expected neither to carry an interaction", scheduledRun, withoutHold)
	}
}

func (fixture *threadAskFixture) awaitQuestionsPosted(t *testing.T, count int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		fixture.adapter.mutex.Lock()
		posted := len(fixture.adapter.sentReplies)
		fixture.adapter.mutex.Unlock()
		if posted >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fewer than %d questions were posted", count)
}

func TestAPressAnswersTheQuestionItNamesWhenTwoWaitInOneConversation(t *testing.T) {
	first := directMessageQuestion(t, &countingReplyReader{})
	first.ask(`{}`, nil)
	first.fixture.awaitQuestionsPosted(t, 1)
	second := questionOnTheFixture(first.fixture, first.reader, "hold-2").ask(`{}`, nil)
	first.fixture.awaitQuestionsPosted(t, 2)

	second.reply(t, "승인", &AnsweredChoice{InteractionID: "hold-2", Key: ApproveOptionID})

	if _, isStillWaiting := first.fixture.connectorRuntime.askingThreads.awaitingRun(first.taskRun.TaskRunID); !isStillWaiting {
		t.Fatal("the press settled the first question instead of the one it named")
	}
	answer, status := second.awaitEnd(t)
	if status != approvalgate.AskAnswered || answer.Signal != agentcontract.ApprovalSignalApprove {
		t.Fatalf("the named question ended %s with %+v, expected the pressed approval", status, answer)
	}
	resolved := first.fixture.adapter.interactionsResolved()
	if len(resolved) != 1 || resolved[0].interactionID != "hold-2" || resolved[0].choiceKey != ApproveOptionID {
		t.Fatalf("published %+v, expected only hold-2 closed on approve", resolved)
	}
}
