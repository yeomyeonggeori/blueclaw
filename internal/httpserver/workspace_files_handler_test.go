package httpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
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
	permissionDenied    bool
	writtenPath         string
	writtenContent      []byte
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

func (actor stubWorkspaceActor) ListDirectory(context.Context, string) ([]security.WorkspaceActorDirectoryEntry, error) {
	return actor.factory.entries, nil
}

func (actor stubWorkspaceActor) Stat(_ context.Context, path string) (security.WorkspaceActorStat, error) {
	if actor.factory.permissionDenied {
		return security.WorkspaceActorStat{}, security.WorkspaceActorError{Operation: "stat", Code: security.ActorErrorCodePermissionDenied, Detail: "permission denied"}
	}
	return security.WorkspaceActorStat{Path: path, IsRegular: true, SizeBytes: int64(len(actor.factory.fileContent))}, nil
}

func (actor stubWorkspaceActor) StreamFile(_ context.Context, _ string, fileRange security.WorkspaceFileRange, destination io.Writer) error {
	_, errorValue := destination.Write(actor.factory.fileContent[fileRange.Offset : fileRange.Offset+fileRange.Length])
	return errorValue
}

func (actor stubWorkspaceActor) WriteFileFrom(_ context.Context, path string, source io.Reader) error {
	if actor.factory.permissionDenied {
		return security.WorkspaceActorError{Operation: "write_file", Code: security.ActorErrorCodePermissionDenied, Detail: "permission denied"}
	}
	content, errorValue := io.ReadAll(source)
	actor.factory.writtenPath = path
	actor.factory.writtenContent = content
	return errorValue
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

	tmpEntryCount := 3
	factory := &stubWorkspaceActorFactory{entries: []security.WorkspaceActorDirectoryEntry{
		{Name: "notes.md", SizeBytes: 12, ModifiedAtUnix: 1700000000},
		{Name: "tmp", IsDirectory: true, ModifiedAtUnix: 1700000000, EntryCount: &tmpEntryCount},
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
	if entries[0].EntryCount == nil || *entries[0].EntryCount != 3 || entries[1].EntryCount != nil {
		t.Fatalf("expected the directory's entry count to survive and a file to carry none, got %+v", entries)
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
		t.Fatalf("the answer named a filesystem path: %q", recorder.Body.String())
	}
}

func TestWorkspaceFilesHandlerStreamsAFileLargerThanAnyBufferItKeeps(t *testing.T) {
	rootPath := t.TempDir()
	homePath := filepath.Join(rootPath, "private", "people", "person-1")
	if errorValue := os.MkdirAll(homePath, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	content := bytes.Repeat([]byte("0123456789abcdef"), 70<<16)
	if errorValue := os.WriteFile(filepath.Join(homePath, "film.mov"), content, 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}
	handler := WorkspaceFilesHandler{
		WorkspaceRootPath:     rootPath,
		WorkspaceActorFactory: security.NewDirectWorkspaceActorFactory(),
		PersonAccessResolver:  stubPersonAccessResolver{},
	}
	server := httptest.NewServer(http.HandlerFunc(handler.HandleDownload))
	defer server.Close()

	response, errorValue := http.Get(server.URL + "/admin/api/workspace/download?personID=person-1&path=/workspace/private/people/person-1/film.mov")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if response.ContentLength != int64(len(content)) {
		t.Fatalf("expected the length to be said up front, got %d", response.ContentLength)
	}
	hasher := sha256.New()
	if _, errorValue := io.Copy(hasher, response.Body); errorValue != nil {
		t.Fatal(errorValue)
	}
	expected := sha256.Sum256(content)
	if hex.EncodeToString(hasher.Sum(nil)) != hex.EncodeToString(expected[:]) {
		t.Fatal("the streamed file is not the file on disk")
	}
}

func TestWorkspaceFilesHandlerRefusesADownloadTheOwnerMayNotRead(t *testing.T) {
	factory := &stubWorkspaceActorFactory{fileContent: []byte("private"), permissionDenied: true}
	handler := newWorkspaceFilesTestHandler(factory, t.TempDir())
	recorder := httptest.NewRecorder()
	handler.HandleDownload(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/workspace/download?personID=person-2&path=/workspace/private/people/person-1/notes.md", nil))
	if recorder.Code != http.StatusForbidden || strings.Contains(recorder.Body.String(), "private") {
		t.Fatalf("status = %d body = %q", recorder.Code, recorder.Body.String())
	}
}

func TestWorkspaceFilesHandlerWritesAnUploadAsThePersonNamed(t *testing.T) {
	rootPath := t.TempDir()
	factory := &stubWorkspaceActorFactory{}
	handler := newWorkspaceFilesTestHandler(factory, rootPath)
	recorder := httptest.NewRecorder()
	handler.HandleUpload(recorder, httptest.NewRequest(http.MethodPut, "/admin/api/workspace/file?personID=person-1&path=/workspace/private/people/person-1/inbox/report.pdf", strings.NewReader("report-bytes")))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if len(factory.recordedRequests) != 1 || factory.recordedRequests[0].personID != "person-1" {
		t.Fatalf("expected the write to run as person-1, got %+v", factory.recordedRequests)
	}
	if factory.writtenPath != filepath.Join(rootPath, "private", "people", "person-1", "inbox", "report.pdf") || string(factory.writtenContent) != "report-bytes" {
		t.Fatalf("wrote %q to %q", factory.writtenContent, factory.writtenPath)
	}
	var answer struct {
		Name      string `json:"name"`
		SizeBytes int64  `json:"sizeBytes"`
	}
	if errorValue := json.Unmarshal(recorder.Body.Bytes(), &answer); errorValue != nil || answer.Name != "report.pdf" || answer.SizeBytes != 12 {
		t.Fatalf("answer %s: %v", recorder.Body.String(), errorValue)
	}
}

func TestWorkspaceFilesHandlerRefusesAnUploadThePersonMayNotWrite(t *testing.T) {
	factory := &stubWorkspaceActorFactory{permissionDenied: true}
	handler := newWorkspaceFilesTestHandler(factory, t.TempDir())
	recorder := httptest.NewRecorder()
	handler.HandleUpload(recorder, httptest.NewRequest(http.MethodPut, "/admin/api/workspace/file?personID=person-2&path=/workspace/private/people/person-1/planted.txt", strings.NewReader("planted")))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestWorkspaceFilesHandlerAnswersOneRangeOfAFile(t *testing.T) {
	factory := &stubWorkspaceActorFactory{fileContent: []byte("0123456789")}
	handler := newWorkspaceFilesTestHandler(factory, t.TempDir())
	target := "/admin/api/workspace/download?personID=person-1&path=/workspace/private/people/person-1/digits.txt"

	cases := []struct {
		rangeHeader  string
		status       int
		body         string
		contentRange string
	}{
		{"bytes=2-5", http.StatusPartialContent, "2345", "bytes 2-5/10"},
		{"bytes=7-100", http.StatusPartialContent, "789", "bytes 7-9/10"},
		{"bytes=4-", http.StatusPartialContent, "456789", "bytes 4-9/10"},
		{"bytes=10-20", http.StatusRequestedRangeNotSatisfiable, "", "bytes */10"},
		{"", http.StatusOK, "0123456789", ""},
	}
	for _, testCase := range cases {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		if testCase.rangeHeader != "" {
			request.Header.Set("Range", testCase.rangeHeader)
		}
		recorder := httptest.NewRecorder()
		handler.HandleDownload(recorder, request)
		if recorder.Code != testCase.status || recorder.Header().Get("Content-Range") != testCase.contentRange {
			t.Fatalf("%q: status = %d content-range = %q", testCase.rangeHeader, recorder.Code, recorder.Header().Get("Content-Range"))
		}
		if testCase.status != http.StatusRequestedRangeNotSatisfiable && recorder.Body.String() != testCase.body {
			t.Fatalf("%q: body = %q", testCase.rangeHeader, recorder.Body.String())
		}
	}
}
