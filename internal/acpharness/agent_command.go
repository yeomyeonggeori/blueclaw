package acpharness

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

type AgentCommand struct {
	Path             string
	Arguments        []string
	WorkingDirectory string
	Environment      []string
}

func (agentCommand AgentCommand) Start(ctx context.Context) (io.Writer, io.Reader, func() error, error) {
	if strings.TrimSpace(agentCommand.Path) == "" {
		return nil, nil, nil, errors.New("acp harness needs an agent command to run")
	}
	command := exec.CommandContext(ctx, agentCommand.Path, agentCommand.Arguments...)
	command.Dir = agentCommand.WorkingDirectory
	command.Env = agentCommand.Environment
	agentInput, errorValue := command.StdinPipe()
	if errorValue != nil {
		return nil, nil, nil, errorValue
	}
	agentOutput, errorValue := command.StdoutPipe()
	if errorValue != nil {
		return nil, nil, nil, errorValue
	}
	if errorValue := command.Start(); errorValue != nil {
		return nil, nil, nil, errorValue
	}
	return agentInput, agentOutput, func() error {
		_ = agentInput.Close()
		return command.Wait()
	}, nil
}

func (agentCommand AgentCommand) StartAsRequester(ctx context.Context, processStarter security.WorkspaceProcessStarter, turnWorkingDirectoryPath string) (io.Writer, io.Reader, func() error, error) {
	if strings.TrimSpace(agentCommand.Path) == "" {
		return nil, nil, nil, errors.New("acp harness needs an agent command to run")
	}
	workingDirectoryPath := agentCommand.WorkingDirectory
	if strings.TrimSpace(workingDirectoryPath) == "" {
		workingDirectoryPath = turnWorkingDirectoryPath
	}
	streamingProcess, errorValue := processStarter.StartProcess(ctx, security.CommandRequest{
		ExecutableName:       agentCommand.Path,
		Arguments:            agentCommand.Arguments,
		WorkingDirectoryPath: workingDirectoryPath,
		EnvironmentVariables: environmentVariableMap(agentCommand.Environment),
		IsHarnessAgent:       true,
		IsInteractive:        true,
	})
	if errorValue != nil {
		return nil, nil, nil, errorValue
	}
	return streamingProcess.Input, streamingProcess.Output, streamingProcess.Wait, nil
}

func environmentVariableMap(environment []string) map[string]string {
	if len(environment) == 0 {
		return nil
	}
	environmentVariables := map[string]string{}
	for _, entry := range environment {
		name, value, hasValue := strings.Cut(entry, "=")
		if !hasValue || strings.TrimSpace(name) == "" {
			continue
		}
		environmentVariables[name] = value
	}
	return environmentVariables
}
