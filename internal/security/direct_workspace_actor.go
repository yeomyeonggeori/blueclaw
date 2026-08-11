package security

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DirectWorkspaceActorFactory runs work as the process itself, with no POSIX
// projection between the agent and the workspace. That is what a standalone
// harness needs - a coding agent pointed at a directory it already owns - and
// what an appliance must never use, because there the whole point is that work
// runs as whoever asked for it.
type DirectWorkspaceActorFactory struct {
	terminalService *TerminalSessionService
}

type DirectWorkspaceActor struct {
	identity        ExecutionIdentity
	terminalService *TerminalSessionService
}

func NewDirectWorkspaceActorFactory(terminalServices ...*TerminalSessionService) DirectWorkspaceActorFactory {
	return DirectWorkspaceActorFactory{terminalService: firstTerminalService(terminalServices)}
}

func firstTerminalService(terminalServices []*TerminalSessionService) *TerminalSessionService {
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

func (actor DirectWorkspaceActor) ListDirectory(ctx context.Context, path string) ([]WorkspaceActorDirectoryEntry, error) {
	_ = ctx
	directoryEntries, errorValue := os.ReadDir(path)
	if errorValue != nil {
		return nil, actor.actorError("list_directory", "direct", path, errorValue)
	}
	entries := []WorkspaceActorDirectoryEntry{}
	for _, directoryEntry := range directoryEntries {
		fileInformation, errorValue := os.Stat(filepath.Join(path, directoryEntry.Name()))
		if errorValue != nil {
			continue
		}
		entries = append(entries, WorkspaceActorDirectoryEntry{
			Name:           directoryEntry.Name(),
			IsDirectory:    fileInformation.IsDir(),
			SizeBytes:      fileInformation.Size(),
			ModifiedAtUnix: fileInformation.ModTime().Unix(),
		})
	}
	return entries, nil
}

func (actor DirectWorkspaceActor) BundleDirectory(ctx context.Context, path string, options WorkspaceActorBundleOptions) (WorkspaceActorBundle, error) {
	_ = ctx
	document, errorValue := actor.bundleDirectoryDocument(path, options)
	if errorValue != nil {
		return WorkspaceActorBundle{}, errorValue
	}
	return WorkspaceActorBundle{
		Format:        "tar.gz",
		ContentBase64: base64.StdEncoding.EncodeToString(document),
		SizeBytes:     int64(len(document)),
	}, nil
}

func (actor DirectWorkspaceActor) bundleDirectoryDocument(path string, options WorkspaceActorBundleOptions) ([]byte, error) {
	buffer := bytes.Buffer{}
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	errorValue := filepath.Walk(path, func(currentPath string, information os.FileInfo, walkError error) error {
		if walkError != nil {
			return walkError
		}
		relativePath, errorValue := filepath.Rel(path, currentPath)
		if errorValue != nil || relativePath == "." {
			return errorValue
		}
		if bundlePathIsExcluded(relativePath, information, options.ExcludeNames) {
			if information.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		return writeBundleEntry(tarWriter, currentPath, relativePath, information)
	})
	closeError := tarWriter.Close()
	gzipCloseError := gzipWriter.Close()
	if errorValue != nil {
		return nil, actor.actorError("bundle_directory", "direct", path, errorValue)
	}
	if closeError != nil {
		return nil, actor.actorError("bundle_directory", "direct", path, closeError)
	}
	if gzipCloseError != nil {
		return nil, actor.actorError("bundle_directory", "direct", path, gzipCloseError)
	}
	if options.MaxBytes > 0 && int64(buffer.Len()) > options.MaxBytes {
		return nil, actor.actorError("bundle_directory", "direct", path, errors.New("directory bundle is too large"))
	}
	return buffer.Bytes(), nil
}

func bundlePathIsExcluded(relativePath string, information os.FileInfo, excludeNames []string) bool {
	pathParts := strings.Split(filepath.ToSlash(relativePath), "/")
	for _, pathPart := range pathParts {
		for _, excludeName := range excludeNames {
			if strings.TrimSpace(pathPart) == strings.TrimSpace(excludeName) {
				return true
			}
		}
	}
	return !information.IsDir() && strings.HasSuffix(relativePath, "~")
}

func writeBundleEntry(tarWriter *tar.Writer, path string, relativePath string, information os.FileInfo) error {
	header, errorValue := tar.FileInfoHeader(information, "")
	if errorValue != nil {
		return errorValue
	}
	header.Name = filepath.ToSlash(relativePath)
	if errorValue := tarWriter.WriteHeader(header); errorValue != nil {
		return errorValue
	}
	if !information.Mode().IsRegular() {
		return nil
	}
	file, errorValue := os.Open(path)
	if errorValue != nil {
		return errorValue
	}
	defer file.Close()
	_, errorValue = io.Copy(tarWriter, file)
	return errorValue
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
