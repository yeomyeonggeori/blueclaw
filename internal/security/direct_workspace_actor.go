package security

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
)

// DirectWorkspaceActorFactory runs work as the process itself, with no POSIX
// projection between the agent and the workspace. That is what a standalone
// harness needs - a coding agent pointed at a directory it already owns - and
// what an appliance must never use, because there the whole point is that work
// runs as whoever asked for it.
type DirectWorkspaceActorFactory struct {
	terminalService *ShellService
}

type DirectWorkspaceActor struct {
	identity        ExecutionIdentity
	terminalService *ShellService
}

func NewDirectWorkspaceActorFactory(terminalServices ...*ShellService) DirectWorkspaceActorFactory {
	return DirectWorkspaceActorFactory{terminalService: firstTerminalService(terminalServices)}
}

func firstTerminalService(terminalServices []*ShellService) *ShellService {
	if len(terminalServices) == 0 {
		return nil
	}
	return terminalServices[0]
}

func (factory DirectWorkspaceActorFactory) CanListDirectory(context.Context) bool {
	return true
}

func (factory DirectWorkspaceActorFactory) Requester(ctx context.Context, request WorkspaceActorRequest) (WorkspaceActor, error) {
	_ = ctx
	personAccess := request.PersonAccess
	if strings.TrimSpace(personAccess.PersonID) == "" {
		return nil, WorkspaceActorError{Operation: "requester", Stage: "identity", Code: ActorErrorCodeIdentityMissing, Detail: ActorErrorCodeIdentityMissing}
	}
	return DirectWorkspaceActor{
		identity:        ExecutionIdentityForPersonAccess(personAccess, request.WorkspaceRootPath),
		terminalService: factory.terminalService,
	}, nil
}

func (actor DirectWorkspaceActor) Run(ctx context.Context, commandRequest CommandRequest) (CommandResult, error) {
	if actor.terminalService == nil {
		return CommandResult{}, errors.New("direct test actor does not run shell commands")
	}
	if strings.TrimSpace(commandRequest.ExecutionIdentity.UserName) == "" {
		commandRequest.ExecutionIdentity = actor.identity
	}
	return actor.terminalService.RunCommand(ctx, commandRequest)
}

func (actor DirectWorkspaceActor) MkdirAll(ctx context.Context, path string) error {
	_ = ctx
	if errorValue := os.MkdirAll(path, 0o777); errorValue != nil {
		return actor.actorError("mkdir_all", "direct", path, errorValue)
	}
	return nil
}

func (actor DirectWorkspaceActor) WriteFile(ctx context.Context, path string, content []byte) error {
	_ = ctx
	if errorValue := os.WriteFile(path, content, 0o666); errorValue != nil {
		return actor.actorError("write_file", "direct", path, errorValue)
	}
	return nil
}

func (actor DirectWorkspaceActor) ReadFile(ctx context.Context, path string, maximumBytes int64) ([]byte, error) {
	_ = ctx
	fileInformation, errorValue := os.Stat(path)
	if errorValue != nil {
		return nil, actor.actorError("read_file", "direct", path, errorValue)
	}
	if maximumBytes > 0 && fileInformation.Size() > maximumBytes {
		return nil, actor.actorError("read_file", "direct", path, errors.New("file is too large"))
	}
	content, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return nil, actor.actorError("read_file", "direct", path, errorValue)
	}
	return content, nil
}

func (actor DirectWorkspaceActor) StreamFile(ctx context.Context, path string, fileRange WorkspaceFileRange, destination io.Writer) error {
	_ = ctx
	sourceFile, errorValue := os.Open(path)
	if errorValue != nil {
		return actor.actorError("stream_file", "direct", path, errorValue)
	}
	defer sourceFile.Close()
	if _, errorValue := sourceFile.Seek(fileRange.Offset, io.SeekStart); errorValue != nil {
		return actor.actorError("stream_file", "direct", path, errorValue)
	}
	var source io.Reader = sourceFile
	if fileRange.Length > 0 {
		source = io.LimitReader(sourceFile, fileRange.Length)
	}
	if _, errorValue := io.Copy(destination, source); errorValue != nil {
		return actor.actorError("stream_file", "direct", path, errorValue)
	}
	return nil
}

func (actor DirectWorkspaceActor) WriteFileFrom(ctx context.Context, path string, source io.Reader) error {
	_ = ctx
	destinationFile, errorValue := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o666)
	if errorValue != nil {
		return actor.actorError("write_file", "direct", path, errorValue)
	}
	if _, errorValue := io.Copy(destinationFile, source); errorValue != nil {
		_ = destinationFile.Close()
		return actor.actorError("write_file", "direct", path, errorValue)
	}
	if errorValue := destinationFile.Close(); errorValue != nil {
		return actor.actorError("write_file", "direct", path, errorValue)
	}
	return nil
}

func (actor DirectWorkspaceActor) ListDirectory(ctx context.Context, path string) ([]WorkspaceActorDirectoryEntry, error) {
	_ = ctx
	entries, errorValue := ReadWorkspaceDirectory(path)
	if errorValue != nil {
		return nil, actor.actorError("list_directory", "direct", path, errorValue)
	}
	return entries, nil
}

func (actor DirectWorkspaceActor) Stat(ctx context.Context, path string) (WorkspaceActorStat, error) {
	_ = ctx
	fileInformation, errorValue := os.Stat(path)
	if errorValue != nil {
		return WorkspaceActorStat{}, actor.actorError("stat", "direct", path, errorValue)
	}
	return WorkspaceActorStat{
		Path:           path,
		IsRegular:      fileInformation.Mode().IsRegular(),
		IsDirectory:    fileInformation.IsDir(),
		SizeBytes:      fileInformation.Size(),
		ModifiedAtUnix: fileInformation.ModTime().Unix(),
		Mode:           fileInformation.Mode(),
	}, nil
}

func (actor DirectWorkspaceActor) actorError(operation string, stage string, path string, errorValue error) error {
	return WorkspaceActorError{
		Operation:   operation,
		Stage:       stage,
		ActorUser:   actor.identity.UserName,
		VirtualPath: path,
		Code:        actorErrorCode(errorValue),
		Detail:      errorValue.Error(),
	}
}

func actorErrorCode(errorValue error) string {
	if os.IsNotExist(errorValue) {
		return ActorErrorCodeNotFound
	}
	if os.IsPermission(errorValue) {
		return ActorErrorCodePermissionDenied
	}
	if os.IsExist(errorValue) {
		return ActorErrorCodeAlreadyExists
	}
	return ActorErrorCodeOperationFailed
}
