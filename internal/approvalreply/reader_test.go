package approvalreply

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type answeringModel struct {
	choice        string
	probabilities map[string]float64
	requests      []model.DecisionRequest
}

func (decisionModel *answeringModel) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	decisionModel.requests = append(decisionModel.requests, request)
	probabilities := decisionModel.probabilities
	if probabilities == nil {
		probabilities = map[string]float64{decisionModel.choice: 1}
	}
	return model.DecisionResponse{Answers: map[string]model.DecisionAnswer{AnswerQuestionName: {Type: model.DecisionQuestionTypeChoice, Choice: decisionModel.choice, Probabilities: probabilities}}}, nil
}

const (
	approveOptionID = "approve"
	rejectOptionID  = "reject"
)

func approvalQuestion(text string) Question {
	return Question{Text: text, Options: []Option{
		{ID: approveOptionID, Meaning: AllowMeaning("approve this call")},
		{ID: rejectOptionID, Meaning: RejectMeaning, IsDeclining: true},
	}}
}

type failingModel struct{}

func (failingModel) Decide(context.Context, model.DecisionRequest) (model.DecisionResponse, error) {
	return model.DecisionResponse{}, errors.New("decisions endpoint answered 502")
}

func TestAnAllowingOptionIsAnApprovalOnlyWhenItOutweighsEverythingElseTogether(t *testing.T) {
	for _, readingCase := range []struct {
		name          string
		probabilities map[string]float64
		approved      string
	}{
		{name: "a plurality is not a majority", probabilities: map[string]float64{approveOptionID: 0.4, OtherOptionID: 0.35, rejectOptionID: 0.25}},
		{name: "a majority approves", probabilities: map[string]float64{approveOptionID: 0.55, OtherOptionID: 0.3, rejectOptionID: 0.15}, approved: approveOptionID},
		{name: "a tie is not a majority", probabilities: map[string]float64{approveOptionID: 0.5, OtherOptionID: 0.5}},
		{name: "no probabilities are no approval", probabilities: map[string]float64{}},
		{name: "a declining majority is no approval", probabilities: map[string]float64{rejectOptionID: 0.9, approveOptionID: 0.1}},
	} {
		decisionModel := &answeringModel{choice: approveOptionID, probabilities: readingCase.probabilities}
		reading, errorValue := NewDecisionModelReader(decisionModel).Read(context.Background(), approvalQuestion("보낼까요?"), "ㅇ", nil)
		if errorValue != nil || reading.ApprovedOptionID != readingCase.approved {
			t.Fatalf("%s: read %+v, want approval of %q: %v", readingCase.name, reading, readingCase.approved, errorValue)
		}
	}
}

func TestEachAllowingOptionIsWeighedAgainstAllTheRest(t *testing.T) {
	question := Question{Text: "언제 할까요?", Options: []Option{
		{ID: "now", Meaning: AllowMeaning("run it now")},
		{ID: "offHours", Meaning: AllowMeaning("run it at 03:00")},
		{ID: "cancel", Meaning: RejectMeaning, IsDeclining: true},
	}}
	for _, readingCase := range []struct {
		probabilities map[string]float64
		approved      string
	}{
		{probabilities: map[string]float64{"now": 0.45, "offHours": 0.45, "cancel": 0.1}},
		{probabilities: map[string]float64{"now": 0.2, "offHours": 0.7, OtherOptionID: 0.1}, approved: "offHours"},
	} {
		decisionModel := &answeringModel{choice: "now", probabilities: readingCase.probabilities}
		reading, _ := NewDecisionModelReader(decisionModel).Read(context.Background(), question, "새벽에", nil)
		if reading.ApprovedOptionID != readingCase.approved {
			t.Fatalf("%v: read %+v, want %q", readingCase.probabilities, reading, readingCase.approved)
		}
	}
}

func TestOtherAndAnOptionNobodyOfferedAreNotAnApproval(t *testing.T) {
	for _, choice := range []string{"other", "tomorrowNoon"} {
		reading, errorValue := NewDecisionModelReader(&answeringModel{choice: choice}).Read(context.Background(), approvalQuestion("보낼까요?"), "음", nil)
		if errorValue != nil || reading.IsApproval() {
			t.Fatalf("%s read as an approval %+v: %v", choice, reading, errorValue)
		}
	}
}

func TestTheDecliningOptionIsTheOneOfferedAsDeclining(t *testing.T) {
	if approvalQuestion("보낼까요?").DecliningOptionID() != rejectOptionID {
		t.Fatal("the question did not name its declining option")
	}
}

