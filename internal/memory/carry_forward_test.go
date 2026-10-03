package memory_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
	"github.com/yeomyeonggeori/bluememo"
	"github.com/yeomyeonggeori/bluememo/bluememotest"
)

func oldPathOf(t *testing.T, root string, parts ...string) string {
	t.Helper()
	directory := filepath.Join(append([]string{root, ".blueclaw", "memory"}, parts[:len(parts)-1]...)...)
	if errorValue := os.MkdirAll(directory, 0o700); errorValue != nil {
		t.Fatalf("make the old directory: %v", errorValue)
	}
	return filepath.Join(directory, parts[len(parts)-1])
}

// writeOldStore leaves a store at the old path holding one memory, closed, so
// its log is folded into the one file a cleanly stopped host leaves behind.
func writeOldStore(t *testing.T, path string, content string) {
	t.Helper()
	store, errorValue := bluememo.Open(context.Background(), path, bluememo.Configuration{Embedder: bluememotest.HashEmbedder{}, EmbeddingModel: "carry-test"})
	if errorValue != nil {
		t.Fatalf("open the old store: %v", errorValue)
	}
	if _, errorValue := store.Adopt(context.Background(), []bluememo.AdoptedMemory{{
		Memory: bluememo.Memory{MemoryID: bluememo.NewIdentifier(), Content: content},
	}}); errorValue != nil {
		t.Fatalf("write the old store: %v", errorValue)
	}
	if errorValue := store.Close(); errorValue != nil {
		t.Fatalf("close the old store: %v", errorValue)
	}
}

func recalledContent(t *testing.T, stores *memory.Stores, scope memory.Scope, query string) []string {
	t.Helper()
	path, errorValue := stores.Path(scope)
	if errorValue != nil {
		t.Fatalf("where the memory belongs: %v", errorValue)
	}
	store, errorValue := bluememo.Open(context.Background(), path, bluememo.Configuration{Embedder: bluememotest.HashEmbedder{}, EmbeddingModel: "carry-test"})
	if errorValue != nil {
		t.Fatalf("open the carried store: %v", errorValue)
	}
	defer store.Close()
	recalled, errorValue := store.Recall(context.Background(), query, 10)
	if errorValue != nil {
		t.Fatalf("recall from the carried store: %v", errorValue)
	}
	contents := []string{}
	for _, held := range recalled.Memories {
		contents = append(contents, held.Memory.Content)
	}
	return contents
}

func TestMemoryWrittenAtTheOldPathIsReadableAtTheNewOne(t *testing.T) {
	stores, root := memorytest.OpenWithRoot(t)
	writeOldStore(t, oldPathOf(t, root, "persons", "person-1.db"), "the quarterly ledger lives in Numbers")
	writeOldStore(t, oldPathOf(t, root, "circles", "member.db"), "the all-hands is on Thursday")
	writeOldStore(t, oldPathOf(t, root, "workspace.db"), "the office closes at seven")

	report, errorValue := stores.CarryForward(context.Background())
	if errorValue != nil {
		t.Fatalf("carry the memory forward: %v", errorValue)
	}
	if report.Carried != 3 {
		t.Fatalf("carried %d of the three stores, report %+v", report.Carried, report)
	}
	for _, carried := range []struct {
		scope   memory.Scope
		query   string
		content string
	}{
		{memory.PersonScope("person-1"), "where is the ledger", "the quarterly ledger lives in Numbers"},
		{memory.CircleScope("member"), "when is the all-hands", "the all-hands is on Thursday"},
		{memory.WorkspaceScope(), "when does the office close", "the office closes at seven"},
	} {
		contents := recalledContent(t, stores, carried.scope, carried.query)
		if len(contents) != 1 || contents[0] != carried.content {
			t.Fatalf("%s holds %q, wanted %q", carried.scope.Kind, contents, carried.content)
		}
	}
}

