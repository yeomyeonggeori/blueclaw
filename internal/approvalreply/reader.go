package approvalreply

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

const (
	ApproveOptionID = "approve"
	RejectOptionID  = "reject"
	otherOptionID   = "other"

	schemaName = "blueclaw_approval_reply"
)

const systemPrompt = `A person was asked a question and replied. Choose which of the offered options the reply picks, or "other".
Choose an option only when the reply plainly picks it, in any language, spelling or brevity: a short affirmative such as "yes" or "ok" accepts, a plain refusal declines, naming a choice or its number picks that choice.
Choose "other" when the reply asks something, changes the request, gives new instructions, or says anything unrelated to the question, and when it is too vague to tell which option it picks.
Do not invent an option the list does not offer, and do not add requirements the question does not state. Politeness, wording, typos and formatting never make a plain reply "other".`

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

type LanguageModelReader struct {
	languageModel model.LanguageModelProvider
}

func NewLanguageModelReader(languageModel model.LanguageModelProvider) LanguageModelReader {
	return LanguageModelReader{languageModel: languageModel}
}

func QuestionFor(text string, choices []approvalgate.ApprovalChoice) Question {
	if len(choices) == 0 {
		return Question{Text: text, Options: []Option{
			{ID: ApproveOptionID, Meaning: "go ahead with the action"},
			{ID: RejectOptionID, Meaning: "do not do the action"},
		}}
	}
	options := []Option{}
	for _, replyOption := range approvalgate.ChoiceReplyOptions(choices) {
		options = append(options, Option{ID: replyOption.Key, Meaning: replyOption.Label})
	}
	return Question{Text: text, Options: options}
}

func (reader LanguageModelReader) Read(ctx context.Context, question Question, reply string, observe agentcontract.LLMCallObserver) (string, bool, error) {
	if reader.languageModel == nil {
		return "", false, errors.New("reading an approval reply needs a language model provider and none is configured")
	}
	trimmedReply := strings.TrimSpace(reply)
	if trimmedReply == "" {
		return "", false, errReplyCarriesNoWords
	}
	request, errorValue := readRequest(question, trimmedReply)
	if errorValue != nil {
		return "", false, errorValue
	}
	response, errorValue := agentcontract.ObserveLanguageModel(reader.languageModel, observe).GenerateStructuredResponse(ctx, request)
	if errorValue != nil {
		return "", false, errorValue
	}
	return offeredOption(question, response.Content)
}

func readRequest(question Question, reply string) (model.StructuredResponseRequest, error) {
	schemaDocument, errorValue := schemaFor(question)
	if errorValue != nil {
		return model.StructuredResponseRequest{}, errorValue
	}
	askedDocument, errorValue := json.Marshal(map[string]any{"question": question.Text, "options": question.Options, "reply": reply})
	if errorValue != nil {
		return model.StructuredResponseRequest{}, errorValue
	}
	return model.StructuredResponseRequest{
		Messages: []model.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: string(askedDocument)},
		},
		StructuredOutputSchema: model.StructuredOutputSchema{Name: schemaName, Document: schemaDocument, IsStrictlyEnforced: true},
	}, nil
}

func schemaFor(question Question) (string, error) {
	optionIDs := []string{}
	for _, option := range question.Options {
		optionIDs = append(optionIDs, option.ID)
	}
	document, errorValue := json.Marshal(map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"answer": map[string]any{"type": "string", "enum": append(optionIDs, otherOptionID)}},
		"required":             []string{"answer"},
		"additionalProperties": false,
	})
	return string(document), errorValue
}

func offeredOption(question Question, content string) (string, bool, error) {
	answer := struct {
		Answer string `json:"answer"`
	}{}
	if errorValue := json.Unmarshal([]byte(content), &answer); errorValue != nil {
		return "", false, errorValue
	}
	for _, option := range question.Options {
		if option.ID == answer.Answer {
			return option.ID, true, nil
		}
	}
	return "", false, nil
}
