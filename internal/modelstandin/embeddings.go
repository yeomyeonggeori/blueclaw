package modelstandin

import (
	"encoding/json"
	"net/http"
)

type embeddingRequestDocument struct {
	Model      string          `json:"model"`
	Input      json.RawMessage `json:"input"`
	Dimensions int             `json:"dimensions"`
}

const unrequestedDimensionCount = 3

func (server *Server) serveEmbeddings(writer http.ResponseWriter, request *http.Request) {
	var document embeddingRequestDocument
	if errorValue := json.NewDecoder(request.Body).Decode(&document); errorValue != nil {
		http.Error(writer, "an embedding request is JSON: "+errorValue.Error(), http.StatusBadRequest)
		return
	}
	server.record(Ask{Kind: AskKindEmbedding, Model: document.Model, Authorization: request.Header.Get("Authorization")})
	embeddings := []map[string]any{}
	for index := range embeddedInputCount(document.Input) {
		embeddings = append(embeddings, map[string]any{"index": index, "embedding": unitVector(document.Dimensions)})
	}
	writeJSON(writer, map[string]any{"data": embeddings, "model": document.Model})
}

func embeddedInputCount(input json.RawMessage) int {
	var inputs []json.RawMessage
	if json.Unmarshal(input, &inputs) == nil {
		return len(inputs)
	}
	return 1
}

func unitVector(dimensionCount int) []float64 {
	if dimensionCount <= 0 {
		dimensionCount = unrequestedDimensionCount
	}
	vector := make([]float64, dimensionCount)
	vector[0] = 1
	return vector
}
