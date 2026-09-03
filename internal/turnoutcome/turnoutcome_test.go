package turnoutcome

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type stubLanguageModel struct {
	responseContent string
	errorValue      error
	lastSubject     string
}

func (stub *stubLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("not used")
}

func (stub *stubLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	for _, message := range request.Messages {
		if message.Role == "user" {
			stub.lastSubject = message.Content
		}
	}
	return model.StructuredResponse{Content: stub.responseContent}, stub.errorValue
}

func TestAnHonestFailureReportIsNotCompleted(t *testing.T) {
	stub := &stubLanguageModel{responseContent: `{"outcome":"failed","reason":"the agent reported it could not read the file"}`}

	verdict, errorValue := NewClassifier(stub).Classify(context.Background(), "Summarise last month's expenses.", "I could not open the expense sheet, so I did not summarise it.", nil)

	if errorValue != nil {
		t.Fatalf("expected a verdict, got %v", errorValue)
	}
	if verdict.Status != agentcontract.TaskStatusFailed {
		t.Fatalf("expected failed, got %q", verdict.Status)
	}
	if verdict.Reason == "" {
		t.Fatal("expected the verdict to carry a reason for the ledger")
	}
}

func TestTheClassifierSeesWhichToolsActuallySucceeded(t *testing.T) {
	stub := &stubLanguageModel{responseContent: `{"outcome":"completed","reason":"the message was sent"}`}

	if _, errorValue := NewClassifier(stub).Classify(context.Background(), "Tell the team.", "Done.", []string{"message_send"}); errorValue != nil {
		t.Fatalf("expected a verdict, got %v", errorValue)
	}
	if !strings.Contains(stub.lastSubject, "message_send") {
		t.Fatalf("expected the observed tools to reach the classifier, got %q", stub.lastSubject)
	}
}

func TestAnUnknownOutcomeIsNotSilentlyTreatedAsSuccess(t *testing.T) {
	stub := &stubLanguageModel{responseContent: `{"outcome":"probably fine","reason":"who knows"}`}

	if _, errorValue := NewClassifier(stub).Classify(context.Background(), "Do the thing.", "Did the thing.", nil); errorValue == nil {
		t.Fatal("expected an unknown outcome to be an error rather than a completion")
	}
}

func TestAnUnconfiguredClassifierRefusesRatherThanGuessing(t *testing.T) {
	classifier := NewClassifier(nil)

	if classifier.IsConfigured() {
		t.Fatal("expected a nil provider to report itself unconfigured")
	}
	if _, errorValue := classifier.Classify(context.Background(), "Do the thing.", "Did the thing.", nil); errorValue == nil {
		t.Fatal("expected an unconfigured classifier to refuse")
	}
}

func TestOnlySucceededToolsAreRecorded(t *testing.T) {
	recorder := &SucceededToolRecorder{}

	recorder.Observe("message_send", true)
	recorder.Observe("message_send", true)
	recorder.Observe("document_create", false)

	recordedToolNames := recorder.SucceededToolNames()
	if len(recordedToolNames) != 1 || recordedToolNames[0] != "message_send" {
		t.Fatalf("expected only the one succeeded tool, got %v", recordedToolNames)
	}
}
