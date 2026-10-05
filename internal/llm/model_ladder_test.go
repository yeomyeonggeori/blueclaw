package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
)

const answeringModel = "example/answering"

type ladderFailure struct {
	name   string
	answer func(http.ResponseWriter)
}

var failuresTheLadderMustSurvive = []ladderFailure{
	{"prose instead of the schema", func(writer http.ResponseWriter) {
		writeCompletion(writer, map[string]any{"content": "삭제할까요?"}, "stop")
	}},
	{"an empty answer", func(writer http.ResponseWriter) {
		writeCompletion(writer, map[string]any{"content": ""}, "stop")
	}},
	{"an answer cut off at its length limit", func(writer http.ResponseWriter) {
		writeCompletion(writer, map[string]any{"content": `{"question":"삭제`}, "length")
	}},
	{"a refused request", func(writer http.ResponseWriter) {
		http.Error(writer, `{"error":{"message":"tool_choice is not supported"}}`, http.StatusBadRequest)
	}},
}

func writeCompletion(writer http.ResponseWriter, message map[string]any, finishReason string) {
	writer.Header().Set("Content-Type", "application/json")
	json.NewEncoder(writer).Encode(map[string]any{
		"provider": "Example",
		"choices":  []map[string]any{{"message": message, "finish_reason": finishReason}},
	})
}

func schemaAnswer(arguments string) map[string]any {
	return map[string]any{"tool_calls": []map[string]any{{
		"id": "call-1", "type": "function",
		"function": map[string]any{"name": "example_question", "arguments": arguments},
	}}}
}

type modelLadderServer struct {
	mutex   sync.Mutex
	askedBy map[string]int
}

func (server *modelLadderServer) asked(modelName string) int {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	return server.askedBy[modelName]
}

func startModelLadderServer(t *testing.T, failure ladderFailure) (*modelLadderServer, string) {
	ladderServer := &modelLadderServer{askedBy: map[string]int{}}
	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		json.NewDecoder(request.Body).Decode(&body)
		ladderServer.mutex.Lock()
		ladderServer.askedBy[body.Model]++
		ladderServer.mutex.Unlock()
		if body.Model == answeringModel {
			writeCompletion(writer, schemaAnswer(`{"question":"미국 출장 일정을 삭제할까요?"}`), "tool_calls")
			return
		}
		failure.answer(writer)
	}))
	t.Cleanup(httpServer.Close)
	return ladderServer, httpServer.URL
}

func TestATiersNextModelAnswersWhenTheFirstBreaksTheSchema(t *testing.T) {
	for _, failure := range failuresTheLadderMustSurvive {
		t.Run(failure.name, func(t *testing.T) {
			ladderServer, url := startModelLadderServer(t, failure)
			runtimeConfiguration := configurationWithOneEndpointPerTier(url, answeringModel)
			runtimeConfiguration.LanguageModel.Tiers["high"] = []config.ModelEndpointConfiguration{
				endpointConfiguration(url, "example/failing"),
				endpointConfiguration(url, answeringModel),
			}
			tierProviderFactory, errorValue := NewTierProviderFactory(runtimeConfiguration)
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			tierProvider, errorValue := tierProviderFactory("high")
			if errorValue != nil {
				t.Fatal(errorValue)
			}

			response, errorValue := tierProvider.Provider.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
				Messages:               []Message{{Role: "user", Content: "Ask whether to delete the trip."}},
				StructuredOutputSchema: StructuredOutputSchema{Name: "example_question", Document: `{"type":"object","properties":{"question":{"type":"string"}},"required":["question"],"additionalProperties":false}`},
			})

			if errorValue != nil {
				t.Fatalf("one model of a tier failing must leave the answer to the next one, got %v", errorValue)
			}
			if response.Content != `{"question":"미국 출장 일정을 삭제할까요?"}` || !response.UsedFallback || response.FallbackReason == "" {
				t.Fatalf("the answer must come from the next model and say why, got %#v", response)
			}
			if ladderServer.asked("example/failing") == 0 || ladderServer.asked(answeringModel) != 1 {
				t.Fatalf("each model must be asked in order, got %v", ladderServer.askedBy)
			}
		})
	}
}
