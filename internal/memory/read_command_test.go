package memory_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
	"github.com/yeomyeonggeori/bluememo"
	"github.com/yeomyeonggeori/bluememo/bluememotest"
)

func TestAReadAnswersFromTheFileWithNothingButACarriedVector(t *testing.T) {
	stores := memorytest.Open(t)
	scope := memory.PersonScope("person-1")
	memorytest.Remember(t, stores, scope, "박예시 keeps the quarterly ledger")

	result := readFrom(t, stores, scope, "who keeps the ledger")

	if len(result.Memories) == 0 {
		t.Fatal("a read of a store that holds a memory returned none")
	}
	if !strings.Contains(result.Memories[0].Memory.Content, "ledger") {
		t.Fatalf("a read answered with something else: %q", result.Memories[0].Memory.Content)
	}
}

func TestAFileTheReadCannotOpenFailsRatherThanLookingEmpty(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens every file, so the kernel decides nothing here")
	}
	stores := memorytest.Open(t)
	scope := memory.PersonScope("person-1")
	memorytest.Remember(t, stores, scope, "박예시 keeps the quarterly ledger")
	path := pathOf(t, stores, scope)
	if errorValue := stores.Close(); errorValue != nil {
		t.Fatalf("close: %v", errorValue)
	}
	if errorValue := os.Chmod(path, 0o000); errorValue != nil {
		t.Fatalf("chmod: %v", errorValue)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	var output bytes.Buffer
	errorValue := memory.RunRead(context.Background(), requestFor(t, path, "who keeps the ledger"), &output)

	if errorValue == nil {
		t.Fatalf("a read of a file this process may not open reported success: %s", output.String())
	}
	if output.Len() != 0 {
		t.Fatalf("a refused read still wrote a result: %s", output.String())
	}
}

func TestAReadWithoutAPathOrAVectorIsRefused(t *testing.T) {
	for name, request := range map[string]memory.ReadRequest{
		"no path":   {QueryVector: []float32{0.1}},
		"no vector": {StorePath: "/tmp/absent.db"},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, errorValue := json.Marshal(request)
			if errorValue != nil {
				t.Fatalf("marshal: %v", errorValue)
			}
			if errorValue := memory.RunRead(context.Background(), bytes.NewReader(encoded), &bytes.Buffer{}); errorValue == nil {
				t.Fatal("an incomplete read request was accepted")
			}
		})
	}
}

func readFrom(t *testing.T, stores *memory.Stores, scope memory.Scope, query string) bluememo.RecallResult {
	t.Helper()
	var output bytes.Buffer
	if errorValue := memory.RunRead(context.Background(), requestFor(t, pathOf(t, stores, scope), query), &output); errorValue != nil {
		t.Fatalf("read: %v", errorValue)
	}
	var result bluememo.RecallResult
	if errorValue := json.Unmarshal(output.Bytes(), &result); errorValue != nil {
		t.Fatalf("decode read result: %v", errorValue)
	}
	return result
}

func pathOf(t *testing.T, stores *memory.Stores, scope memory.Scope) string {
	t.Helper()
	path, errorValue := stores.Path(scope)
	if errorValue != nil {
		t.Fatalf("path for %v: %v", scope, errorValue)
	}
	return path
}

func requestFor(t *testing.T, path string, query string) *bytes.Reader {
	t.Helper()
	vector, errorValue := (&bluememotest.HashEmbedder{}).EmbedQuery(context.Background(), query)
	if errorValue != nil {
		t.Fatalf("embed the query: %v", errorValue)
	}
	encoded, errorValue := json.Marshal(memory.ReadRequest{
		StorePath:      path,
		Query:          query,
		QueryVector:    vector,
		EmbeddingModel: "test-embed",
		Limit:          10,
	})
	if errorValue != nil {
		t.Fatalf("marshal the read request: %v", errorValue)
	}
	return bytes.NewReader(encoded)
}
