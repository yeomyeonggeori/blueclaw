//go:build !nobundledharness

package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

const roomQuestionInput = `{"question":"어느 회의실로 할까요?","choices":["회의실 A","회의실 B"]}`

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
	*runWithoutAnEvent
	reader *countingReplyReader
}

func directMessageQuestion(t *testing.T, reader *countingReplyReader) *askedQuestion {
	t.Helper()
	fixture := newThreadAskFixture(t, threadAskScript{})
	fixture.connectorRuntime.UseApprovalReplyReader(reader)
	fixture.connectorRuntime.UseRequesterDirectMessages(openRequesterDirectMessage, requesterAccountsOnThePlatform())
	return &askedQuestion{runWithoutAnEvent: scheduledRunOn(fixture), reader: reader}
}

func askApprovalInTheDirectMessage(t *testing.T, reader *countingReplyReader) *askedQuestion {
	t.Helper()
	asked := directMessageQuestion(t, reader)
	asked.callHeldTool(t, "event_delete", `{"eventHint":"event-1"}`)
	return asked
}

func askRoomInTheDirectMessage(t *testing.T, reader *countingReplyReader) *askedQuestion {
	t.Helper()
	asked := directMessageQuestion(t, reader)
	asked.callHeldTool(t, "ask_input", roomQuestionInput)
	return asked
}

func (asked *askedQuestion) postedInteraction() *AskInteraction {
	return asked.fixture.adapter.sentReplies[0].interaction
}

func (asked *askedQuestion) reply(prompt string, answered *AnsweredChoice) ParkedAnswer {
	event := directMessageReply("message-2", prompt)
	event.AnsweredChoice = answered
	return asked.answer(event)
}

func (asked *askedQuestion) expectClosedWith(t *testing.T, choiceKey string) {
	t.Helper()
	resolved := asked.fixture.adapter.interactionsResolved()
	if len(resolved) != 1 || resolved[0].interactionID != asked.hold(t).ID || resolved[0].messageID != "dispatch-1" || resolved[0].choiceKey != choiceKey {
		t.Fatalf("published %+v, expected the question closed on dispatch-1 with key %q", resolved, choiceKey)
	}
}

func TestAPlainApprovalQuestionCarriesApproveAndRejectByKeyForTheRequester(t *testing.T) {
	asked := askApprovalInTheDirectMessage(t, &countingReplyReader{})

	interaction := asked.postedInteraction()

	if interaction == nil || interaction.Kind != approvalInteractionKind || interaction.InteractionID != asked.hold(t).ID || interaction.TargetPlatformUserID != "sender-user" {
		t.Fatalf("the approval question went out with %+v, expected an approval interaction for its hold addressed to the requester", interaction)
	}
	if len(interaction.Options) != 2 || interaction.Options[0] != (AskChoiceOption{Key: approvalgate.ApproveOptionKey}) || interaction.Options[1] != (AskChoiceOption{Key: approvalgate.RejectOptionKey}) {
		t.Fatalf("the approval question offered %+v, expected unlabelled approve and reject", interaction.Options)
	}
}

func TestAPressedButtonSettlesWithoutAReadingAndClosesTheCardOnIt(t *testing.T) {
	asked := askRoomInTheDirectMessage(t, &countingReplyReader{})
	interaction := asked.postedInteraction()
	if interaction == nil || interaction.Kind != agentcontract.AskKindInput || len(interaction.Options) != 2 || interaction.Options[1].Label != "회의실 B" {
		t.Fatalf("the question went out with %+v, expected its choices as labelled options", interaction)
	}

	answer := asked.reply("회의실 B", &AnsweredChoice{InteractionID: asked.hold(t).ID, Key: "2"})

	if !answer.ShouldResume || asked.reader.readCount != 0 {
		t.Fatalf("settled %+v after %d readings, expected the pressed option to settle with no reading", answer, asked.reader.readCount)
	}
	chosen, isChosen := approvalgate.AnswerChosen(asked.fixture.connectorRuntime.taskRunService.ListTaskEvent(asked.taskRun.TaskRunID), asked.hold(t).Choices)
	if !isChosen || chosen.ChoiceKey != "2" {
		t.Fatalf("the pressed option was recorded as %+v, expected choice 2", chosen)
	}
	asked.expectClosedWith(t, "2")
}

func TestAPressNamingAnotherQuestionOrAnUnofferedKeyIsReadLikeTypedWords(t *testing.T) {
	for _, pressed := range []AnsweredChoice{{InteractionID: "hold-other", Key: approvalgate.ApproveOptionKey}, {Key: "approve-twice"}} {
		asked := askApprovalInTheDirectMessage(t, &countingReplyReader{reading: approvalreply.Reading{OptionID: approvalgate.ApproveOptionKey}})
		if pressed.InteractionID == "" {
			pressed.InteractionID = asked.hold(t).ID
		}

		answer := asked.reply("네", &pressed)

		if asked.reader.readCount != 1 || !answer.ShouldResume || asked.hold(t).State != holdrecord.StateApproved {
			t.Fatalf("%+v was read %d times and settled %+v, expected the reader to judge it once", pressed, asked.reader.readCount, answer)
		}
		asked.expectClosedWith(t, approvalgate.ApproveOptionKey)
	}
}

func TestATypedReplyReadAsAnOptionClosesTheCardOnThatOption(t *testing.T) {
	asked := askRoomInTheDirectMessage(t, &countingReplyReader{reading: approvalreply.Reading{OptionID: "2"}})

	asked.reply("두 번째", nil)

	asked.expectClosedWith(t, "2")
}

