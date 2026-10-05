package approvalreply

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type answeringModel struct {
	content string
	request model.StructuredResponseRequest
}

func (languageModel *answeringModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("the reader only asks for structured output")
}

func (languageModel *answeringModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	languageModel.request = request
	return model.StructuredResponse{Content: languageModel.content}, nil
}

func TestAnOfferedOptionIsTheAnswer(t *testing.T) {
	languageModel := &answeringModel{content: `{"answer":"approve"}`}

	optionID, isAnswer, errorValue := NewLanguageModelReader(languageModel).Read(context.Background(), QuestionFor("보낼까요?", nil), "ㅇ", nil)

	if errorValue != nil || !isAnswer || optionID != ApproveOptionID {
		t.Fatalf("read %q answered=%v: %v", optionID, isAnswer, errorValue)
	}
}

func TestOtherAndAnOptionNobodyOfferedAreNotAnAnswer(t *testing.T) {
	for _, content := range []string{`{"answer":"other"}`, `{"answer":"tomorrowNoon"}`} {
		_, isAnswer, errorValue := NewLanguageModelReader(&answeringModel{content: content}).Read(context.Background(), QuestionFor("보낼까요?", nil), "음", nil)
		if errorValue != nil || isAnswer {
			t.Fatalf("%s read as an answer=%v: %v", content, isAnswer, errorValue)
		}
	}
}

func TestTheSchemaOffersExactlyTheOptionsAndOtherAndNothingElse(t *testing.T) {
	languageModel := &answeringModel{content: `{"answer":"other"}`}
	choices := []approvalgate.ApprovalChoice{{Key: "now"}, {Key: "offHours", StartsAt: "2099-10-03T03:00:00+09:00"}}

	NewLanguageModelReader(languageModel).Read(context.Background(), QuestionFor("언제 할까요?", choices), "새벽에", nil)

	document := languageModel.request.StructuredOutputSchema.Document
	if !strings.Contains(document, `"enum":["now","offHours","cancel","other"]`) || !strings.Contains(document, `"additionalProperties":false`) || !languageModel.request.StructuredOutputSchema.IsStrictlyEnforced {
		t.Fatalf("the schema is not the closed set of offered ids: %s", document)
	}
}

func TestAnEmptyReplyIsNotSentToTheModel(t *testing.T) {
	languageModel := &answeringModel{content: `{"answer":"approve"}`}

	_, isAnswer, errorValue := NewLanguageModelReader(languageModel).Read(context.Background(), QuestionFor("보낼까요?", nil), "  ", nil)

	if errorValue == nil || isAnswer || len(languageModel.request.Messages) != 0 {
		t.Fatalf("an empty reply reached the model: answered=%v error=%v", isAnswer, errorValue)
	}
}

func TestTheCallIsHandedToTheObserver(t *testing.T) {
	observed := []agentcontract.LLMCallRecord{}
	NewLanguageModelReader(&answeringModel{content: `{"answer":"reject"}`}).Read(context.Background(), QuestionFor("보낼까요?", nil), "싫어", func(record agentcontract.LLMCallRecord) {
		observed = append(observed, record)
	})

	if len(observed) != 1 {
		t.Fatalf("the reading was observed %d times, expected once", len(observed))
	}
}
