package llm

import (
	"context"
	"fmt"

	"github.com/yeomyeonggeori/blueprotocol/model/openaicompatible"
)

type fixedWidthEmbedder struct {
	provider   *openaicompatible.EmbeddingProvider
	dimensions int
}

func (embedder fixedWidthEmbedder) EmbeddingModelName() string {
	return embedder.provider.ModelName()
}

func (embedder fixedWidthEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	embedding, errorValue := embedder.provider.EmbedQuery(ctx, text)
	return embedder.checked(embedding, errorValue)
}

func (embedder fixedWidthEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	embeddings, errorValue := embedder.provider.EmbedDocuments(ctx, texts)
	if errorValue != nil {
		return nil, errorValue
	}
	for _, embedding := range embeddings {
		if _, errorValue := embedder.checked(embedding, nil); errorValue != nil {
			return nil, errorValue
		}
	}
	return embeddings, nil
}

func (embedder fixedWidthEmbedder) checked(embedding []float32, errorValue error) ([]float32, error) {
	if errorValue != nil {
		return nil, errorValue
	}
	if len(embedding) != embedder.dimensions {
		return nil, fmt.Errorf("the embedding endpoint answered %d dimensions for model %s, but memory and skills are configured for %d", len(embedding), embedder.provider.ModelName(), embedder.dimensions)
	}
	return embedding, nil
}
