package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

const requesterHeaderForTest = "X-Requester-Email"

func TestAnEndpointNamesTheRequesterOnlyWhenItsRungAsksFor(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		headerName string
		expected   string
	}{
		{name: "a rung that charges the requester", headerName: requesterHeaderForTest, expected: "park.sample@example.com"},
		{name: "a rung that does not", headerName: "", expected: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			received := "unset"
			server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
				received = request.Header.Get(requesterHeaderForTest)
				writeCompletion(responseWriter, schemaAnswer(`{"question":"done"}`), "tool_calls")
			}))
			defer server.Close()
			apiKeyPath := filepath.Join(t.TempDir(), "host-session")
			writeKeyFile(t, apiKeyPath, "session")
			provider, errorValue := endpointProvider(config.ModelEndpointConfiguration{
				Endpoint: server.URL + "/v1", Model: "example/model", APIKeyPath: apiKeyPath, RequesterEmailHeader: testCase.headerName,
			})
			if errorValue != nil {
				t.Fatal(errorValue)
			}

			if _, errorValue := provider.GenerateResponse(contextAskedBy("park.sample@example.com"), "hello"); errorValue != nil {
				t.Fatal(errorValue)
			}

			if received != testCase.expected {
				t.Fatalf("expected the requester header %q, got %q", testCase.expected, received)
			}
		})
	}
}

func TestADecisionEndpointNamesTheRequester(t *testing.T) {
	received := ""
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		received = request.Header.Get(requesterHeaderForTest)
		_, _ = responseWriter.Write([]byte(`{"answers":{"route":{"choice":"start_task","probabilities":{"start_task":0.9,"answer_question":0.1}}}}`))
	}))
	defer server.Close()
	apiKeyPath := filepath.Join(t.TempDir(), "host-session")
	writeKeyFile(t, apiKeyPath, "session")
	decisionModel, errorValue := NewConfiguredDecisionModel(config.RuntimeConfiguration{LanguageModel: config.LanguageModelConfiguration{
		Decision: config.ModelEndpointConfiguration{Endpoint: server.URL + "/api/alpha/decisions", Model: "kev-latest", APIKeyPath: apiKeyPath, RequesterEmailHeader: requesterHeaderForTest},
	}})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	request := model.DecisionRequest{
		State:     "a message",
		Questions: map[string]model.DecisionQuestion{"route": model.ChoiceQuestion{Instructions: "What now?", OptionDescriptions: map[string]string{"start_task": "work", "answer_question": "words"}}.Question()},
	}

	if _, errorValue := decisionModel.Decide(contextAskedBy("park.sample@example.com"), request); errorValue != nil {
		t.Fatal(errorValue)
	}

	if received != "park.sample@example.com" {
		t.Fatalf("expected the decision to name its requester, got %q", received)
	}
}

func contextAskedBy(email string) context.Context {
	return ContextWithRequestContext(context.Background(), model.RequestContext{RequesterEmail: email})
}
