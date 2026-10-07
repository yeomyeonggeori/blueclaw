//go:build !nobundledharness

package defaultharness

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func TestSkillRetrievalEmbedsThroughTheCapabilityRouteWithInputTypesAndDimensions(t *testing.T) {
	var mutex sync.Mutex
	inputTypes := []string{}
	dimensions := map[float64]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		var received map[string]any
		if errorValue := json.NewDecoder(request.Body).Decode(&received); errorValue != nil {
			t.Error(errorValue)
		}
		mutex.Lock()
		inputTypes = append(inputTypes, received["inputType"].(string))
		dimensions[received["outputDimensions"].(float64)] = true
		mutex.Unlock()
		_ = json.NewEncoder(responseWriter).Encode(map[string]any{"embedding": []float64{1, 0}, "embeddings": [][]float64{{1, 0}}})
	}))
	defer server.Close()
	runtimeConfiguration := config.RuntimeConfiguration{
		Capabilities:  config.CapabilityConfiguration{Endpoint: server.URL},
		LanguageModel: config.LanguageModelConfiguration{Embedding: config.ModelEndpointConfiguration{Model: llm.DefaultEmbeddingModelName}},
	}
	embeddingProvider, errorValue := llm.NewConfiguredEmbeddingProvider(runtimeConfiguration)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	skillRetriever := newSkillRetriever(harnessdriver.Dependencies{
		EmbeddingProvider:  embeddingProvider,
		EmbeddingModelName: llm.DefaultEmbeddingModelName,
		SkillIndexPath:     filepath.Join(t.TempDir(), "skill-index.json"),
	})
	skillInstructions := []agentcontract.SkillInstruction{{
		Name:        "presentation",
		Description: "Create presentation slides.",
		Source:      agentcontract.InstructionSource{Path: "/skills/presentation/SKILL.md", SHA256: "one", SkillName: "presentation"},
	}}

	skillRetriever.Search(context.Background(), agentcontract.AgentRequest{}, skillInstructions, agentcontract.SkillSearchQuerySet{
		Queries: []agentcontract.SkillSearchQuery{{Description: "make slides"}},
	}, 3)

	if len(inputTypes) != 2 || inputTypes[0] != llm.EmbeddingInputTypeDocument || inputTypes[1] != llm.EmbeddingInputTypeQuery {
		t.Fatalf("expected the skill description as a document then the request as a query, got %v", inputTypes)
	}
	if len(dimensions) != 1 || !dimensions[float64(llm.DefaultEmbeddingDimensions)] {
		t.Fatalf("expected every skill embedding to ask for %d dimensions, got %v", llm.DefaultEmbeddingDimensions, dimensions)
	}
}
