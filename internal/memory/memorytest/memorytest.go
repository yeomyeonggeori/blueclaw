package memorytest

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"math"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/bluecollar/model"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
)

type HashEmbedder struct {
	Failure error
	mutex   sync.Mutex
	calls   int
}

func (embedder *HashEmbedder) EmbedQuery(_ context.Context, text string) ([]float32, error) {
	embedder.mutex.Lock()
	embedder.calls++
	embedder.mutex.Unlock()
	if embedder.Failure != nil {
		return nil, embedder.Failure
	}
	return Embed(text), nil
}

func (embedder *HashEmbedder) EmbedDocuments(_ context.Context, texts []string) ([][]float32, error) {
	embedder.mutex.Lock()
	embedder.calls++
	embedder.mutex.Unlock()
	if embedder.Failure != nil {
		return nil, embedder.Failure
	}
	embeddings := make([][]float32, 0, len(texts))
	for _, text := range texts {
		embeddings = append(embeddings, Embed(text))
	}
	return embeddings, nil
}

func (embedder *HashEmbedder) Calls() int {
	embedder.mutex.Lock()
	defer embedder.mutex.Unlock()
	return embedder.calls
}

func Embed(text string) []float32 {
	embedding := make([]float32, memory.EmbeddingDimensionCount)
	for _, term := range strings.Fields(strings.ToLower(text)) {
		hasher := fnv.New32a()
		_, _ = hasher.Write([]byte(term))
		embedding[int(hasher.Sum32()%uint32(memory.EmbeddingDimensionCount))] += 1
	}
	var norm float64
	for _, value := range embedding {
		norm += float64(value) * float64(value)
	}
	if norm == 0 {
		embedding[0] = 1
		return embedding
	}
	scale := float32(1 / math.Sqrt(norm))
	for index := range embedding {
		embedding[index] *= scale
	}
	return embedding
}

type ScriptedModel struct {
	mutex     sync.Mutex
	responses []string
	Failure   error
	Requests  []model.StructuredResponseRequest
}

func NewScriptedModel(responses ...string) *ScriptedModel {
	return &ScriptedModel{responses: responses}
}

func (scripted *ScriptedModel) Queue(response any) {
	scripted.mutex.Lock()
	defer scripted.mutex.Unlock()
	document, _ := json.Marshal(response)
	scripted.responses = append(scripted.responses, string(document))
}

func (scripted *ScriptedModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("scripted model answers structured requests only")
}

func (scripted *ScriptedModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	scripted.mutex.Lock()
	defer scripted.mutex.Unlock()
	scripted.Requests = append(scripted.Requests, request)
	if scripted.Failure != nil {
		return model.StructuredResponse{}, scripted.Failure
	}
	if len(scripted.responses) == 0 {
		return model.StructuredResponse{}, errors.New("scripted model has no response left")
	}
	response := scripted.responses[0]
	scripted.responses = scripted.responses[1:]
	return model.StructuredResponse{Content: response, ModelName: "scripted"}, nil
}

func (scripted *ScriptedModel) RequestCount() int {
	scripted.mutex.Lock()
	defer scripted.mutex.Unlock()
	return len(scripted.Requests)
}

func (scripted *ScriptedModel) LastUserMessage() string {
	scripted.mutex.Lock()
	defer scripted.mutex.Unlock()
	if len(scripted.Requests) == 0 {
		return ""
	}
	messages := scripted.Requests[len(scripted.Requests)-1].Messages
	return messages[len(messages)-1].Content
}

type IngestFact struct {
	Content           string `json:"content"`
	Kind              string `json:"kind"`
	Scope             string `json:"scope"`
	SubjectPersonHint string `json:"subjectPersonHint"`
	Relation          string `json:"relation"`
	RelatedFactID     string `json:"relatedFactID"`
	ValidUntil        string `json:"validUntil"`
}

func IngestResponse(facts ...IngestFact) map[string]any {
	if facts == nil {
		facts = []IngestFact{}
	}
	return map[string]any{"facts": facts}
}

func ProfileResponse(identityLines []string, currentLines []string) map[string]any {
	return map[string]any{"identityLines": identityLines, "currentLines": currentLines}
}
