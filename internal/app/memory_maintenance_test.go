package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluememo"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
	runtimelogging "github.com/yeomyeonggeori/blueclaw/internal/runtime"
)

type embeddingRequestRecorder struct {
	mutex    sync.Mutex
	requests []map[string]any
}

func (recorder *embeddingRequestRecorder) handler(t *testing.T) http.HandlerFunc {
	return func(responseWriter http.ResponseWriter, request *http.Request) {
		var received map[string]any
		if errorValue := json.NewDecoder(request.Body).Decode(&received); errorValue != nil {
			t.Error(errorValue)
		}
		recorder.mutex.Lock()
		recorder.requests = append(recorder.requests, received)
		recorder.mutex.Unlock()
		inputs, _ := received["input"].([]any)
		embeddings := make([][]float64, len(inputs))
		for index := range embeddings {
			embeddings[index] = make([]float64, llm.DefaultEmbeddingDimensions)
			embeddings[index][0] = 1
		}
		_ = json.NewEncoder(responseWriter).Encode(map[string]any{"embeddings": embeddings})
	}
}

func TestStartupMaintenanceReembedsMemoriesWrittenUnderAnotherModel(t *testing.T) {
	stores, root := memorytest.OpenEmbeddedBy(t, "baai/bge-m3")
	memorytest.Remember(t, stores, memory.WorkspaceScope(), "이샘플 keeps the quarterly ledger")
	if errorValue := stores.Close(); errorValue != nil {
		t.Fatal(errorValue)
	}
	recorder := &embeddingRequestRecorder{}
	server := httptest.NewServer(recorder.handler(t))
	defer server.Close()
	reopened := memory.NewStores(root, bluememo.Configuration{
		Embedder: llm.CapabilityEmbeddingClient{
			CapabilityClient: capability.Client{Endpoint: server.URL, HTTPClient: server.Client()},
			ModelName:        llm.DefaultEmbeddingModelName,
			OutputDimensions: llm.DefaultEmbeddingDimensions,
		},
	}, memorytest.ReadsInThisProcess())
	t.Cleanup(func() { _ = reopened.Close() })
	application := &Application{
		memoryStores:  reopened,
		runtimeLogger: &runtimelogging.PersistentLogger{Logger: slog.Default()},
	}

	application.startMemoryMaintenance()
	t.Cleanup(application.memoryMaintenanceCancel)

	waitForNoStaleMemory(t, reopened)
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if len(recorder.requests) == 0 || recorder.requests[0]["model"] != "google/embeddinggemma-2" || recorder.requests[0]["inputType"] != llm.EmbeddingInputTypeDocument || recorder.requests[0]["outputDimensions"] != float64(768) {
		t.Fatalf("expected a document embedding by embeddinggemma at 768 dimensions, got %v", recorder.requests)
	}
}

func waitForNoStaleMemory(t *testing.T, stores *memory.Stores) {
	t.Helper()
	store, errorValue := stores.Store(context.Background(), memory.WorkspaceScope())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		state, errorValue := store.IndexState(context.Background())
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if state.Stale == 0 && state.Current > 0 {
			if state.EmbeddingModel != "google/embeddinggemma-2" {
				t.Fatalf("expected the index to be current for embeddinggemma, got %q", state.EmbeddingModel)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("startup maintenance left memories embedded by the previous model")
}
