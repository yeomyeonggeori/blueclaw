package security

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

const (
	ActorErrorCodeIdentityMissing      = "actor_identity_missing"
	ActorErrorCodeRuntimeUnavailable   = "actor_runtime_unavailable"
	ActorErrorCodePermissionDenied     = "permission_denied"
	ActorErrorCodeNotFound             = "not_found"
	ActorErrorCodeAlreadyExists        = "already_exists"
	ActorErrorCodeInvalidPath          = "invalid_path"
	ActorErrorCodeOperationFailed      = "operation_failed"
	ActorErrorCodeUnsupportedOperation = "unsupported_operation"
)

const (
	actorDirectoryCreateMode os.FileMode = 0o2770
	actorFileCreateMode      os.FileMode = 0o660
)

type WorkspaceActor interface {
	Run(context.Context, CommandRequest) (CommandResult, error)
	MkdirAll(context.Context, string) error
	WriteFile(context.Context, string, []byte) error
	ReadFile(context.Context, string, int64) ([]byte, error)
	BundleDirectory(context.Context, string, WorkspaceActorBundleOptions) (WorkspaceActorBundle, error)
	ListDirectory(context.Context, string) ([]WorkspaceActorDirectoryEntry, error)
	Stat(context.Context, string) (WorkspaceActorStat, error)
}

type WorkspaceActorFactory interface {
	Requester(context.Context, WorkspaceActorRequest) (WorkspaceActor, error)
	CanListDirectory(context.Context) bool
}

type WorkspaceActorRequest struct {
	PersonAccess      policy.PersonAccess
	WorkspaceRootPath string
}

type WorkspaceActorError struct {
	Operation   string `json:"operation"`
	Stage       string `json:"stage"`
	ActorUser   string `json:"actorUser"`
	VirtualPath string `json:"virtualPath"`
	Code        string `json:"code"`
	Detail      string `json:"detail"`
}

type WorkspaceActorStat struct {
	Path           string      `json:"path"`
	IsRegular      bool        `json:"isRegular"`
	IsDirectory    bool        `json:"isDirectory"`
	SizeBytes      int64       `json:"sizeBytes"`
	ModifiedAtUnix int64       `json:"modifiedAtUnix"`
	Mode           os.FileMode `json:"mode"`
}

type WorkspaceActorDirectoryEntry struct {
	Name           string `json:"name"`
	IsDirectory    bool   `json:"isDirectory"`
	SizeBytes      int64  `json:"sizeBytes"`
	ModifiedAtUnix int64  `json:"modifiedAtUnix"`
}

type WorkspaceActorBundleOptions struct {
	Format       string
	MaxBytes     int64
	ExcludeNames []string
}

type WorkspaceActorBundle struct {
	Format        string
	ContentBase64 string
	SizeBytes     int64
}

type POSIXWorkspaceActorFactory struct {
	terminalService       *ShellService
	terminalConfiguration config.TerminalConfiguration
}

type POSIXHelperWorkspaceActor struct {
	terminalService       *ShellService
	terminalConfiguration config.TerminalConfiguration
	executionIdentity     ExecutionIdentity
}

func (actorError WorkspaceActorError) Error() string {
	detail := strings.TrimSpace(actorError.Detail)
	if detail == "" {
		detail = "operation failed"
	}
	actorUser := strings.TrimSpace(actorError.ActorUser)
	if actorUser == "" {
		actorUser = "unknown"
	}
	virtualPath := strings.TrimSpace(actorError.VirtualPath)
	if virtualPath == "" {
		virtualPath = "workspace path"
	}
	return fmt.Sprintf("actor.%s failed for %s as %s: %s", actorError.Operation, virtualPath, actorUser, detail)
}

func (shellService *ShellService) WorkspaceActorFactory() WorkspaceActorFactory {
	return POSIXWorkspaceActorFactory{
		terminalService:       shellService,
		terminalConfiguration: shellService.commandGuardrailService.terminalConfiguration,
	}
}

func (factory POSIXWorkspaceActorFactory) CanListDirectory(ctx context.Context) bool {
	return HelperCanListDirectory(ctx, factory.terminalConfiguration.POSIXHelperPath)
}

