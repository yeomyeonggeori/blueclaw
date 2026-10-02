package memory_test

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
)

func TestMergePersonRenamesTheLosingFileWhenTheSurvivorHasNone(t *testing.T) {
	stores := memorytest.Open(t)
	ctx := context.Background()
	memorytest.Remember(t, stores, memory.PersonScope("legacy-person"), "이샘플 prefers the morning slot.")

	if errorValue := stores.MergePerson(ctx, "legacy-person", "person-1"); errorValue != nil {
		t.Fatalf("merge: %v", errorValue)
	}

	if held := memorytest.Count(t, stores, memory.PersonScope("person-1")); held != 1 {
		t.Errorf("the survivor holds %d memories, want the one that moved", held)
	}
	if left := memorytest.Count(t, stores, memory.PersonScope("legacy-person")); left != 0 {
		t.Errorf("the merged-away person still holds %d memories, want none", left)
	}
}

func TestMergePersonCarriesTheMemoriesWhenBothFilesExist(t *testing.T) {
	stores := memorytest.Open(t)
	ctx := context.Background()
	memorytest.Remember(t, stores, memory.PersonScope("legacy-person"), "이샘플 prefers the morning slot.")
	memorytest.Remember(t, stores, memory.PersonScope("person-1"), "이샘플 chairs the safety review.")

	if errorValue := stores.MergePerson(ctx, "legacy-person", "person-1"); errorValue != nil {
		t.Fatalf("merge: %v", errorValue)
	}

	if held := memorytest.Count(t, stores, memory.PersonScope("person-1")); held != 2 {
		t.Errorf("the survivor holds %d memories, want both", held)
	}
	if left := memorytest.Count(t, stores, memory.PersonScope("legacy-person")); left != 0 {
		t.Errorf("the merged-away person still holds %d memories, want none", left)
	}
}

func TestMergePersonIsQuietWhenThereIsNothingToMove(t *testing.T) {
	stores := memorytest.Open(t)
	ctx := context.Background()

	if errorValue := stores.MergePerson(ctx, "legacy-person", "person-1"); errorValue != nil {
		t.Fatalf("merge with no file: %v", errorValue)
	}
	if errorValue := stores.MergePerson(ctx, "person-1", "person-1"); errorValue != nil {
		t.Fatalf("merge into itself: %v", errorValue)
	}
}
