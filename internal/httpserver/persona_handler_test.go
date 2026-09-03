package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

type personaStubFactory struct {
	documents map[string][]byte
	requested []string
	madeDirs  []string
}

type personaStubActor struct {
	factory *personaStubFactory
}

func (factory *personaStubFactory) CanListDirectory(context.Context) bool { return true }

func (factory *personaStubFactory) Requester(_ context.Context, request security.WorkspaceActorRequest) (security.WorkspaceActor, error) {
	factory.requested = append(factory.requested, request.PersonAccess.PersonID)
	return personaStubActor{factory: factory}, nil
}

func (actor personaStubActor) Run(context.Context, security.CommandRequest) (security.CommandResult, error) {
	return security.CommandResult{}, nil
}

func (actor personaStubActor) MkdirAll(_ context.Context, path string) error {
	actor.factory.madeDirs = append(actor.factory.madeDirs, path)
	return nil
}

func (actor personaStubActor) WriteFile(_ context.Context, path string, content []byte) error {
	actor.factory.documents[path] = content
	return nil
}

func (actor personaStubActor) ReadFile(_ context.Context, path string, _ int64) ([]byte, error) {
	document, isPresent := actor.factory.documents[path]
	if !isPresent {
		return nil, security.WorkspaceActorError{Operation: "read", Code: security.ActorErrorCodeNotFound, Detail: "not found"}
	}
	return document, nil
}

func (actor personaStubActor) BundleDirectory(context.Context, string, security.WorkspaceActorBundleOptions) (security.WorkspaceActorBundle, error) {
	return security.WorkspaceActorBundle{}, nil
}

func (actor personaStubActor) ListDirectory(context.Context, string) ([]security.WorkspaceActorDirectoryEntry, error) {
	return nil, nil
}

func (actor personaStubActor) Stat(context.Context, string) (security.WorkspaceActorStat, error) {
	return security.WorkspaceActorStat{}, nil
}

func TestPersonaHandlerWritesAndReadsTheDocumentInThePersonsOwnHome(t *testing.T) {
	workspacePath := t.TempDir()
	factory := &personaStubFactory{documents: map[string][]byte{}}
	handler := PersonaHandler{WorkspaceRootPath: workspacePath, WorkspaceActorFactory: factory, PersonAccessResolver: stubPersonAccessResolver{}}

	recorder := httptest.NewRecorder()
	handler.HandleReadUser(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/persona/user?personID=person-1", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"schemaVersion":1`) {
		t.Fatalf("expected an empty document before any write, got %d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.HandleWriteUser(recorder, httptest.NewRequest(http.MethodPut, "/admin/api/persona/user?personID=person-1", strings.NewReader(`{"schemaVersion": 1, "callMe": " 샘플님 ", "preferences": ["Give me the command first."]}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected the write to succeed, got %d %s", recorder.Code, recorder.Body.String())
	}
	documentPath := filepath.Join(security.PersonHomeDirectoryPath(workspacePath, "person-1"), ".internkim", "user.json")
	written, isWritten := factory.documents[documentPath]
	if !isWritten || !strings.Contains(string(written), `"callMe": "샘플님"`) {
		t.Fatalf("expected the canonical document under the person's home, got %q %v", written, factory.documents)
	}
	if len(factory.madeDirs) != 1 || factory.madeDirs[0] != filepath.Dir(documentPath) {
		t.Fatalf("expected the .internkim directory to be made as the person, got %v", factory.madeDirs)
	}
	for _, requested := range factory.requested {
		if requested != "person-1" {
			t.Fatalf("expected every access to run as the person named, got %v", factory.requested)
		}
	}

	recorder = httptest.NewRecorder()
	handler.HandleReadUser(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/persona/user?personID=person-1", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Give me the command first.") {
		t.Fatalf("expected the written document back, got %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestPersonaHandlerRefusesWhatTheSchemaDoesNotNameAndNeedsAPerson(t *testing.T) {
	factory := &personaStubFactory{documents: map[string][]byte{}}
	handler := PersonaHandler{WorkspaceRootPath: t.TempDir(), WorkspaceActorFactory: factory, PersonAccessResolver: stubPersonAccessResolver{}}

	recorder := httptest.NewRecorder()
	handler.HandleWriteUser(recorder, httptest.NewRequest(http.MethodPut, "/admin/api/persona/user?personID=person-1", strings.NewReader(`{"schemaVersion": 1, "nickname": "kim"}`)))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "nickname") {
		t.Fatalf("expected the unknown field to be refused by name, got %d %s", recorder.Code, recorder.Body.String())
	}
	if len(factory.documents) != 0 {
		t.Fatalf("expected nothing written after a refusal, got %v", factory.documents)
	}

	recorder = httptest.NewRecorder()
	handler.HandleWriteUser(recorder, httptest.NewRequest(http.MethodPut, "/admin/api/persona/user", strings.NewReader(`{"schemaVersion": 1}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected a write without a person to be refused, got %d", recorder.Code)
	}
}
