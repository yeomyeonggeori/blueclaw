package lab

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var errorUnsupportedHostMode = errors.New("unsupported host mode")

type Service struct {
	configuration      Configuration
	commandRunner      CommandRunner
	repositoryRootPath string
}

func NewService(configuration Configuration, commandRunner CommandRunner, repositoryRootPath string) Service {
	if strings.TrimSpace(configuration.VirtualMachine.SharedWorkspacePath) == "" {
		configuration.VirtualMachine.SharedWorkspacePath = repositoryRootPath
	}
	return Service{
		configuration:      configuration,
		commandRunner:      commandRunner,
		repositoryRootPath: repositoryRootPath,
	}
}

func (service Service) ImageBuild(ctx context.Context) error {
	for _, executableCommand := range service.buildImageBuildCommands() {
		errorValue := service.commandRunner.Run(ctx, executableCommand)
		if errorValue != nil {
			return errorValue
		}
	}

	return nil
}

func (service Service) VirtualMachineUp(ctx context.Context) error {
	errorValue := service.commandRunner.Start(ctx, service.buildVirtualMachineUpCommand())
	if errorValue != nil {
		return errorValue
	}

	return service.waitForVirtualMachineIPAddress(ctx)
}

func (service Service) VirtualMachineDown(ctx context.Context) error {
	return service.commandRunner.Run(ctx, service.buildVirtualMachineDownCommand())
}

func (service Service) VirtualMachineSSH(ctx context.Context, remoteArguments []string) error {
	virtualMachineIPAddress, errorValue := service.resolveVirtualMachineIPAddress(ctx)
	if errorValue != nil {
		return errorValue
	}

	commandArguments := []string{service.configuration.VirtualMachine.SSHUsername + "@" + virtualMachineIPAddress}
	commandArguments = append(commandArguments, remoteArguments...)

	return service.commandRunner.Run(ctx, ExecutableCommand{
		ExecutableName:       "ssh",
		Arguments:            commandArguments,
		WorkingDirectoryPath: service.repositoryRootPath,
	})
}

func (service Service) ScenarioBrowserHandoff(ctx context.Context) error {
	if service.configuration.Host.Mode != "single-mac" && service.configuration.Host.Mode != "dual-mac" {
		return errorUnsupportedHostMode
	}

	return service.runScenarioScript(ctx, filepath.Join("lab", "scripts", "scenario-browser-handoff.sh"), []string{
		service.configuration.Host.Mode,
		service.configuration.Host.Companion.ListenAddress,
		service.configuration.Host.Companion.CallbackBaseURL,
	})
}

func (service Service) ScenarioMattermost(ctx context.Context) error {
	return service.runScenarioScript(ctx, filepath.Join("lab", "scripts", "scenario-mattermost.sh"), []string{
		service.configuration.VirtualMachine.Mattermost.ListenAddress,
		service.configuration.VirtualMachine.MountDirectoryPath,
	})
}

func (service Service) ScenarioSlack(ctx context.Context) error {
	return service.runScenarioScript(ctx, filepath.Join("lab", "scripts", "scenario-slack.sh"), []string{
		service.configuration.Host.Mode,
	})
}

func (service Service) buildImageBuildCommands() []ExecutableCommand {
	return []ExecutableCommand{
		{
			ExecutableName: service.configuration.VirtualMachine.Tart.BinaryPath,
			Arguments: []string{
				"clone",
				service.configuration.VirtualMachine.Tart.Image,
				service.configuration.VirtualMachine.Tart.Name,
			},
			WorkingDirectoryPath: service.repositoryRootPath,
		},
		{
			ExecutableName: service.configuration.VirtualMachine.Tart.BinaryPath,
			Arguments: []string{
				"set",
				service.configuration.VirtualMachine.Tart.Name,
				"--cpu",
				formatInteger(service.configuration.VirtualMachine.Tart.CPUCount),
				"--memory",
				formatInteger(service.configuration.VirtualMachine.Tart.MemoryMiB),
				"--disk-size",
				formatInteger(service.configuration.VirtualMachine.Tart.DiskGiB),
			},
			WorkingDirectoryPath: service.repositoryRootPath,
		},
	}
}

func (service Service) buildVirtualMachineUpCommand() ExecutableCommand {
	commandArguments := []string{
		"run",
	}
	if service.configuration.VirtualMachine.Tart.NestedEnabled {
		commandArguments = append(commandArguments, "--nested")
	}
	commandArguments = append(commandArguments,
		"--dir=workspace:"+service.configuration.VirtualMachine.SharedWorkspacePath,
		service.configuration.VirtualMachine.Tart.Name,
	)

	return ExecutableCommand{
		ExecutableName:       service.configuration.VirtualMachine.Tart.BinaryPath,
		Arguments:            commandArguments,
		WorkingDirectoryPath: service.repositoryRootPath,
	}
}

func (service Service) buildVirtualMachineDownCommand() ExecutableCommand {
	return ExecutableCommand{
		ExecutableName: service.configuration.VirtualMachine.Tart.BinaryPath,
		Arguments: []string{
			"stop",
			service.configuration.VirtualMachine.Tart.Name,
		},
		WorkingDirectoryPath: service.repositoryRootPath,
	}
}

func (service Service) runScenarioScript(ctx context.Context, relativeScriptPath string, scriptArguments []string) error {
	virtualMachineIPAddress, errorValue := service.resolveVirtualMachineIPAddress(ctx)
	if errorValue != nil {
		return errorValue
	}

	remoteScriptArguments := append([]string{service.configuration.VirtualMachine.SSHPassword}, scriptArguments...)
	commandArguments := []string{
		"-p",
		service.configuration.VirtualMachine.SSHPassword,
		"ssh",
		"-o",
		"StrictHostKeyChecking no",
		"-o",
		"UserKnownHostsFile=/dev/null",
		service.configuration.VirtualMachine.SSHUsername + "@" + virtualMachineIPAddress,
		"bash -s -- " + shellEscapeArguments(remoteScriptArguments),
	}

	return service.commandRunner.Run(ctx, ExecutableCommand{
		ExecutableName:       "sshpass",
		Arguments:            commandArguments,
		WorkingDirectoryPath: service.repositoryRootPath,
		StandardInputPath:    filepath.Join(service.repositoryRootPath, relativeScriptPath),
	})
}

func (service Service) resolveVirtualMachineIPAddress(ctx context.Context) (string, error) {
	output, errorValue := service.commandRunner.Output(ctx, ExecutableCommand{
		ExecutableName: service.configuration.VirtualMachine.Tart.BinaryPath,
		Arguments: []string{
			"ip",
			service.configuration.VirtualMachine.Tart.Name,
		},
		WorkingDirectoryPath: service.repositoryRootPath,
	})
	if errorValue != nil {
		return "", errorValue
	}

	return strings.TrimSpace(output), nil
}

func (service Service) waitForVirtualMachineIPAddress(ctx context.Context) error {
	for range 60 {
		virtualMachineIPAddress, errorValue := service.resolveVirtualMachineIPAddress(ctx)
		if errorValue == nil && virtualMachineIPAddress != "" {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	return errors.New("virtual machine did not report an ip address")
}

func shellEscapeArguments(arguments []string) string {
	escapedArguments := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		escapedArguments = append(escapedArguments, "'"+strings.ReplaceAll(argument, "'", "'\"'\"'")+"'")
	}

	return strings.Join(escapedArguments, " ")
}

func formatInteger(value int) string {
	return strconv.Itoa(value)
}

func formatUnsignedInteger(value uint32) string {
	return formatInteger(int(value))
}
