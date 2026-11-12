package memorytest

import (
	"context"
	"sync"

	"github.com/yeomyeonggeori/bluememo/bluememotest"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

// RequesterRecordingEmbedder embeds as Open's embedder does and keeps who each
// call was made for, for a test that checks a remembering call names its
// requester.
type RequesterRecordingEmbedder struct {
	bluememotest.HashEmbedder
	mutex      sync.Mutex
	requesters []model.RequestContext
}

func (embedder *RequesterRecordingEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	embedder.mutex.Lock()
	embedder.requesters = append(embedder.requesters, model.RequestContextFromContext(ctx))
	embedder.mutex.Unlock()
	return embedder.HashEmbedder.EmbedDocuments(ctx, texts)
}

func (embedder *RequesterRecordingEmbedder) Requesters() []model.RequestContext {
	embedder.mutex.Lock()
	defer embedder.mutex.Unlock()
	return append([]model.RequestContext{}, embedder.requesters...)
}
