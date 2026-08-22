package agentruntime

import (
	"reflect"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestEveryRequestFieldIsEitherRecordedOrDeclaredInvisible(t *testing.T) {
	classified := map[string]bool{}
	for _, fieldName := range append(append([]string{}, modelVisibleRequestFields...), runtimeOnlyRequestFields...) {
		if classified[fieldName] {
			t.Fatalf("%s is in both lists, so nothing decides whether the model sees it", fieldName)
		}
		classified[fieldName] = true
	}

	requestType := reflect.TypeOf(agentcontract.AgentTurnRequest{})
	for index := 0; index < requestType.NumField(); index++ {
		fieldName := requestType.Field(index).Name
		if !classified[fieldName] {
			t.Fatalf("%s is new on AgentTurnRequest and nothing says whether a model reads it. Add it to modelVisibleRequestFields and to modelVisibleContextDocument, or to runtimeOnlyRequestFields.", fieldName)
		}
		delete(classified, fieldName)
	}
	for fieldName := range classified {
		t.Fatalf("%s is classified and no longer exists, so the lists are describing a request nobody sends", fieldName)
	}
}

func TestTheRecordedContextCarriesWhatTheModelWasGiven(t *testing.T) {
	document := modelVisibleContextDocument(agentcontract.AgentTurnRequest{
		Prompt:          "회의록 보내줘",
		HostInstruction: "The company closes at six.",
		RequesterName:   "이샘플",
		VisibleContext: agentcontract.VisibleContext{Messages: []agentcontract.VisibleContextMessage{
			{Speaker: "박예시", Text: "라운지에서 촬영이 있습니다"},
		}},
		MemoryFacts: []agentcontract.MemoryFact{{Content: "이샘플 prefers Korean"}},
	})

	for _, expected := range []string{"회의록 보내줘", "The company closes at six.", "라운지에서 촬영이 있습니다", "이샘플 prefers Korean"} {
		if !strings.Contains(document, expected) {
			t.Fatalf("the ledger has to hold what the model was given, or diagnosing a turn means guessing: %q missing from %s", expected, document)
		}
	}
}

func TestARecordedFieldIsAlsoInTheDocument(t *testing.T) {
	document := modelVisibleContextDocument(agentcontract.AgentTurnRequest{})
	for _, fieldName := range modelVisibleRequestFields {
		jsonName := strings.ToLower(fieldName[:1]) + fieldName[1:]
		if !strings.Contains(document, `"`+jsonName+`"`) {
			t.Fatalf("%s is listed as model-visible and modelVisibleContextDocument does not write it, so the list promises a record nobody keeps", fieldName)
		}
	}
}
