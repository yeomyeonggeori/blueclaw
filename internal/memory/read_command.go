package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/yeomyeonggeori/bluememo"
)

const ReadCommand = "memory-read"

type ReadRequest struct {
	StorePath      string    `json:"storePath"`
	Query          string    `json:"query"`
	QueryVector    []float32 `json:"queryVector"`
	EmbeddingModel string    `json:"embeddingModel"`
	Limit          int       `json:"limit"`
	LaneDepth      int       `json:"laneDepth"`
	RecallSources  bool      `json:"recallSources"`
}

// RunRead tells a memory nobody has written from one this reader may not
// open: the first is nothing to recall, the second is a refusal, and a recall
// that confused them would report every empty file as a failure.
func RunRead(ctx context.Context, input io.Reader, output io.Writer) error {
	request, errorValue := decodeReadRequest(input)
	if errorValue != nil {
		return errorValue
	}
	if _, errorValue := os.Stat(request.StorePath); errors.Is(errorValue, os.ErrNotExist) {
		return json.NewEncoder(output).Encode(bluememo.RecallResult{})
	}
	store, errorValue := bluememo.Open(ctx, request.StorePath, bluememo.Configuration{
		Embedder:       carriedVector{vector: request.QueryVector},
		EmbeddingModel: request.EmbeddingModel,
		LaneDepth:      request.LaneDepth,
		RecallSources:  request.RecallSources,
	})
	if errorValue != nil {
		return fmt.Errorf("open %s: %w", request.StorePath, errorValue)
	}
	defer store.Close()

	result, errorValue := store.Recall(ctx, request.Query, request.Limit)
	if errorValue != nil {
		return fmt.Errorf("recall from %s: %w", request.StorePath, errorValue)
	}
	return json.NewEncoder(output).Encode(result)
}

func decodeReadRequest(input io.Reader) (ReadRequest, error) {
	var request ReadRequest
	if errorValue := json.NewDecoder(input).Decode(&request); errorValue != nil {
		return ReadRequest{}, fmt.Errorf("read request: %w", errorValue)
	}
	if strings.TrimSpace(request.StorePath) == "" {
		return ReadRequest{}, errors.New("read request carries no store path")
	}
	if len(request.QueryVector) == 0 {
		return ReadRequest{}, errors.New("read request carries no query vector")
	}
	return request, nil
}

type carriedVector struct {
	vector []float32
}

func (carried carriedVector) EmbedQuery(context.Context, string) ([]float32, error) {
	return carried.vector, nil
}

func (carried carriedVector) EmbedDocuments(context.Context, []string) ([][]float32, error) {
	return nil, errors.New("a read embeds nothing: the service holds the credentials for that")
}
