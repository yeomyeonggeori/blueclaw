package security

import (
	"context"
	"io"
	"os/exec"
)

type StreamingProcess struct {
	Input  io.WriteCloser
	Output io.ReadCloser
	Wait   func() error
}

type WorkspaceProcessStarter interface {
	StartProcess(context.Context, CommandRequest) (StreamingProcess, error)
}

func (shellService *ShellService) StartStreamingCommand(ctx context.Context, commandRequest CommandRequest) (StreamingProcess, error) {
	commandPlan, errorValue := shellService.commandGuardrailService.BuildCommandPlan(commandRequest)
	if errorValue != nil {
		return StreamingProcess{}, errorValue
	}
	if errorValue := shellService.prepareWorkingDirectory(commandPlan.WorkingDirectoryPath); errorValue != nil {
		return StreamingProcess{}, errorValue
	}

	command := exec.CommandContext(ctx, commandPlan.ExecutablePath, commandPlan.Arguments...)
	configureCommandGroupKill(command)
	command.Dir = commandPlan.WorkingDirectoryPath
	command.Env = mapEnvironmentVariables(commandPlan.EnvironmentVariables)

	standardInput, errorValue := command.StdinPipe()
	if errorValue != nil {
		return StreamingProcess{}, errorValue
	}
	standardOutput, errorValue := command.StdoutPipe()
	if errorValue != nil {
		return StreamingProcess{}, errorValue
	}
	if errorValue := command.Start(); errorValue != nil {
		return StreamingProcess{}, errorValue
	}
	return StreamingProcess{
		Input:  standardInput,
		Output: standardOutput,
		Wait: func() error {
			_ = standardInput.Close()
			return command.Wait()
		},
	}, nil
}

func (actor POSIXHelperWorkspaceActor) StartProcess(ctx context.Context, commandRequest CommandRequest) (StreamingProcess, error) {
	return startProcessWithIdentity(ctx, actor.terminalService, actor.executionIdentity, commandRequest)
}

func (actor DirectWorkspaceActor) StartProcess(ctx context.Context, commandRequest CommandRequest) (StreamingProcess, error) {
	return startProcessWithIdentity(ctx, actor.terminalService, actor.identity, commandRequest)
}

func startProcessWithIdentity(ctx context.Context, terminalService *ShellService, identity ExecutionIdentity, commandRequest CommandRequest) (StreamingProcess, error) {
	if terminalService == nil {
		return StreamingProcess{}, actorError("start_process", "identity", identity, "", ActorErrorCodeIdentityMissing, ActorErrorCodeIdentityMissing)
	}
	if commandRequest.ExecutionIdentity.UserName == "" {
		commandRequest.ExecutionIdentity = identity
	}
	if commandRequest.ExecutionIdentity.UserName == "" {
		return StreamingProcess{}, actorError("start_process", "identity", identity, "", ActorErrorCodeIdentityMissing, ActorErrorCodeIdentityMissing)
	}
	return terminalService.StartStreamingCommand(ctx, commandRequest)
}
