package approvalreply

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

const (
	OtherOptionID = "other"

	AnswerQuestionName = "answer"
)

const instructions = `The state holds a question a person was asked (postedQuestion) and their reply (reply). Which option does the reply pick? Read the reply as an answer to that question, by what the person means, in any language and any script. A reply to a question is usually brief: a word, a bit of shorthand or a single character is a full answer, and polite or friendly words around it do not change it. Choose other only when the reply does not pick any of the options.`

const (
	RejectMeaning = "the person declines, cancels or halts the action, with or without a reason"
	otherMeaning  = "the reply picks none of the options: it asks something, leaves the decision open, adds to or changes what would be done (target, recipients, content, scope, time or conditions), or is an unrelated request"
)

func AllowMeaning(optionName string) string {
	return "the person agrees to go ahead with \"" + optionName + "\" as asked, however briefly or informally, alone or with thanks, politeness or a remark that leaves what would be done exactly as asked"
}

const allowAsAskedMeaning = "the person agrees to go ahead with what the question asks, however briefly or informally, alone or with thanks, politeness or a remark that leaves what would be done exactly as asked"

var errReplyCarriesNoWords = errors.New("an approval reply with no words says nothing to read")

type Option struct {
	ID          string `json:"id"`
	Meaning     string `json:"meaning"`
	IsDeclining bool   `json:"-"`
}

type Offer struct {
	ID          string
	Name        string
	IsDeclining bool
}

func OptionsOf(offers []Offer) []Option {
	isOnlyWayToGoAhead := countAllowing(offers) == 1
	options := make([]Option, 0, len(offers))
	for _, offer := range offers {
		options = append(options, Option{ID: offer.ID, Meaning: meaningOf(offer, isOnlyWayToGoAhead), IsDeclining: offer.IsDeclining})
	}
	return options
}

func countAllowing(offers []Offer) int {
	count := 0
	for _, offer := range offers {
		if !offer.IsDeclining {
			count++
		}
	}
	return count
}

func meaningOf(offer Offer, isOnlyWayToGoAhead bool) string {
	if offer.IsDeclining {
		return RejectMeaning
	}
	if isOnlyWayToGoAhead {
		return allowAsAskedMeaning
	}
	return AllowMeaning(offer.Name)
}

type Question struct {
	Text    string   `json:"question"`
	Options []Option `json:"options"`
}

func (question Question) DecliningOptionID() string {
	for _, option := range question.Options {
		if option.IsDeclining {
			return option.ID
		}
	}
	return ""
}

type Reading struct {
	ApprovedOptionID string
}

func (reading Reading) IsApproval() bool {
	return reading.ApprovedOptionID != ""
}

type Reader interface {
	Read(ctx context.Context, question Question, reply string, observe agentcontract.LLMCallObserver) (Reading, error)
}

type DecisionModelReader struct {
	decisionModel model.DecisionModel
}

func NewDecisionModelReader(decisionModel model.DecisionModel) DecisionModelReader {
	return DecisionModelReader{decisionModel: decisionModel}
}

type readState struct {
	PostedQuestion string `json:"postedQuestion"`
	Reply          string `json:"reply"`
}

func (reader DecisionModelReader) Read(ctx context.Context, question Question, reply string, observe agentcontract.LLMCallObserver) (Reading, error) {
	if reader.decisionModel == nil {
		return Reading{}, errors.New("reading an approval reply needs a decision model and none is configured")
	}
	trimmedReply := strings.TrimSpace(reply)
	if trimmedReply == "" {
		return Reading{}, errReplyCarriesNoWords
	}
	request := readRequest(question, trimmedReply)
	startedAt := time.Now()
	response, errorValue := reader.decisionModel.Decide(ctx, request)
	if observe != nil {
		observe(agentcontract.DecisionCallRecord(request, response, time.Since(startedAt), errorValue))
	}
	if errorValue != nil {
		return Reading{}, errorValue
	}
	return Reading{ApprovedOptionID: approvedOption(question, response.Answers[AnswerQuestionName])}, nil
}

func readRequest(question Question, reply string) model.DecisionRequest {
	descriptions := map[string]string{OtherOptionID: otherMeaning}
	for _, option := range question.Options {
		descriptions[option.ID] = option.Meaning
	}
	return model.DecisionRequest{
		State:     readState{PostedQuestion: question.Text, Reply: reply},
		Questions: map[string]model.DecisionQuestion{AnswerQuestionName: model.ChoiceQuestion{Instructions: instructions, OptionDescriptions: descriptions}.Question()},
	}
}

func approvedOption(question Question, answer model.DecisionAnswer) string {
	total := 0.0
	for _, probability := range answer.Probabilities {
		total += probability
	}
	for _, option := range question.Options {
		probability := answer.Probabilities[option.ID]
		if !option.IsDeclining && probability > total-probability {
			return option.ID
		}
	}
	return ""
}
