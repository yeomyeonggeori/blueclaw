package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

type recordedWorkspaceActorRequest struct {
	personID          string
	circles           []string
	workspaceRootPath string
}

type stubWorkspaceActorFactory struct {
	cannotListDirectory bool
	entries             []security.WorkspaceActorDirectoryEntry
	fileContent         []byte
	recordedRequests    []recordedWorkspaceActorRequest
}

type stubWorkspaceActor struct {
	factory *stubWorkspaceActorFactory
}

func (factory *stubWorkspaceActorFactory) CanListDirectory(context.Context) bool {
	return !factory.cannotListDirectory
}

func (factory *stubWorkspaceActorFactory) Requester(_ context.Context, request security.WorkspaceActorRequest) (security.WorkspaceActor, error) {
	factory.recordedRequests = append(factory.recordedRequests, recordedWorkspaceActorRequest{
		personID:          request.PersonAccess.PersonID,
		circles:           request.PersonAccess.Circles,
		workspaceRootPath: request.WorkspaceRootPath,
	})
	return stubWorkspaceActor{factory: factory}, nil
}

func (actor stubWorkspaceActor) Run(context.Context, security.CommandRequest) (security.CommandResult, error) {
	return security.CommandResult{}, nil
}

func (actor stubWorkspaceActor) MkdirAll(context.Context, string) error { return nil }

func (actor stubWorkspaceActor) WriteFile(context.Context, string, []byte) error { return nil }

func (actor stubWorkspaceActor) ReadFile(context.Context, string, int64) ([]byte, error) {
	return actor.factory.fileContent, nil
}

func (actor stubWorkspaceActor) BundleDirectory(context.Context, string, security.WorkspaceActorBundleOptions) (security.WorkspaceActorBundle, error) {
	return security.WorkspaceActorBundle{}, nil
}

func (actor stubWorkspaceActor) ListDirectory(context.Context, string) ([]security.WorkspaceActorDirectoryEntry, error) {
	return actor.factory.entries, nil
}

func (actor stubWorkspaceActor) Stat(_ context.Context, path string) (security.WorkspaceActorStat, error) {
	return security.WorkspaceActorStat{Path: path, IsRegular: true, SizeBytes: int64(len(actor.factory.fileContent))}, nil
}

type stubPersonAccessResolver struct{}

func (stubPersonAccessResolver) ResolvePersonAccess(personID string) policy.PersonAccess {
	return policy.PersonAccess{PersonID: personID, Circles: []string{"member"}}
}

func newWorkspaceFilesTestHandler(factory *stubWorkspaceActorFactory, workspaceRootPath string) WorkspaceFilesHandler {
	return WorkspaceFilesHandler{
		WorkspaceRootPath:     workspaceRootPath,
		WorkspaceActorFactory: factory,
		PersonAccessResolver:  stubPersonAccessResolver{},
	}
}

func decodeWorkspaceListEntries(t *testing.T, recorder *httptest.ResponseRecorder) []workspaceFileEntry {
	t.Helper()
	var listResponse struct {
		Entries []workspaceFileEntry `json:"entries"`
	}
	if errorValue := json.Unmarshal(recorder.Body.Bytes(), &listResponse); errorValue != nil {
		t.Fatalf("body %q: %v", recorder.Body.String(), errorValue)
	}
	return listResponse.Entries
}

func makeUnreadableByTheServingProcess(t *testing.T, directoryPath string) {
	t.Helper()
	if errorValue := os.Chmod(directoryPath, 0o000); errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() { _ = os.Chmod(directoryPath, 0o755) })
}