func (factory POSIXWorkspaceActorFactory) Requester(ctx context.Context, request WorkspaceActorRequest) (WorkspaceActor, error) {
	identity := ExecutionIdentityForPersonAccess(request.PersonAccess, request.WorkspaceRootPath)
	if strings.TrimSpace(identity.UserName) == "" {
		return nil, WorkspaceActorError{Operation: "requester", Stage: "identity", Code: ActorErrorCodeIdentityMissing, Detail: ActorErrorCodeIdentityMissing}
	}
	if factory.terminalService == nil || strings.TrimSpace(factory.terminalConfiguration.POSIXHelperPath) == "" {
		return nil, WorkspaceActorError{Operation: "requester", Stage: "factory", ActorUser: identity.UserName, Code: ActorErrorCodeRuntimeUnavailable, Detail: "posix helper is required for requester workspace side effects"}
	}
	if errorValue := ensureHelperSupportsExecAndFS(ctx, factory.terminalConfiguration.POSIXHelperPath, identity.UserName); errorValue != nil {
		return nil, errorValue
	}
	return POSIXHelperWorkspaceActor{
		terminalService:       factory.terminalService,
		terminalConfiguration: factory.terminalConfiguration,
		executionIdentity:     identity,
	}, nil
}

type helperCapabilities struct {
	Version      int      `json:"version"`
	Capabilities []string `json:"capabilities"`
}

var verifiedHelperSupportByPath sync.Map

func ensureHelperSupportsExecAndFS(ctx context.Context, helperPath string, actorUser string) error {
	if _, isVerified := verifiedHelperSupportByPath.Load(helperPath); isVerified {
		return nil
	}
	if errorValue := probeHelperSupportsExecAndFS(ctx, helperPath, actorUser); errorValue != nil {
		return errorValue
	}
	verifiedHelperSupportByPath.Store(helperPath, true)
	return nil
}

func probeHelperSupportsExecAndFS(ctx context.Context, helperPath string, actorUser string) error {
	executionContext, cancelFunction := context.WithTimeout(ctx, 15*time.Second)
	defer cancelFunction()
	command := exec.CommandContext(executionContext, helperPath, "capabilities")
	output, errorValue := command.CombinedOutput()
	if executionContext.Err() == context.DeadlineExceeded {
		return WorkspaceActorError{Operation: "requester", Stage: "capabilities", ActorUser: actorUser, Code: ActorErrorCodeRuntimeUnavailable, Detail: helperFailureDetail(helperPath, "capabilities", "posix helper capabilities timed out", nil)}
	}
	if errorValue != nil {
		return WorkspaceActorError{Operation: "requester", Stage: "capabilities", ActorUser: actorUser, Code: ActorErrorCodeRuntimeUnavailable, Detail: helperFailureDetail(helperPath, "capabilities", errorValue.Error(), output)}
	}
	var capabilities helperCapabilities
	if errorValue := json.Unmarshal(output, &capabilities); errorValue != nil {
		return WorkspaceActorError{Operation: "requester", Stage: "capabilities", ActorUser: actorUser, Code: ActorErrorCodeRuntimeUnavailable, Detail: helperFailureDetail(helperPath, "capabilities", errorValue.Error(), output)}
	}
	if capabilities.Version < 2 || !containsCapability(capabilities.Capabilities, "exec") || !containsCapability(capabilities.Capabilities, "fs") {
		return WorkspaceActorError{Operation: "requester", Stage: "capabilities", ActorUser: actorUser, Code: ActorErrorCodeRuntimeUnavailable, Detail: helperFailureDetail(helperPath, "capabilities", "posix helper does not support exec and fs capabilities", output)}
	}
	return nil
}

func containsCapability(capabilities []string, expectedCapability string) bool {
	for _, capability := range capabilities {
		if strings.TrimSpace(capability) == expectedCapability {
			return true
		}
	}
	return false
}

func (actor POSIXHelperWorkspaceActor) Run(ctx context.Context, commandRequest CommandRequest) (CommandResult, error) {
	if strings.TrimSpace(commandRequest.ExecutionIdentity.UserName) == "" {
		commandRequest.ExecutionIdentity = actor.executionIdentity
	}
	if strings.TrimSpace(commandRequest.ExecutionIdentity.UserName) == "" {
		return CommandResult{}, actorError("run", "identity", actor.executionIdentity, "", ActorErrorCodeIdentityMissing, ActorErrorCodeIdentityMissing)
	}
	return actor.terminalService.RunCommand(ctx, commandRequest)
}

func (actor POSIXHelperWorkspaceActor) MkdirAll(ctx context.Context, path string) error {
	return actor.executeFS(ctx, "mkdir_all", path, fsRequest{Path: path, Mode: actorDirectoryCreateMode}, nil)
}

func (actor POSIXHelperWorkspaceActor) WriteFile(ctx context.Context, path string, content []byte) error {
	return actor.executeFS(ctx, "write_file", path, fsRequest{Path: path, Mode: actorFileCreateMode}, bytes.NewReader(content))
}

func (actor POSIXHelperWorkspaceActor) ReadFile(ctx context.Context, path string, maximumBytes int64) ([]byte, error) {
	var response fsResponse
	errorValue := actor.executeFSWithResponse(ctx, "read_file", path, fsRequest{Path: path, MaxBytes: maximumBytes}, nil, &response)
	if errorValue != nil {
		return nil, errorValue
	}
	content, errorValue := base64.StdEncoding.DecodeString(response.ContentBase64)
	if errorValue != nil {
		return nil, actorError("read_file", "decode", actor.executionIdentity, path, ActorErrorCodeOperationFailed, errorValue.Error())
	}
	return content, nil
}

