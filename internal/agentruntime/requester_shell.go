package agentruntime

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type requesterShellCommand struct {
	Command            string
	Stdin              string
	TimeoutSecond      int
	OutputMaximumBytes int
}

type requesterShellOutcome struct {
	CommandResult     security.CommandResult
	ExecutionIdentity security.ExecutionIdentity
	RunError          error
}

func (toolCatalogBuilder *ToolCatalogBuilder) runRequesterShell(toolContext context.Context, request ToolCatalogRequest, shellCommand requesterShellCommand) (requesterShellOutcome, *toolcontract.ToolResult) {
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, request)
	if actorFailure != nil {
		return requesterShellOutcome{}, actorFailure
	}
	executionIdentity := toolCatalogBuilder.executionIdentityForRequester(request)
	commandResult, runError := workspaceActor.Run(toolContext, security.CommandRequest{
		Command:              requesterShellScript(shellCommand.Command),
		Stdin:                shellCommand.Stdin,
		EnvironmentVariables: requesterShellEnvironment(executionIdentity),
		TimeoutSecond:        shellCommand.TimeoutSecond,
		OutputMaximumBytes:   shellCommand.OutputMaximumBytes,
		ExecutionIdentity:    executionIdentity,
	})
	return requesterShellOutcome{
		CommandResult:     commandResult,
		ExecutionIdentity: executionIdentity,
		RunError:          runError,
	}, nil
}

func requesterShellScript(command string) string {
	return `mkdir -p -- "$HOME" && cd -- "$HOME" || exit 1` + "\n" + command
}

func requesterShellEnvironment(executionIdentity security.ExecutionIdentity) map[string]string {
	if strings.TrimSpace(executionIdentity.HomeDirectoryPath) == "" {
		return nil
	}
	return map[string]string{"HOME": executionIdentity.HomeDirectoryPath}
}

func shellPathArgument(path string) string {
	if path == "~" {
		return `"$HOME"`
	}
	if strings.HasPrefix(path, "~/") {
		return `"$HOME"/` + shellSingleQuoted(strings.TrimPrefix(path, "~/"))
	}
	return shellSingleQuoted(path)
}

func (outcome requesterShellOutcome) toolFailure(operation string, stage string, path string) toolcontract.ToolResult {
	return actorToolFailure(operation, stage, path, outcome.actorError(operation, path))
}

func (outcome requesterShellOutcome) actorError(operation string, path string) security.WorkspaceActorError {
	return security.WorkspaceActorError{
		Operation:   operation,
		Stage:       "shell",
		ActorUser:   outcome.ExecutionIdentity.UserName,
		VirtualPath: path,
		Code:        outcome.failureCode(),
		Detail:      outcome.failureDetail(),
	}
}

func (outcome requesterShellOutcome) failureCode() string {
	detail := strings.ToLower(outcome.CommandResult.Stderr)
	switch {
	case strings.Contains(detail, "no such file or directory"):
		return security.ActorErrorCodeNotFound
	case strings.Contains(detail, "permission denied"), strings.Contains(detail, "operation not permitted"), strings.Contains(detail, "read-only file system"):
		return security.ActorErrorCodePermissionDenied
	case strings.Contains(detail, "is a directory"):
		return security.ActorErrorCodeInvalidPath
	default:
		return security.ActorErrorCodeOperationFailed
	}
}

func (outcome requesterShellOutcome) failureDetail() string {
	if detail := strings.TrimSpace(outcome.CommandResult.Stderr); detail != "" {
		return detail
	}
	if outcome.RunError != nil {
		return outcome.RunError.Error()
	}
	return "command failed"
}
