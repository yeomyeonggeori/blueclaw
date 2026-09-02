package lab

import (
	"context"
	"strings"
	"testing"
)

type fakeCommandRunner struct {
	runCommands    []ExecutableCommand
	startCommands  []ExecutableCommand
	outputCommands []ExecutableCommand
	outputValue    string
}

func (fakeCommandRunner *fakeCommandRunner) Run(ctx context.Context, executableCommand ExecutableCommand) error {
	_ = ctx
	fakeCommandRunner.runCommands = append(fakeCommandRunner.runCommands, executableCommand)
	return nil
}

func (fakeCommandRunner *fakeCommandRunner) Start(ctx context.Context, executableCommand ExecutableCommand) error {
	_ = ctx
	fakeCommandRunner.startCommands = append(fakeCommandRunner.startCommands, executableCommand)
	return nil
}

func (fakeCommandRunner *fakeCommandRunner) Output(ctx context.Context, executableCommand ExecutableCommand) (string, error) {
	_ = ctx
	fakeCommandRunner.outputCommands = append(fakeCommandRunner.outputCommands, executableCommand)
	return fakeCommandRunner.outputValue, nil
}

func TestImageBuildUsesCloneAndSetCommands(t *testing.T) {
	commandRunner := &fakeCommandRunner{}
	service := NewService(buildTestConfiguration(), commandRunner, "/repo")

	errorValue := service.ImageBuild(context.Background())
	if errorValue != nil {
		t.Fatalf("expected image build to succeed: %v", errorValue)
	}
	if len(commandRunner.runCommands) != 2 {
		t.Fatalf("expected image build to emit 2 commands, got %d", len(commandRunner.runCommands))
	}
	if commandRunner.runCommands[0].ExecutableName != "tart" {
		t.Fatalf("expected tart clone command, got %q", commandRunner.runCommands[0].ExecutableName)
	}
	if commandRunner.runCommands[0].Arguments[0] != "clone" {
		t.Fatalf("expected clone subcommand, got %q", commandRunner.runCommands[0].Arguments[0])
	}
	if commandRunner.runCommands[1].Arguments[0] != "set" {
		t.Fatalf("expected set subcommand, got %q", commandRunner.runCommands[1].Arguments[0])
	}
}

func TestVirtualMachineUpUsesNestedRunWithSharedDirectory(t *testing.T) {
	commandRunner := &fakeCommandRunner{outputValue: "10.0.0.5\n"}
	service := NewService(buildTestConfiguration(), commandRunner, "/repo")

	errorValue := service.VirtualMachineUp(context.Background())
	if errorValue != nil {
		t.Fatalf("expected vm up to succeed: %v", errorValue)
	}
	if len(commandRunner.startCommands) != 1 {
		t.Fatalf("expected vm up to start one command, got %d", len(commandRunner.startCommands))
	}

	commandArguments := strings.Join(commandRunner.startCommands[0].Arguments, " ")
	if !strings.Contains(commandArguments, "--nested") {
		t.Fatalf("expected nested tart run, got %q", commandArguments)
	}
	if !strings.Contains(commandArguments, "--dir=workspace:/Users/test/workspace") {
		t.Fatalf("expected shared workspace mount, got %q", commandArguments)
	}
}

func TestVirtualMachineUpDefaultsSharedDirectoryToRepositoryRoot(t *testing.T) {
	commandRunner := &fakeCommandRunner{outputValue: "10.0.0.5\n"}
	configuration := buildTestConfiguration()
	configuration.VirtualMachine.SharedWorkspacePath = ""
	service := NewService(configuration, commandRunner, "/repo")

	errorValue := service.VirtualMachineUp(context.Background())
	if errorValue != nil {
		t.Fatalf("expected vm up to succeed: %v", errorValue)
	}

	commandArguments := strings.Join(commandRunner.startCommands[0].Arguments, " ")
	if !strings.Contains(commandArguments, "--dir=workspace:/repo") {
		t.Fatalf("expected repository root shared mount, got %q", commandArguments)
	}
}

func buildTestConfiguration() Configuration {
	return applyDefaultConfiguration(Configuration{
		Host: HostConfiguration{
			Mode: "single-mac",
			Companion: CompanionConfiguration{
				ListenAddress:   "127.0.0.1:7780",
				CallbackBaseURL: "http://127.0.0.1:7780/callback",
			},
		},
		VirtualMachine: VirtualMachineConfiguration{
			Tart: TartConfiguration{
				Name:          "blueclaw-dev",
				Image:         "ghcr.io/cirruslabs/ubuntu:latest",
				NestedEnabled: true,
				CPUCount:      6,
				MemoryMiB:     8192,
				DiskGiB:       80,
			},
			Mattermost: MattermostConfiguration{
				ListenAddress: "127.0.0.1:8065",
			},
			SharedWorkspacePath: "/Users/test/workspace",
		},
	})
}
