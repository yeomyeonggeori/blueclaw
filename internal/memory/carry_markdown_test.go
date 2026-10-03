package memory_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
)

const sampleMarkdownMemory = `# Memory

## Preferences
- 이샘플 prefers replies in Korean.
- Weekly reports go out on Fridays.

Some framing the compressor wrote.
`

func writeMarkdownMemory(t *testing.T, root string, personID string) string {
	t.Helper()
	directory := filepath.Join(root, ".blueclaw", "memory", "people", personID)
	if errorValue := os.MkdirAll(directory, 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
	path := filepath.Join(directory, "MEMORY.md")
	if errorValue := os.WriteFile(path, []byte(sampleMarkdownMemory), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	return path
}

func everyoneIsOnTheRoster(string) bool { return true }

func heldContents(t *testing.T, stores *memory.Stores, personID string) []string {
	t.Helper()
	store, errorValue := stores.Store(context.Background(), memory.PersonScope(personID))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	memories, errorValue := store.Memories(context.Background())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	contents := []string{}
	for _, held := range memories {
		contents = append(contents, held.Content)
	}
	slices.Sort(contents)
	return contents
}

func TestAMarkdownMemoryBecomesThePersonsMemories(t *testing.T) {
	stores, root := memorytest.OpenWithRoot(t)
	path := writeMarkdownMemory(t, root, "person-1")

	report, errorValue := stores.CarryMarkdownMemory(context.Background(), everyoneIsOnTheRoster)
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if report.Carried != 1 {
		t.Fatalf("report %+v, want one file carried", report)
	}
	want := []string{"Weekly reports go out on Fridays.", "이샘플 prefers replies in Korean."}
	if contents := heldContents(t, stores, "person-1"); !slices.Equal(contents, want) {
		t.Fatalf("the store holds %q, want the bullets %q", contents, want)
	}
	if _, errorValue := os.Stat(path + ".carried"); errorValue != nil {
		t.Fatalf("the carried file was not kept beside its old name: %v", errorValue)
	}
}

func TestCarryingAMarkdownMemoryTwiceHoldsEachFactOnce(t *testing.T) {
	stores, root := memorytest.OpenWithRoot(t)
	path := writeMarkdownMemory(t, root, "person-1")
	if _, errorValue := stores.CarryMarkdownMemory(context.Background(), everyoneIsOnTheRoster); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.Rename(path+".carried", path); errorValue != nil {
		t.Fatal(errorValue)
	}

	if _, errorValue := stores.CarryMarkdownMemory(context.Background(), everyoneIsOnTheRoster); errorValue != nil {
		t.Fatal(errorValue)
	}

	if contents := heldContents(t, stores, "person-1"); len(contents) != 2 {
		t.Fatalf("the store holds %q after two carries, want two facts", contents)
	}
}

func TestTheMarkdownMemoryOfSomebodyOffTheRosterIsLeft(t *testing.T) {
	stores, root := memorytest.OpenWithRoot(t)
	path := writeMarkdownMemory(t, root, "person-departed")

	report, errorValue := stores.CarryMarkdownMemory(context.Background(), func(string) bool { return false })
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if report.Carried != 0 || len(report.Left) != 1 || report.Left[0] != path {
		t.Fatalf("report %+v; a file nobody on the roster owns must be left and named", report)
	}
	if _, errorValue := os.Stat(path); errorValue != nil {
		t.Fatalf("the file was moved: %v", errorValue)
	}
}