func TestAnAnswerInOwnWordsClosesTheCardWithNoOptionAndIsWhatTheQuestionReturns(t *testing.T) {
	asked := askRoomInTheDirectMessage(t, &countingReplyReader{reading: approvalreply.Reading{OptionID: approvalreply.OwnWordsOptionID}})

	answer := asked.reply("둘 다 말고 회의실 C", nil)

	result := approvalgate.ChosenAnswerResult(asked.fixture.connectorRuntime.taskRunService.ListTaskEvent(asked.taskRun.TaskRunID), json.RawMessage(roomQuestionInput))
	if !answer.ShouldResume || result.Failure != nil || result.Output.Content != "The requester answered in their own words: 둘 다 말고 회의실 C" {
		t.Fatalf("own words settled %+v and the question returns %+v", answer, result)
	}
	asked.expectClosedWith(t, "")
}

func TestAReplyThatPicksNoneClosesTheCardWithNoOptionAndLeavesTheMessageToBeHandledAsAnOrdinaryTurn(t *testing.T) {
	asked := askRoomInTheDirectMessage(t, &countingReplyReader{reading: approvalreply.Reading{}})

	answer := asked.reply("회의는 몇 시였지?", nil)

	if answer.IsAnswer || asked.hold(t).State == holdrecord.StatePending || asked.reader.readCount != 1 {
		t.Fatalf("a reply that picks nothing settled %+v with the hold %s after %d readings", answer, asked.hold(t).State, asked.reader.readCount)
	}
	asked.expectClosedWith(t, "")
}

func TestAReplyTheReaderCouldNotReadIsTreatedAsPickingNone(t *testing.T) {
	asked := askApprovalInTheDirectMessage(t, &countingReplyReader{errorValue: errors.New("the decision model is down")})

	answer := asked.reply("ㅇ", nil)

	if answer.IsAnswer || asked.hold(t).State == holdrecord.StateApproved {
		t.Fatalf("an unread reply settled %+v with the hold %s, and nothing may be approved on a reading that failed", answer, asked.hold(t).State)
	}
}

func TestAnOperatorAnswerClosesTheCardToo(t *testing.T) {
	asked := askApprovalInTheDirectMessage(t, &countingReplyReader{})

	if _, errorValue := asked.fixture.connectorRuntime.AnswerParkedQuestionAsOperator(context.Background(), asked.taskRun.TaskRunID, agentcontract.ApprovalSignalReject); errorValue != nil {
		t.Fatalf("the operator's answer was not taken: %v", errorValue)
	}

	asked.expectClosedWith(t, approvalgate.RejectOptionKey)
}

func TestACardThatCannotBeClosedDoesNotStopTheAnswer(t *testing.T) {
	asked := directMessageQuestion(t, &countingReplyReader{})
	asked.fixture.adapter.resolveInteractionError = errors.New("chatd answered 503")
	asked.callHeldTool(t, "event_delete", `{"eventHint":"event-1"}`)

	answer := asked.reply("승인", &AnsweredChoice{InteractionID: asked.hold(t).ID, Key: approvalgate.ApproveOptionKey})

	if !answer.ShouldResume || asked.hold(t).State != holdrecord.StateApproved || len(asked.fixture.adapter.interactionsResolved()) != 1 {
		t.Fatalf("settled %+v after %d close attempts, expected the approval and one attempt", answer, len(asked.fixture.adapter.interactionsResolved()))
	}
}

func TestOnlyAnsweredQuestionsAndPlainApprovalsCarryAnInteraction(t *testing.T) {
	scheduledRun := heldCallInteraction("hold-1", []holdrecord.Choice{{Key: "now"}, {Key: "offHours", StartsAt: "2099-10-03T03:00:00+09:00"}}, json.RawMessage(`{}`))
	withoutHold := heldCallInteraction("", nil, json.RawMessage(`{}`))

	if scheduledRun != nil || withoutHold != nil {
		t.Fatalf("a scheduled-run choice gave %+v and a question with no hold gave %+v, expected neither to carry an interaction", scheduledRun, withoutHold)
	}
}

func TestANewQuestionInTheSamePlaceClosesTheCardOfTheOneBeforeIt(t *testing.T) {
	first := askApprovalInTheDirectMessage(t, &countingReplyReader{})
	second := &askedQuestion{runWithoutAnEvent: scheduledRunOn(first.fixture), reader: first.reader}

	second.callHeldTool(t, "event_delete", `{"eventHint":"event-2"}`)

	if first.hold(t).State == holdrecord.StatePending || second.hold(t).State != holdrecord.StatePending {
		t.Fatalf("the earlier hold is %s and the newer %s, expected only the newer question to wait", first.hold(t).State, second.hold(t).State)
	}
	answer := second.reply("승인", &AnsweredChoice{InteractionID: second.hold(t).ID, Key: approvalgate.ApproveOptionKey})
	resolved := first.fixture.adapter.interactionsResolved()
	if !answer.ShouldResume || answer.TaskRun.TaskRunID != second.taskRun.TaskRunID {
		t.Fatalf("the press settled %+v, expected the newer run", answer)
	}
	if len(resolved) != 2 || resolved[0].interactionID != first.hold(t).ID || resolved[0].choiceKey != "" || resolved[1].interactionID != second.hold(t).ID || resolved[1].choiceKey != approvalgate.ApproveOptionKey {
		t.Fatalf("published %+v, expected the earlier card closed with no choice and the newer closed on approve", resolved)
	}
}
