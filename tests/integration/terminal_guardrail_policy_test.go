package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

func TestTerminalGuardrailAllowsWorkspaceCommand(t *testing.T) {
	workspaceRootPath := t.TempDir()
	terminalConfiguration := config.TerminalConfiguration{
		Mode:              "native",
		WorkspaceRootPath: workspaceRootPath,
		TimeoutSecond:     10,
		AllowNetwork:      true,
	}

	shellService := security.NewShellService(terminalConfiguration)
	commandResult, errorValue := shellService.RunCommand(context.Background(), security.CommandRequest{
		ExecutableName:       "echo",
		Arguments:            []string{"blueclaw"},
		WorkingDirectoryPath: workspaceRootPath,
	})
	if errorValue != nil {
		t.Fatalf("expected terminal command to succeed: %v", errorValue)
	}
	if strings.TrimSpace(commandResult.Stdout) != "blueclaw" {
		t.Fatalf("expected stdout to match, got %q", commandResult.Stdout)
	}
}

func TestTerminalGuardrailAllowsUnprivilegedExecutable(t *testing.T) {
	workspaceRootPath := t.TempDir()
	commandGuardrailService := security.NewCommandGuardrailService(config.TerminalConfiguration{
		Mode:              "native",
		WorkspaceRootPath: workspaceRootPath,
		TimeoutSecond:     10,
		AllowNetwork:      true,
	})

	_, errorValue := commandGuardrailService.BuildCommandPlan(security.CommandRequest{
		ExecutableName:       "printf",
		Arguments:            []string{"%s", "x"},
		WorkingDirectoryPath: workspaceRootPath,
	})
	if errorValue != nil {
		t.Fatalf("expected POSIX permissions to remain the executable boundary: %v", errorValue)
	}
}

func TestTerminalGuardrailAllowsInlineEval(t *testing.T) {
	workspaceRootPath := t.TempDir()
	commandGuardrailService := security.NewCommandGuardrailService(config.TerminalConfiguration{
		Mode:              "native",
		WorkspaceRootPath: workspaceRootPath,
		TimeoutSecond:     10,
		AllowNetwork:      true,
	})

	_, errorValue := commandGuardrailService.BuildCommandPlan(security.CommandRequest{
		ExecutableName:       "python3",
		Arguments:            []string{"-c", "print('x')"},
		WorkingDirectoryPath: workspaceRootPath,
	})
	if errorValue != nil {
		t.Fatalf("expected inline eval to rely on POSIX and path permissions: %v", errorValue)
	}
}

func TestTerminalGuardrailLeavesPathAccessToPOSIX(t *testing.T) {
	workspaceRootPath := t.TempDir()
	commandGuardrailService := security.NewCommandGuardrailService(config.TerminalConfiguration{
		Mode:              "native",
		WorkspaceRootPath: workspaceRootPath,
		TimeoutSecond:     10,
		AllowNetwork:      true,
	})

	_, errorValue := commandGuardrailService.BuildCommandPlan(security.CommandRequest{
		ExecutableName:       "cat",
		Arguments:            []string{"/etc/passwd"},
		WorkingDirectoryPath: workspaceRootPath,
	})
	if errorValue != nil {
		t.Fatalf("expected the plan to build and file permissions to decide access: %v", errorValue)
	}
}

func TestTerminalGuardrailDeniesUnsupportedSandboxProvider(t *testing.T) {
	workspaceRootPath := t.TempDir()
	commandGuardrailService := security.NewCommandGuardrailService(config.TerminalConfiguration{
		Mode:              "sandbox",
		SandboxProvider:   "firecracker",
		WorkspaceRootPath: workspaceRootPath,
		TimeoutSecond:     10,
		AllowNetwork:      false,
	})

	_, errorValue := commandGuardrailService.BuildCommandPlan(security.CommandRequest{
		ExecutableName:       "echo",
		Arguments:            []string{"blueclaw"},
		WorkingDirectoryPath: workspaceRootPath,
	})
	if errorValue == nil {
		t.Fatal("expected unsupported sandbox provider to be denied")
	}
}
