package approvalreply

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

const (
	ApproveOptionID = "approve"
	RejectOptionID  = "reject"
	otherOptionID   = "other"

	answerQuestionName = "answer"
)

const instructions = `The state holds a question a person was asked (postedQuestion) and their reply (reply). Which option does the reply pick? Read the reply as an answer to that question, by what the person means, in any language and any script. A reply to a question is usually brief: a word, a bit of shorthand or a single character is a full answer, and polite or friendly words around it do not change it. Choose other only when the reply does not pick any of the options.`

const (
	approveMeaning = "the person agrees to the action as asked, however briefly or informally, alone or with thanks, politeness or a remark that leaves what would be done exactly as asked"
	rejectMeaning  = "the person declines, cancels or halts the action, with or without a reason"
	otherMeaning   = "the reply picks none of the options: it asks something, leaves the decision open, adds to or changes what would be done (target, recipients, content, scope, time or conditions), or is an unrelated request"
)

var errReplyCarriesNoWords = errors.New("an approval reply with no words says nothing to read")

type Option struct {
	ID      string `json:"id"`
	Meaning string `json:"meaning"`
}

type Question struct {
	Text    string   `json:"question"`
	Options []Option `json:"options"`
}

type Reader interface {
	Read(ctx context.Context, question Question, reply string, observe agentcontract.LLMCallObserver) (string, bool, error)
}

type DecisionModelReader struct {
	decisionModel model.DecisionModel
}

func NewDecisionModelReader(decisionModel model.DecisionModel) DecisionModelReader {
	return DecisionModelReader{decisionModel: decisionModel}
}

func QuestionFor(text string, choices []approvalgate.ApprovalChoice) Question {
	if len(choices) == 0 {
		return Question{Text: text, Options: []Option{
			{ID: ApproveOptionID, Meaning: approveMeaning},
			{ID: RejectOptionID, Meaning: rejectMeaning},
		}}
	}
	options := []Option{}
	for _, replyOption := range approvalgate.ChoiceReplyOptions(choices) {
		options = append(options, Option{ID: replyOption.Key, Meaning: "the reply picks this choice: " + replyOption.Label})
	}
	return Question{Text: text, Options: options}
}

type readState struct {
	PostedQuestion string `json:"postedQuestion"`
	Reply          string `json:"reply"`
}

func (reader DecisionModelReader) Read(ctx context.Context, question Question, reply string, observe agentcontract.LLMCallObserver) (string, bool, error) {
	if reader.decisionModel == nil {
		return "", false, errors.New("reading an approval reply needs a decision model and none is configured")
	}
	trimmedReply := strings.TrimSpace(reply)
	if trimmedReply == "" {
		return "", false, errReplyCarriesNoWords
	}
	request := readRequest(question, trimmedReply)
	startedAt := time.Now()
	response, errorValue := reader.decisionModel.Decide(ctx, request)
	if observe != nil {
		observe(agentcontract.DecisionCallRecord(request, response, time.Since(startedAt), errorValue))
	}
	if errorValue != nil {
		return "", false, errorValue
	}
	return offeredOption(question, response)
}

func readRequest(question Question, reply string) model.DecisionRequest {
	descriptions := map[string]string{otherOptionID: otherMeaning}
	for _, option := range question.Options {
		descriptions[option.ID] = option.Meaning
	}
	return model.DecisionRequest{
		State:     readState{PostedQuestion: question.Text, Reply: reply},
		Questions: map[string]model.DecisionQuestion{answerQuestionName: model.ChoiceQuestion{Instructions: instructions, OptionDescriptions: descriptions}.Question()},
	}
}

func offeredOption(question Question, response model.DecisionResponse) (string, bool, error) {
	answer, isAnswered := response.Answers[answerQuestionName]
	if !isAnswered || strings.TrimSpace(answer.Choice) == "" {
		return "", false, errors.New("the decision model answered no choice for the approval reply")
	}
	for _, option := range question.Options {
		if option.ID == strings.TrimSpace(answer.Choice) {
			return option.ID, true, nil
		}
	}
	return "", false, nil
}