func TestTheQuestionOffersExactlyTheOptionsAndOtherAndNothingElse(t *testing.T) {
	decisionModel := &answeringModel{choice: "other"}
	question := Question{Text: "언제 할까요?", Options: []Option{
		{ID: "now", Meaning: AllowMeaning("run it now")},
		{ID: "offHours", Meaning: AllowMeaning("run it at 03:00")},
		{ID: "cancel", Meaning: RejectMeaning},
	}}

	NewDecisionModelReader(decisionModel).Read(context.Background(), question, "새벽에", nil)

	if len(decisionModel.requests) != 1 || len(decisionModel.requests[0].Questions) != 1 {
		t.Fatalf("expected one call with one question, got %+v", decisionModel.requests)
	}
	criteria, isDescribed := decisionModel.requests[0].Questions[AnswerQuestionName].Criteria.(map[string]string)
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

	NewDecisionModelReader(decisionModel).Read(context.Background(), approvalQuestion("보낼까요?"), "ㅇ", nil)

	criteria, _ := decisionModel.requests[0].Questions[AnswerQuestionName].Criteria.(map[string]string)
	if len(criteria) != 3 || criteria[approveOptionID] == "" || criteria[rejectOptionID] == "" || criteria[OtherOptionID] == "" {
		t.Fatalf("expected approve, reject and other, got %+v", criteria)
	}
}

func TestTheStateCarriesOnlyThePostedQuestionAndTheReply(t *testing.T) {
	decisionModel := &answeringModel{choice: "approve"}

	NewDecisionModelReader(decisionModel).Read(context.Background(), approvalQuestion("보낼까요?"), "  ㅇ  ", nil)

	state, isReadState := decisionModel.requests[0].State.(readState)
	if !isReadState || state != (readState{PostedQuestion: "보낼까요?", Reply: "ㅇ"}) {
		t.Fatalf("expected the posted question and the trimmed reply and nothing else, got %+v", decisionModel.requests[0].State)
	}
}

func TestAnEmptyReplyIsNotSentToTheModel(t *testing.T) {
	decisionModel := &answeringModel{choice: "approve"}

	reading, errorValue := NewDecisionModelReader(decisionModel).Read(context.Background(), approvalQuestion("보낼까요?"), "  ", nil)

	if errorValue == nil || reading.IsApproval() || len(decisionModel.requests) != 0 {
		t.Fatalf("an empty reply reached the model: read=%+v error=%v", reading, errorValue)
	}
}

func TestAMissingDecisionModelIsAnError(t *testing.T) {
	_, errorValue := NewDecisionModelReader(nil).Read(context.Background(), approvalQuestion("보낼까요?"), "ㅇ", nil)

	if errorValue == nil {
		t.Fatal("a reader with no decision model answered")
	}
}

func TestTheCallIsHandedToTheObserver(t *testing.T) {
	observed := []agentcontract.LLMCallRecord{}
	NewDecisionModelReader(&answeringModel{choice: "reject"}).Read(context.Background(), approvalQuestion("보낼까요?"), "싫어", func(record agentcontract.LLMCallRecord) {
		observed = append(observed, record)
	})

	if len(observed) != 1 || observed[0].Kind != agentcontract.LLMCallKindDecision || observed[0].QuestionCount != 1 {
		t.Fatalf("expected one decision record, got %+v", observed)
	}
}

func TestAFailedCallIsObservedAndReturned(t *testing.T) {
	observed := []agentcontract.LLMCallRecord{}
	_, errorValue := NewDecisionModelReader(failingModel{}).Read(context.Background(), approvalQuestion("보낼까요?"), "ㅇ", func(record agentcontract.LLMCallRecord) {
		observed = append(observed, record)
	})

	if errorValue == nil || len(observed) != 1 || !observed[0].IsError {
		t.Fatalf("expected the error and an errored record, got %v %+v", errorValue, observed)
	}
}

func TestAnOptionsLabelIsNotWhatTheOnlyWayToGoAheadMeans(t *testing.T) {
	labelled := OptionsOf([]Offer{{ID: "allow", Name: "Allow"}, {ID: "reject", Name: "Reject", IsDeclining: true}})
	renamed := OptionsOf([]Offer{{ID: "allow", Name: "approve this call as asked"}, {ID: "reject", IsDeclining: true}})

	if labelled[0].Meaning != renamed[0].Meaning || labelled[1].Meaning != RejectMeaning {
		t.Fatalf("expected one reading of a lone allowing option whatever it is called, got %+v and %+v", labelled, renamed)
	}
}

func TestSeveralAllowingOptionsAreToldApartByTheirOwnWords(t *testing.T) {
	options := OptionsOf([]Offer{{ID: "now", Name: "run it now"}, {ID: "later", Name: "run it at 03:00"}, {ID: "cancel", IsDeclining: true}})

	if options[0].Meaning != AllowMeaning("run it now") || options[1].Meaning != AllowMeaning("run it at 03:00") || options[2].Meaning != RejectMeaning {
		t.Fatalf("expected each allowing option to keep its name and declining to mean declining, got %+v", options)
	}
}
