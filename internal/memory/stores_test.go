package memory_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/bluememo"
	"github.com/yeomyeonggeori/bluememo/bluememotest"
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
		"a person": {memory.PersonScope("person-1"), "/private/protected/person-1/memory.db"},
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

func TestAMemoryNobodyWroteRecallsNothingRatherThanFailing(t *testing.T) {
	stores := memorytest.Open(t)

	recalled, errorValue := stores.RecallAcross(
		context.Background(),
		policy.PersonAccess{PersonID: "person-nobody-wrote-for"},
		[]memory.Scope{memory.PersonScope("person-nobody-wrote-for"), memory.WorkspaceScope()},
		"anything",
		10,
	)

	if errorValue != nil {
		t.Fatalf("a recall of files nobody has written failed: %v", errorValue)
	}
	if len(recalled.Facts) != 0 {
		t.Fatalf("a recall of files nobody has written answered with %d facts", len(recalled.Facts))
	}
}

func TestARefusedReadFailsTheRecallRatherThanThinningIt(t *testing.T) {
	stores := memory.NewStores(t.TempDir(), bluememo.Configuration{
		Embedder: &bluememotest.HashEmbedder{},
	}, refusingActor{})
	t.Cleanup(func() { _ = stores.Close() })

	_, errorValue := stores.RecallAcross(
		context.Background(),
		policy.PersonAccess{PersonID: "person-1"},
		[]memory.Scope{memory.PersonScope("person-1")},
		"anything",
		10,
	)

	if errorValue == nil {
		t.Fatal("a recall whose read was refused reported success, so a refusal would read as an empty memory")
	}
	if !strings.Contains(errorValue.Error(), "refused") {
		t.Fatalf("a refused recall failed with something else: %v", errorValue)
	}
}

type refusingActor struct {
	security.WorkspaceActor
}

func (refusingActor) Requester(context.Context, security.WorkspaceActorRequest) (security.WorkspaceActor, error) {
	return refusingActor{}, nil
}

func (refusingActor) CanListDirectory(context.Context) bool { return false }

func (refusingActor) Run(context.Context, security.CommandRequest) (security.CommandResult, error) {
	return security.CommandResult{ExitCode: 1, Stderr: "permission denied"}, nil
}

func TestEveryScopeAnAdministratorReachesSitsInADeclaredDirectory(t *testing.T) {
	stores, root := memorytest.OpenWithRoot(t)
	administrator := policy.PersonPolicy{PersonID: "person-1", Circles: []string{"sales"}, IsAdmin: true}
	document := policy.CanonicalizePolicyDocument(policy.PolicyDocument{
		People:  []policy.PersonPolicy{administrator},
		Circles: []policy.CirclePolicy{{CircleID: "sales"}},
	})
	declared := map[string]bool{}
	const hostRoot = "/workspace"
	for _, directory := range security.POSIXStateForPolicy(document, hostRoot).Directories {
		declared[directory.Path] = true
	}
	personAccess := policy.PolicyProjectionService{}.ReplacePolicyProjectionTransactionally(document).PersonAccessByPersonID["person-1"]

	scopes := memory.ScopesToSearch(personAccess, nil)
	for _, circleID := range personAccess.Circles {
		scopes = append(scopes, memory.ScopeToRemember("person-1", circleID))
	}
	for _, scope := range scopes {
		path, errorValue := stores.Path(scope)
		if errorValue != nil {
			t.Fatalf("path for %+v: %v", scope, errorValue)
		}
		directory := strings.Replace(filepath.Dir(path), root, hostRoot, 1)
		if !declared[directory] {
			t.Fatalf("memory for %s %q sits in %s, which no POSIX declaration sets up, so the person cannot open it", scope.Kind, scope.ID, directory)
		}
	}
}

func TestWhatIsSaidUnderTheAdminCircleIsRememberedForThePerson(t *testing.T) {
	if scope := memory.ScopeToRemember("person-1", "admin"); scope != memory.PersonScope("person-1") {
		t.Fatalf("remembered under %+v; the admin circle has no memory of its own", scope)
	}
	if scope := memory.ScopeToRemember("person-1", "Sales"); scope != memory.CircleScope("sales") {
		t.Fatalf("remembered under %+v; a circle with a directory keeps what is said in it", scope)
	}
}
