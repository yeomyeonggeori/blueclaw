package memory_test

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
	"github.com/yeomyeonggeori/bluememo"
)

func TestAReaderWhoMayOnlyReadRecallsThroughEveryLayer(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes every file, so nothing here would be read-only")
	}
	stores := memorytest.Open(t)
	person, company := memory.PersonScope("person-1"), memory.WorkspaceScope()
	memorytest.Remember(t, stores, person, "이샘플 prefers terse release notes")
	memorytest.Remember(t, stores, company, "The release notes go out every Friday")
	for _, scope := range []memory.Scope{person, company} {
		withoutWriting(t, pathOf(t, stores, scope))
	}

	facts := memorytest.Recall(t, stores, []memory.Scope{person, company}, "release notes")

	if scopeOf(facts, "이샘플 prefers terse release notes") != memory.ScopePerson ||
		scopeOf(facts, "The release notes go out every Friday") != memory.ScopeWorkspace {
		t.Fatalf("a read-only reader recalled %+v", facts)
	}
	if held := memoryOf(t, stores, person); held.LastRecalledAt.IsZero() {
		t.Fatal("what a reader recalled was not reinforced by the service that keeps it")
	}
}

func TestWhatTheCompanyAlreadyKnowsIsNotRememberedAgainForAPerson(t *testing.T) {
	stores := memorytest.OpenJudgingSame(t)
	person, company := memory.PersonScope("person-1"), memory.WorkspaceScope()
	memorytest.Remember(t, stores, company, "The office closes at seven")

	stack := memory.StackToRemember(person, []memory.Scope{person, company})
	note := bluememo.Note{GroupID: memory.NewIdentifier(), Body: "The office closes at seven", IsExplicit: true}
	if _, errorValue := stores.Remember(context.Background(), stack, note); errorValue != nil {
		t.Fatalf("remember: %v", errorValue)
	}

	if count := memorytest.Count(t, stores, person); count != 0 {
		t.Fatalf("a person's memory took %d copies of what the company already knows", count)
	}
}

func TestAMemoryStandsOnlyOnScopesBroaderThanItself(t *testing.T) {
	person, circle, company := memory.PersonScope("person-1"), memory.CircleScope("leadership"), memory.WorkspaceScope()
	searched := []memory.Scope{person, circle, company}

	if stack := memory.StackToRemember(person, searched); !slices.Equal(stack, searched) {
		t.Fatalf("a person's memory stands on %v", stack)
	}
	if stack := memory.StackToRemember(circle, searched); !slices.Equal(stack, []memory.Scope{circle, company}) {
		t.Fatalf("a circle's memory stands on %v", stack)
	}
	if stack := memory.StackToRemember(company, searched); !slices.Equal(stack, []memory.Scope{company}) {
		t.Fatalf("the company's memory stands on %v", stack)
	}
}

func withoutWriting(t *testing.T, storePath string) {
	t.Helper()
	for _, path := range []string{storePath, storePath + "-wal", storePath + "-shm"} {
		if errorValue := os.Chmod(path, 0o444); errorValue != nil {
			t.Fatalf("chmod %s: %v", path, errorValue)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o640) })
	}
}

func scopeOf(facts []memory.MemoryFact, content string) string {
	for _, fact := range facts {
		if fact.Content == content {
			return fact.ScopeType
		}
	}
	return ""
}

func memoryOf(t *testing.T, stores *memory.Stores, scope memory.Scope) bluememo.Memory {
	t.Helper()
	store, errorValue := stores.Store(context.Background(), scope)
	if errorValue != nil {
		t.Fatalf("open %s: %v", scope.Kind, errorValue)
	}
	memories, errorValue := store.Memories(context.Background())
	if errorValue != nil || len(memories) != 1 {
		t.Fatalf("%s holds %d memories (%v)", scope.Kind, len(memories), errorValue)
	}
	return memories[0]
}