func TestWhatOnlyTheLogHeldIsCarriedRatherThanTruncatedAway(t *testing.T) {
	stores, root := memorytest.OpenWithRoot(t)
	staging := filepath.Join(t.TempDir(), "memory.db")
	store, errorValue := bluememo.Open(context.Background(), staging, bluememo.Configuration{Embedder: bluememotest.HashEmbedder{}, EmbeddingModel: "carry-test"})
	if errorValue != nil {
		t.Fatalf("open the staging store: %v", errorValue)
	}
	if _, errorValue := store.Adopt(context.Background(), []bluememo.AdoptedMemory{{
		Memory: bluememo.Memory{MemoryID: bluememo.NewIdentifier(), Content: "the audit was filed on a Friday"},
	}}); errorValue != nil {
		t.Fatalf("write the staging store: %v", errorValue)
	}

	// Copy while it is still open, so the old path holds what a host killed
	// mid-write leaves: a database whose recent writes live only in its log.
	oldPath := oldPathOf(t, root, "persons", "person-1.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		bytes, errorValue := os.ReadFile(staging + suffix)
		if errorValue != nil {
			t.Fatalf("read the staging %s: %v", suffix, errorValue)
		}
		if errorValue := os.WriteFile(oldPath+suffix, bytes, 0o600); errorValue != nil {
			t.Fatalf("stage the old %s: %v", suffix, errorValue)
		}
	}
	store.Close()

	if _, errorValue := stores.CarryForward(context.Background()); errorValue != nil {
		t.Fatalf("carry the memory forward: %v", errorValue)
	}
	contents := recalledContent(t, stores, memory.PersonScope("person-1"), "when was the audit filed")
	if len(contents) != 1 || contents[0] != "the audit was filed on a Friday" {
		t.Fatalf("the carried store holds %q, so what only the log held was lost", contents)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, errorValue := os.Stat(oldPath + suffix); !os.IsNotExist(errorValue) {
			t.Fatalf("%s was left at the old path", filepath.Base(oldPath+suffix))
		}
	}
}

func TestAnOldFileIsFoldedIntoTheFileAlreadyThere(t *testing.T) {
	stores, root := memorytest.OpenWithRoot(t)
	oldPath := oldPathOf(t, root, "persons", "person-1.db")
	writeOldStore(t, oldPath, "what the old path holds")
	memorytest.Remember(t, stores, memory.PersonScope("person-1"), "what the new path holds")

	report, errorValue := stores.CarryForward(context.Background())
	if errorValue != nil {
		t.Fatalf("carry the memory forward: %v", errorValue)
	}
	if report.Carried != 0 || report.Adopted != 1 {
		t.Fatalf("report %+v, wanted one file folded in", report)
	}
	held := heldContent(t, stores, memory.PersonScope("person-1"))
	if !held["what the old path holds"] || !held["what the new path holds"] {
		t.Fatalf("the store holds %v, wanted what both paths held", held)
	}
	if _, errorValue := os.Stat(oldPath); !os.IsNotExist(errorValue) {
		t.Fatal("the old file was left behind once its memory was folded in")
	}
}

func heldContent(t *testing.T, stores *memory.Stores, scope memory.Scope) map[string]bool {
	t.Helper()
	store, errorValue := stores.Store(context.Background(), scope)
	if errorValue != nil {
		t.Fatalf("open the store: %v", errorValue)
	}
	memories, errorValue := store.Memories(context.Background())
	if errorValue != nil {
		t.Fatalf("read the store: %v", errorValue)
	}
	held := map[string]bool{}
	for _, memory := range memories {
		held[memory.Content] = true
	}
	return held
}

func TestCarryingForwardTwiceCarriesNothingTheSecondTime(t *testing.T) {
	stores, root := memorytest.OpenWithRoot(t)
	writeOldStore(t, oldPathOf(t, root, "persons", "person-1.db"), "the ledger lives in Numbers")

	first, errorValue := stores.CarryForward(context.Background())
	if errorValue != nil || first.Carried != 1 {
		t.Fatalf("first carry %+v: %v", first, errorValue)
	}
	second, errorValue := stores.CarryForward(context.Background())
	if errorValue != nil {
		t.Fatalf("second carry: %v", errorValue)
	}
	if second.Carried != 0 || second.Adopted != 0 {
		t.Fatalf("second carry %+v, wanted nothing to do", second)
	}
}

func TestAHostThatNeverWroteAtTheOldPathCarriesNothing(t *testing.T) {
	stores, _ := memorytest.OpenWithRoot(t)
	report, errorValue := stores.CarryForward(context.Background())
	if errorValue != nil {
		t.Fatalf("a fresh host is not a failure: %v", errorValue)
	}
	if report.Carried != 0 || report.Adopted != 0 {
		t.Fatalf("report %+v, wanted nothing to do", report)
	}
}

func TestTheAdminCirclesOldFileIsLeftWhereItIs(t *testing.T) {
	stores, root := memorytest.OpenWithRoot(t)
	oldPath := oldPathOf(t, root, "circles", "admin.db")
	writeOldStore(t, oldPath, "the board meets on the first Monday")

	report, errorValue := stores.CarryForward(context.Background())
	if errorValue != nil {
		t.Fatalf("carry the memory forward: %v", errorValue)
	}
	if report.Carried != 0 || len(report.Left) != 1 || report.Left[0] != oldPath {
		t.Fatalf("report %+v; the admin circle has no directory to carry into", report)
	}
	if _, errorValue := os.Stat(filepath.Join(root, "circles", "admin")); !os.IsNotExist(errorValue) {
		t.Fatalf("circles/admin was made (%v); nothing sets up its group, so nobody could read it", errorValue)
	}
}