func (actor POSIXHelperWorkspaceActor) ListDirectory(ctx context.Context, path string) ([]WorkspaceActorDirectoryEntry, error) {
	var response fsResponse
	errorValue := actor.executeFSWithResponse(ctx, "list_directory", path, fsRequest{Path: path}, nil, &response)
	if errorValue != nil {
		return nil, errorValue
	}
	return response.Entries, nil
}

func (actor POSIXHelperWorkspaceActor) BundleDirectory(ctx context.Context, path string, options WorkspaceActorBundleOptions) (WorkspaceActorBundle, error) {
	var response fsResponse
	errorValue := actor.executeFSWithResponse(ctx, "bundle_directory", path, fsRequest{
		Path:         path,
		MaxBytes:     options.MaxBytes,
		ExcludeNames: options.ExcludeNames,
	}, nil, &response)
	if errorValue != nil {
		return WorkspaceActorBundle{}, errorValue
	}
	return WorkspaceActorBundle{
		Format:        firstNonEmptyString(response.Format, "tar.gz"),
		ContentBase64: response.ContentBase64,
		SizeBytes:     response.SizeBytes,
	}, nil
}

func (actor POSIXHelperWorkspaceActor) Stat(ctx context.Context, path string) (WorkspaceActorStat, error) {
	var response fsResponse
	errorValue := actor.executeFSWithResponse(ctx, "stat", path, fsRequest{Path: path}, nil, &response)
	if errorValue != nil {
		return WorkspaceActorStat{}, errorValue
	}
	return WorkspaceActorStat{
		Path:           path,
		IsRegular:      response.IsRegular,
		IsDirectory:    response.IsDirectory,
		SizeBytes:      response.SizeBytes,
		ModifiedAtUnix: response.ModifiedAtUnix,
		Mode:           response.Mode,
	}, nil
}

func (actor POSIXHelperWorkspaceActor) executeFS(ctx context.Context, operation string, path string, request fsRequest, stdin io.Reader) error {
	return actor.executeFSWithResponse(ctx, operation, path, request, stdin, nil)
}

