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

// ReadRequest names a stack of stores, nearest first: the read goes through
// all of them as one, the way a store stands on the layers beneath it.
type ReadRequest struct {
	StorePaths     []string  `json:"storePaths"`
	Query          string    `json:"query"`
	QueryVector    []float32 `json:"queryVector"`
	EmbeddingModel string    `json:"embeddingModel"`
	Limit          int       `json:"limit"`
	LaneDepth      int       `json:"laneDepth"`
	RecallSources  bool      `json:"recallSources"`
}

// ReadResult says, for each memory recalled, which of the request's stores
// holds it, so the service reinforces it there and a reader forgets it there.
type ReadResult struct {
	Recall bluememo.RecallResult `json:"recall"`
	Layers []int                 `json:"layers"`
}

func (result ReadResult) validate(layerCount int) error {
	if len(result.Layers) != len(result.Recall.Memories) {
		return fmt.Errorf("a read named the store of %d memories out of %d", len(result.Layers), len(result.Recall.Memories))
	}
	for _, layer := range result.Layers {
		if layer < 0 || layer >= layerCount {
			return fmt.Errorf("a read named store %d of %d", layer, layerCount)
		}
	}
	return nil
}

// RunRead tells a memory nobody has written from one this reader may not
// open: the first is nothing to recall, the second is a refusal, and a recall
// that confused them would report every empty file as a failure.
func RunRead(ctx context.Context, input io.Reader, output io.Writer) error {
	request, errorValue := decodeReadRequest(input)
	if errorValue != nil {
		return errorValue
	}
	opened, errorValue := openToRead(ctx, request)
	defer closeAll(opened)
	if errorValue != nil {
		return errorValue
	}
	if len(opened) == 0 {
		return json.NewEncoder(output).Encode(ReadResult{Layers: []int{}})
	}
	found := map[string]int{}
	beneath := make([]bluememo.Known, 0, len(opened)-1)
	for _, layer := range opened[1:] {
		beneath = append(beneath, recordingLayer{layer: layer, found: found})
	}
	recalled, errorValue := opened[0].store.On(beneath...).Recall(ctx, request.Query, request.Limit)
	if errorValue != nil {
		return fmt.Errorf("recall from %s: %w", request.StorePaths[opened[0].index], errorValue)
	}
	layers := make([]int, len(recalled.Memories))
	for index, entry := range recalled.Memories {
		layers[index] = opened[0].index
		if layer, isBeneath := found[entry.Memory.MemoryID]; isBeneath {
			layers[index] = layer
		}
	}
	return json.NewEncoder(output).Encode(ReadResult{Recall: recalled, Layers: layers})
}

type openedLayer struct {
	store *bluememo.Store
	index int
}

func openToRead(ctx context.Context, request ReadRequest) ([]openedLayer, error) {
	opened := []openedLayer{}
	for index, path := range request.StorePaths {
		if _, errorValue := os.Stat(path); errors.Is(errorValue, os.ErrNotExist) {
			continue
		}
		store, errorValue := bluememo.OpenToRead(ctx, path, bluememo.Configuration{
			Embedder:       carriedVector{vector: request.QueryVector},
			EmbeddingModel: request.EmbeddingModel,
			LaneDepth:      request.LaneDepth,
			RecallSources:  request.RecallSources,
		})
		if errorValue != nil {
			return opened, fmt.Errorf("open %s: %w", path, errorValue)
		}
		opened = append(opened, openedLayer{store: store, index: index})
	}
	return opened, nil
}

func closeAll(opened []openedLayer) {
	for _, layer := range opened {
		layer.store.Close()
	}
}

type recordingLayer struct {
	layer openedLayer
	found map[string]int
}

func (recording recordingLayer) Search(ctx context.Context, query string, limit int) ([]bluememo.RecalledMemory, error) {
	found, errorValue := recording.layer.store.Search(ctx, query, limit)
	for _, entry := range found {
		recording.found[entry.Memory.MemoryID] = recording.layer.index
	}
	return found, errorValue
}

func decodeReadRequest(input io.Reader) (ReadRequest, error) {
	var request ReadRequest
	if errorValue := json.NewDecoder(input).Decode(&request); errorValue != nil {
		return ReadRequest{}, fmt.Errorf("read request: %w", errorValue)
	}
	if len(request.StorePaths) == 0 || strings.TrimSpace(strings.Join(request.StorePaths, "")) == "" {
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