func TestWorkspaceFilesHandlerListsAPrivateHomeAsItsOwner(t *testing.T) {
	rootPath := t.TempDir()
	privateHomePath := filepath.Join(rootPath, "private", "people", "person-1")
	if errorValue := os.MkdirAll(filepath.Join(privateHomePath, "unreadable-by-the-service"), 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	makeUnreadableByTheServingProcess(t, privateHomePath)

	factory := &stubWorkspaceActorFactory{entries: []security.WorkspaceActorDirectoryEntry{
		{Name: "notes.md", SizeBytes: 12, ModifiedAtUnix: 1700000000},
		{Name: "tmp", IsDirectory: true, ModifiedAtUnix: 1700000000},
		{Name: ".blueclaw", IsDirectory: true, ModifiedAtUnix: 1700000000},
	}}
	handler := newWorkspaceFilesTestHandler(factory, rootPath)

	recorder := httptest.NewRecorder()
	handler.HandleList(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/workspace/list?personID=person-1&path=/workspace/private/people/person-1", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}

	entries := decodeWorkspaceListEntries(t, recorder)
	if len(entries) != 2 || entries[0].Name != "tmp" || !entries[0].IsDirectory || entries[1].Name != "notes.md" {
		t.Fatalf("expected the owner's own listing with .blueclaw hidden and directories first, got %+v", entries)
	}
	if entries[1].Size != 12 || entries[1].ModifiedAt != "2023-11-14T22:13:20Z" {
		t.Fatalf("expected the actor's size and modification time to survive, got %+v", entries[1])
	}
	if len(factory.recordedRequests) != 1 {
		t.Fatalf("expected exactly one requester actor, got %+v", factory.recordedRequests)
	}
	if factory.recordedRequests[0].personID != "person-1" {
		t.Fatalf("expected the listing to run as person-1, got %+v", factory.recordedRequests[0])
	}
	if factory.recordedRequests[0].workspaceRootPath != rootPath {
		t.Fatalf("expected the actor to be built for %q, got %+v", rootPath, factory.recordedRequests[0])
	}
}

func TestWorkspaceFilesHandlerListsCircleAndPublicPathsAsTheSamePerson(t *testing.T) {
	factory := &stubWorkspaceActorFactory{entries: []security.WorkspaceActorDirectoryEntry{{Name: "spec.md", SizeBytes: 3}}}
	handler := newWorkspaceFilesTestHandler(factory, t.TempDir())
	for _, path := range []string{"/workspace/circles/member", "/workspace/shared/public"} {
		recorder := httptest.NewRecorder()
		handler.HandleList(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/workspace/list?personID=person-1&path="+path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("list %q status = %d body = %s", path, recorder.Code, recorder.Body.String())
		}
		if entries := decodeWorkspaceListEntries(t, recorder); len(entries) != 1 || entries[0].Name != "spec.md" {
			t.Fatalf("list %q entries = %+v", path, entries)
		}
	}
	if len(factory.recordedRequests) != 2 {
		t.Fatalf("expected both listings to go through a requester actor, got %+v", factory.recordedRequests)
	}
	for _, recordedRequest := range factory.recordedRequests {
		if recordedRequest.personID != "person-1" || len(recordedRequest.circles) == 0 {
			t.Fatalf("expected the person's circles to reach the actor, got %+v", recordedRequest)
		}
	}
}

func TestWorkspaceFilesHandlerRefusesAReadThatNamesNoPerson(t *testing.T) {
	factory := &stubWorkspaceActorFactory{}
	handler := newWorkspaceFilesTestHandler(factory, t.TempDir())
	anonymousReads := map[string]func(http.ResponseWriter, *http.Request){
		"/admin/api/workspace/list?path=/workspace/private/people/person-1":              handler.HandleList,
		"/admin/api/workspace/download?path=/workspace/private/people/person-1/notes.md": handler.HandleDownload,
	}
	for target, read := range anonymousReads {
		recorder := httptest.NewRecorder()
		read(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d body = %s", target, recorder.Code, recorder.Body.String())
		}
	}
	if len(factory.recordedRequests) != 0 {
		t.Fatalf("expected no actor for an anonymous read, got %+v", factory.recordedRequests)
	}
}

func TestWorkspaceFilesHandlerDownloadsAsTheOwner(t *testing.T) {
	factory := &stubWorkspaceActorFactory{fileContent: []byte("deck-bytes")}
	handler := newWorkspaceFilesTestHandler(factory, t.TempDir())
	recorder := httptest.NewRecorder()
	handler.HandleDownload(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/workspace/download?personID=person-1&path=/workspace/private/people/person-1/deck.pptx", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "deck-bytes" {
		t.Fatalf("status = %d body = %q", recorder.Code, recorder.Body.String())
	}
	if len(factory.recordedRequests) != 1 || factory.recordedRequests[0].personID != "person-1" {
		t.Fatalf("expected the download to run as person-1, got %+v", factory.recordedRequests)
	}
}

func TestWorkspaceFilesHandlerRejectsPathEscape(t *testing.T) {
	factory := &stubWorkspaceActorFactory{}
	handler := newWorkspaceFilesTestHandler(factory, t.TempDir())
	recorder := httptest.NewRecorder()
	handler.HandleList(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/workspace/list?personID=person-1&path=/workspace/../../etc", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected a path escape to be rejected, got status %d", recorder.Code)
	}
}

func TestAHelperTooOldToActForPeopleStillServesWhatTheServiceCanRead(t *testing.T) {
	rootPath := t.TempDir()
	sharedPath := filepath.Join(rootPath, "shared", "public")
	if errorValue := os.MkdirAll(filepath.Join(sharedPath, "handbook"), 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(sharedPath, "notice.txt"), []byte("hello"), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}

	factory := &stubWorkspaceActorFactory{cannotListDirectory: true}
	handler := newWorkspaceFilesTestHandler(factory, rootPath)

	recorder := httptest.NewRecorder()
	handler.HandleList(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/workspace/list?personID=person-1&path=/workspace/shared/public", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("a shared directory stopped answering: status = %d body = %s", recorder.Code, recorder.Body.String())
	}

	entries := decodeWorkspaceListEntries(t, recorder)
	if len(entries) != 2 || entries[0].Name != "handbook" || !entries[0].IsDirectory || entries[1].Name != "notice.txt" {
		t.Fatalf("expected the service listing with directories first, got %+v", entries)
	}
}

func TestAHelperTooOldToActForPeopleLeavesAPrivateHomeToTheKernel(t *testing.T) {
	rootPath := t.TempDir()
	privateHomePath := filepath.Join(rootPath, "private", "people", "person-1")
	if errorValue := os.MkdirAll(privateHomePath, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	makeUnreadableByTheServingProcess(t, privateHomePath)

	factory := &stubWorkspaceActorFactory{cannotListDirectory: true}
	handler := newWorkspaceFilesTestHandler(factory, rootPath)

	recorder := httptest.NewRecorder()
	handler.HandleList(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/workspace/list?personID=person-1&path=/workspace/private/people/person-1", nil))
	if recorder.Code == http.StatusOK {
		t.Fatalf("an old helper handed out a private home the service may not read: %s", recorder.Body.String())
	}
}

func TestWorkspaceFilesHandlerSaysAnOlderHelperCannotReadAPrivateHome(t *testing.T) {
	workspaceRootPath := t.TempDir()
	privateHome := filepath.Join(workspaceRootPath, "private", "people", "person-1")
	if errorValue := os.MkdirAll(privateHome, 0o700); errorValue != nil {
		t.Fatalf("make the private home: %v", errorValue)
	}
	if errorValue := os.Chmod(privateHome, 0o000); errorValue != nil {
		t.Fatalf("close the private home: %v", errorValue)
	}
	t.Cleanup(func() { os.Chmod(privateHome, 0o700) })

	handler := newWorkspaceFilesTestHandler(&stubWorkspaceActorFactory{cannotListDirectory: true}, workspaceRootPath)
	recorder := httptest.NewRecorder()
	handler.HandleList(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/workspace/list?personID=person-1&path=/workspace/private/people/person-1", nil))

	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d body = %q", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), workspaceRootPath) {
		t.Fatalf("the answer named a guest path: %q", recorder.Body.String())
	}
}
