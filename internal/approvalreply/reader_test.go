package approvalreply

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type answeringModel struct {
	choice   string
	requests []model.DecisionRequest
}

func (decisionModel *answeringModel) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	decisionModel.requests = append(decisionModel.requests, request)
	return model.DecisionResponse{Answers: map[string]model.DecisionAnswer{answerQuestionName: {Type: model.DecisionQuestionTypeChoice, Choice: decisionModel.choice}}}, nil
}

type failingModel struct{}

func (failingModel) Decide(context.Context, model.DecisionRequest) (model.DecisionResponse, error) {
	return model.DecisionResponse{}, errors.New("decisions endpoint answered 502")
}

func TestAnOfferedOptionIsTheAnswer(t *testing.T) {
	optionID, isAnswer, errorValue := NewDecisionModelReader(&answeringModel{choice: "approve"}).Read(context.Background(), QuestionFor("보낼까요?", nil), "ㅇ", nil)

	if errorValue != nil || !isAnswer || optionID != ApproveOptionID {
		t.Fatalf("read %q answered=%v: %v", optionID, isAnswer, errorValue)
	}
}

func TestOtherAndAnOptionNobodyOfferedAreNotAnAnswer(t *testing.T) {
	for _, choice := range []string{"other", "tomorrowNoon"} {
		_, isAnswer, errorValue := NewDecisionModelReader(&answeringModel{choice: choice}).Read(context.Background(), QuestionFor("보낼까요?", nil), "음", nil)
		if errorValue != nil || isAnswer {
			t.Fatalf("%s read as an answer=%v: %v", choice, isAnswer, errorValue)
		}
	}
}

func TestTheQuestionOffersExactlyTheOptionsAndOtherAndNothingElse(t *testing.T) {
	decisionModel := &answeringModel{choice: "other"}
	choices := []approvalgate.ApprovalChoice{{Key: "now"}, {Key: "offHours", StartsAt: "2099-10-03T03:00:00+09:00"}}

	NewDecisionModelReader(decisionModel).Read(context.Background(), QuestionFor("언제 할까요?", choices), "새벽에", nil)

	if len(decisionModel.requests) != 1 || len(decisionModel.requests[0].Questions) != 1 {
		t.Fatalf("expected one call with one question, got %+v", decisionModel.requests)
	}
	criteria, isDescribed := decisionModel.requests[0].Questions[answerQuestionName].Criteria.(map[string]string)
	if !isDescribed {
		t.Fatalf("the question carries no option descriptions: %+v", decisionModel.requests[0].Questions)
	}
	offered := []string{}
	for optionID := range criteria {
		offered = append(offered, optionID)
	}
	sort.Strings(offered)
	expected := []string{"cancel", "now", "offHours", "other"}
	if len(offered) != len(expected) {
		t.Fatalf("expected the options %v, got %v", expected, offered)
	}
	for index := range expected {
		if offered[index] != expected[index] {
			t.Fatalf("expected the options %v, got %v", expected, offered)
		}
	}
}

func TestAPlainApprovalQuestionOffersApproveRejectAndOther(t *testing.T) {
	decisionModel := &answeringModel{choice: "approve"}

	NewDecisionModelReader(decisionModel).Read(context.Background(), QuestionFor("보낼까요?", nil), "ㅇ", nil)

	criteria, _ := decisionModel.requests[0].Questions[answerQuestionName].Criteria.(map[string]string)
	if len(criteria) != 3 || criteria[ApproveOptionID] == "" || criteria[RejectOptionID] == "" || criteria[otherOptionID] == "" {
		t.Fatalf("expected approve, reject and other, got %+v", criteria)
	}
}

func TestTheStateCarriesOnlyThePostedQuestionAndTheReply(t *testing.T) {
	decisionModel := &answeringModel{choice: "approve"}

	NewDecisionModelReader(decisionModel).Read(context.Background(), QuestionFor("보낼까요?", nil), "  ㅇ  ", nil)

	state, isReadState := decisionModel.requests[0].State.(readState)
	if !isReadState || state != (readState{PostedQuestion: "보낼까요?", Reply: "ㅇ"}) {
		t.Fatalf("expected the posted question and the trimmed reply and nothing else, got %+v", decisionModel.requests[0].State)
	}
}

func TestAnEmptyReplyIsNotSentToTheModel(t *testing.T) {
	decisionModel := &answeringModel{choice: "approve"}

	_, isAnswer, errorValue := NewDecisionModelReader(decisionModel).Read(context.Background(), QuestionFor("보낼까요?", nil), "  ", nil)

	if errorValue == nil || isAnswer || len(decisionModel.requests) != 0 {
		t.Fatalf("an empty reply reached the model: answered=%v error=%v", isAnswer, errorValue)
	}
}

func TestAMissingDecisionModelIsAnError(t *testing.T) {
	_, _, errorValue := NewDecisionModelReader(nil).Read(context.Background(), QuestionFor("보낼까요?", nil), "ㅇ", nil)

	if errorValue == nil {
		t.Fatal("a reader with no decision model answered")
	}
}

func TestTheCallIsHandedToTheObserver(t *testing.T) {
	observed := []agentcontract.LLMCallRecord{}
	NewDecisionModelReader(&answeringModel{choice: "reject"}).Read(context.Background(), QuestionFor("보낼까요?", nil), "싫어", func(record agentcontract.LLMCallRecord) {
		observed = append(observed, record)
	})

	if len(observed) != 1 || observed[0].Kind != agentcontract.LLMCallKindDecision || observed[0].QuestionCount != 1 {
		t.Fatalf("expected one decision record, got %+v", observed)
	}
}

func TestAFailedCallIsObservedAndReturned(t *testing.T) {
	observed := []agentcontract.LLMCallRecord{}
	_, _, errorValue := NewDecisionModelReader(failingModel{}).Read(context.Background(), QuestionFor("보낼까요?", nil), "ㅇ", func(record agentcontract.LLMCallRecord) {
		observed = append(observed, record)
	})

	if errorValue == nil || len(observed) != 1 || !observed[0].IsError {
		t.Fatalf("expected the error and an errored record, got %v %+v", errorValue, observed)
	}
}