func (actor POSIXHelperWorkspaceActor) executeFSWithResponse(ctx context.Context, operation string, path string, request fsRequest, stdin io.Reader, response *fsResponse) error {
	resolvedIdentity, errorValue := ResolveExecutionIdentity(actor.executionIdentity)
	if errorValue != nil {
		return actorError(operation, "resolve_identity", actor.executionIdentity, path, ActorErrorCodeIdentityMissing, errorValue.Error())
	}
	arguments := fsHelperArguments(operation, resolvedIdentity, request)
	executionContext, cancelFunction := context.WithTimeout(ctx, fsHelperOperationTimeout(operation))
	defer cancelFunction()
	command := exec.CommandContext(executionContext, actor.terminalConfiguration.POSIXHelperPath, arguments...)
	if stdin != nil {
		command.Stdin = stdin
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	errorValue = command.Run()
	if executionContext.Err() == context.DeadlineExceeded {
		return actorError(operation, "helper", actor.executionIdentity, path, ActorErrorCodeOperationFailed, "operation timed out")
	}
	if errorValue != nil {
		detail := firstNonEmptyString(strings.TrimSpace(stderr.String()), errorValue.Error())
		code := actorErrorCodeForDetail(detail)
		if isHelperExecutionFailure(errorValue, stderr.String()) {
			code = ActorErrorCodeRuntimeUnavailable
			detail = helperFailureDetail(actor.terminalConfiguration.POSIXHelperPath, operation, detail, nil)
		}
		return actorError(operation, "helper", actor.executionIdentity, path, code, detail)
	}
	if response != nil {
		if errorValue := json.Unmarshal(stdout.Bytes(), response); errorValue != nil {
			return actorError(operation, "decode", actor.executionIdentity, path, ActorErrorCodeOperationFailed, errorValue.Error())
		}
	}
	return nil
}

func helperFailureDetail(helperPath string, operation string, detail string, output []byte) string {
	parts := []string{
		"posix helper " + strings.TrimSpace(operation) + " failed",
		"path=" + strings.TrimSpace(helperPath),
	}
	if trimmedOutput := strings.TrimSpace(string(output)); trimmedOutput != "" {
		parts = append(parts, "output="+trimmedOutput)
	}
	if trimmedDetail := strings.TrimSpace(detail); trimmedDetail != "" {
		parts = append(parts, "detail="+trimmedDetail)
	}
	return strings.Join(parts, "; ")
}

func isHelperExecutionFailure(errorValue error, stderr string) bool {
	return strings.TrimSpace(stderr) == "" && os.IsPermission(errorValue)
}

type fsRequest struct {
	Path         string
	Source       string
	Mode         os.FileMode
	MaxBytes     int64
	Overwrite    bool
	ExcludeNames []string
}

type fsResponse struct {
	IsRegular      bool                           `json:"isRegular"`
	IsDirectory    bool                           `json:"isDirectory"`
	SizeBytes      int64                          `json:"sizeBytes"`
	ModifiedAtUnix int64                          `json:"modifiedAtUnix"`
	Mode           os.FileMode                    `json:"mode"`
	ContentBase64  string                         `json:"contentBase64"`
	Format         string                         `json:"format"`
	Entries        []WorkspaceActorDirectoryEntry `json:"entries"`
}

func fsHelperOperationTimeout(operation string) time.Duration {
	if operation == "bundle_directory" {
		return 180 * time.Second
	}
	return 30 * time.Second
}

func fsHelperArguments(operation string, identity ExecutionIdentity, request fsRequest) []string {
	arguments := []string{
		"fs",
		"--uid", formatUnsignedID(identity.UserID),
		"--gid", formatUnsignedID(identity.GroupID),
		"--groups", joinUnsignedIDs(identity.SupplementaryGroupIDs),
		"--operation", operation,
	}
	if strings.TrimSpace(request.Path) != "" {
		arguments = append(arguments, "--path", request.Path)
	}
	if strings.TrimSpace(request.Source) != "" {
		arguments = append(arguments, "--source", request.Source)
	}
	if request.Mode != 0 {
		arguments = append(arguments, "--mode", fmt.Sprintf("%04o", request.Mode))
	}
	if request.MaxBytes > 0 {
		arguments = append(arguments, "--max-bytes", fmt.Sprintf("%d", request.MaxBytes))
	}
	if request.Overwrite {
		arguments = append(arguments, "--overwrite")
	}
	if len(request.ExcludeNames) > 0 {
		arguments = append(arguments, "--exclude-names", strings.Join(request.ExcludeNames, ","))
	}
	return arguments
}

func actorError(operation string, stage string, identity ExecutionIdentity, path string, code string, detail string) WorkspaceActorError {
	return WorkspaceActorError{
		Operation:   operation,
		Stage:       stage,
		ActorUser:   strings.TrimSpace(identity.UserName),
		VirtualPath: strings.TrimSpace(path),
		Code:        firstNonEmptyString(code, ActorErrorCodeOperationFailed),
		Detail:      firstNonEmptyString(strings.TrimSpace(detail), "operation failed"),
	}
}

func actorErrorCodeForDetail(detail string) string {
	normalizedDetail := strings.ToLower(strings.TrimSpace(detail))
	switch {
	case strings.Contains(normalizedDetail, "permission denied"):
		return ActorErrorCodePermissionDenied
	case strings.Contains(normalizedDetail, "no such file or directory"):
		return ActorErrorCodeNotFound
	case strings.Contains(normalizedDetail, "file exists"):
		return ActorErrorCodeAlreadyExists
	case strings.Contains(normalizedDetail, "not a regular file"):
		return ActorErrorCodeInvalidPath
	default:
		return ActorErrorCodeOperationFailed
	}
}

// A helper built before an operation existed still reports the fs capability,
// so asking it to do that operation fails halfway through a request. Naming the
// operation lets a caller find out first and choose something it can do.
func HelperCanListDirectory(ctx context.Context, helperPath string) bool {
	return helperAdvertises(ctx, helperPath, "fs.list_directory")
}

var advertisedCapabilitiesByHelperPath sync.Map

func helperAdvertises(ctx context.Context, helperPath string, capability string) bool {
	if strings.TrimSpace(helperPath) == "" {
		return false
	}
	if advertised, isKnown := advertisedCapabilitiesByHelperPath.Load(helperPath); isKnown {
		return containsCapability(advertised.([]string), capability)
	}
	executionContext, cancelFunction := context.WithTimeout(ctx, 15*time.Second)
	defer cancelFunction()
	output, errorValue := exec.CommandContext(executionContext, helperPath, "capabilities").CombinedOutput()
	if errorValue != nil {
		return false
	}
	var capabilities helperCapabilities
	if errorValue := json.Unmarshal(output, &capabilities); errorValue != nil {
		return false
	}
	advertisedCapabilitiesByHelperPath.Store(helperPath, capabilities.Capabilities)
	return containsCapability(capabilities.Capabilities, capability)
}

func IsActorNotFoundError(errorValue error) bool {
	var actorError WorkspaceActorError
	if errors.As(errorValue, &actorError) {
		return actorError.Code == ActorErrorCodeNotFound
	}
	return false
}
