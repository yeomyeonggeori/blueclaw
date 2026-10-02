package memory_test

import (
	"context"
	"os"
	"strings"
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

func TestASubjectsMemorySitsInTheirOwnWorkspaceUnderProtected(t *testing.T) {
	stores := memorytest.Open(t)
	for name, expectation := range map[string]struct {
		scope  memory.Scope
		suffix string
	}{
		"a person": {memory.PersonScope("person-1"), "/private/people/person-1/.protected/memory.db"},
		"a circle": {memory.CircleScope("member"), "/circles/member/.protected/memory.db"},
		"everyone": {memory.WorkspaceScope(), "/shared/.protected/memory.db"},
	} {
		t.Run(name, func(t *testing.T) {
			path, errorValue := stores.Path(expectation.scope)
			if errorValue != nil {
				t.Fatalf("path: %v", errorValue)
			}
			if !strings.HasSuffix(path, expectation.suffix) {
				t.Fatalf("memory for %s sits at %q, which does not end in %q", name, path, expectation.suffix)
			}
		})
	}
}

func TestAMemoryFileIsReadableByItsSubjectAndWritableByNobodyElse(t *testing.T) {
	stores := memorytest.Open(t)
	scope := memory.PersonScope("person-1")
	memorytest.Remember(t, stores, scope, "박예시 keeps the quarterly ledger")

	path, errorValue := stores.Path(scope)
	if errorValue != nil {
		t.Fatalf("path: %v", errorValue)
	}
	information, errorValue := os.Stat(path)
	if errorValue != nil {
		t.Fatalf("stat: %v", errorValue)
	}
	if mode := information.Mode().Perm(); mode != 0o640 {
		t.Fatalf("a memory file carries mode %04o; the subject's group must read it and only the service write it", mode)
	}
}
