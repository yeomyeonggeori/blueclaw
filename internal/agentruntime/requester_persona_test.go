package agentruntime

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

type personaActorFactory struct {
	documents map[string][]byte
	readPaths []string
	requested []string
}

type personaActor struct {
	factory *personaActorFactory
}

func (factory *personaActorFactory) CanListDirectory(context.Context) bool { return true }

func (factory *personaActorFactory) Requester(_ context.Context, request security.WorkspaceActorRequest) (security.WorkspaceActor, error) {
	factory.requested = append(factory.requested, request.PersonAccess.PersonID)
	return personaActor{factory: factory}, nil
}

func (actor personaActor) Run(context.Context, security.CommandRequest) (security.CommandResult, error) {
	return security.CommandResult{}, nil
}

func (actor personaActor) MkdirAll(context.Context, string) error { return nil }

func (actor personaActor) WriteFile(context.Context, string, []byte) error { return nil }

func (actor personaActor) ReadFile(_ context.Context, path string, _ int64) ([]byte, error) {
	actor.factory.readPaths = append(actor.factory.readPaths, path)
	document, isPresent := actor.factory.documents[path]
	if !isPresent {
		return nil, security.WorkspaceActorError{Operation: "read", Code: security.ActorErrorCodeNotFound, Detail: "not found"}
	}
	return document, nil
}

func (actor personaActor) BundleDirectory(context.Context, string, security.WorkspaceActorBundleOptions) (security.WorkspaceActorBundle, error) {
	return security.WorkspaceActorBundle{}, nil
}

func (actor personaActor) ListDirectory(context.Context, string) ([]security.WorkspaceActorDirectoryEntry, error) {
	return nil, nil
}

func (actor personaActor) Stat(context.Context, string) (security.WorkspaceActorStat, error) {
	return security.WorkspaceActorStat{}, nil
}

func TestRequesterPersonaInstructionReadsTheRequestersOwnHomeAsThatPerson(t *testing.T) {
	workspacePath := t.TempDir()
	documentPath := filepath.Join(security.PersonHomeDirectoryPath(workspacePath, "person-1"), ".internkim", "user.json")
	factory := &personaActorFactory{documents: map[string][]byte{
		documentPath: []byte(`{"schemaVersion": 1, "callMe": "샘플님", "preferences": ["Give me the command first."]}`),
	}}

	instruction := requesterPersonaInstruction(factory, policy.PersonAccess{PersonID: "person-1"}, workspacePath)

	if !strings.Contains(instruction, "Call them 샘플님.") || !strings.Contains(instruction, "Give me the command first.") {
		t.Fatalf("expected the requester's document in the instruction, got %q", instruction)
	}
	if len(factory.requested) != 1 || factory.requested[0] != "person-1" {
		t.Fatalf("expected the read to run as the requester, got %v", factory.requested)
	}
	if requesterPersonaInstruction(factory, policy.PersonAccess{PersonID: "person-2"}, workspacePath) != "" {
		t.Fatal("expected nothing for a person without a document")
	}
	if requesterPersonaInstruction(nil, policy.PersonAccess{PersonID: "person-1"}, workspacePath) != "" {
		t.Fatal("expected nothing without an actor factory")
	}
}

func TestRequesterPersonaInstructionLeavesOutADocumentTheSchemaRefuses(t *testing.T) {
	workspacePath := t.TempDir()
	documentPath := filepath.Join(security.PersonHomeDirectoryPath(workspacePath, "person-1"), ".internkim", "user.json")
	factory := &personaActorFactory{documents: map[string][]byte{
		documentPath: []byte(`{"schemaVersion": 1, "callMe": "샘플님", "nickname": "kim"}`),
	}}

	if instruction := requesterPersonaInstruction(factory, policy.PersonAccess{PersonID: "person-1"}, workspacePath); instruction != "" {
		t.Fatalf("expected a refused document to render nothing, got %q", instruction)
	}
}
