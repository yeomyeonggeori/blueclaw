package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func postedDecisionState(t *testing.T, request model.DecisionRequest) any {
	t.Helper()
	var posted struct {
		State any `json:"state"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, httpRequest *http.Request) {
		if errorValue := json.NewDecoder(httpRequest.Body).Decode(&posted); errorValue != nil {
			t.Fatal(errorValue)
		}
		_ = json.NewEncoder(responseWriter).Encode(map[string]any{"answers": map[string]any{"visual_defect": map[string]any{"choice": "none"}}})
	}))
	defer server.Close()
	client := CapabilityDecisionClient{CapabilityLLMClient: CapabilityLLMClient{CapabilityClient: capability.Client{Endpoint: server.URL, HTTPClient: server.Client()}}}
	if _, errorValue := client.Decide(context.Background(), request); errorValue != nil {
		t.Fatal(errorValue)
	}
	return posted.State
}

func sampleDecisionQuestions() map[string]model.DecisionQuestion {
	return map[string]model.DecisionQuestion{"visual_defect": model.ChoiceQuestion{Instructions: "Which defect?", OptionDescriptions: map[string]string{"none": "clean"}}.Question()}
}

func TestADecisionRequestWithAnImagePostsAPartsArrayState(t *testing.T) {
	state := postedDecisionState(t, model.DecisionRequest{
		State:     map[string]any{"number": 2},
		Questions: sampleDecisionQuestions(),
		Images:    []model.DecisionImage{{MediaType: "image/png", Data: []byte("png-bytes")}},
	})

	parts, isArray := state.([]any)
	if !isArray || len(parts) != 2 {
		t.Fatalf("expected a text part and an image part, got %v", state)
	}
	text, _ := parts[0].(map[string]any)
	image, _ := parts[1].(map[string]any)
	if text["type"] != "text" || image["type"] != "image_url" {
		t.Fatalf("expected the state as text then the image, got %v", parts)
	}
}

func TestADecisionRequestWithoutImagesPostsTheStateAsItIs(t *testing.T) {
	state := postedDecisionState(t, model.DecisionRequest{State: map[string]any{"number": 2}, Questions: sampleDecisionQuestions()})

	if shown, isObject := state.(map[string]any); !isObject || shown["number"] != float64(2) {
		t.Fatalf("expected the state object, got %v", state)
	}
}
