package security

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
)

func TestCommandPlanUsesPOSIXHelperForExecutionIdentity(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root cannot build terminal command plans")
	}

	currentUser, errorValue := user.Current()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	currentGroup, errorValue := user.LookupGroupId(currentUser.Gid)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	workspaceRootPath := t.TempDir()
	commandGuardrailService := NewCommandGuardrailService(config.TerminalConfiguration{
		Mode:                  "firecrackerGuest",
		WorkspaceRootPath:     workspaceRootPath,
		AllowNetwork:          true,
		AllowInteractiveShell: true,
		POSIXHelperPath:       "/usr/local/bin/blueclaw-posix-helper",
		TimeoutSecond:         3,
	})

	commandPlan, errorValue := commandGuardrailService.BuildCommandPlan(CommandRequest{
		Command: "printf ready",
		ExecutionIdentity: ExecutionIdentity{
			UserName:          currentUser.Username,
			GroupName:         currentGroup.Name,
			HomeDirectoryPath: workspaceRootPath,
		},
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if commandPlan.ExecutablePath != "/usr/local/bin/blueclaw-posix-helper" {
		t.Fatalf("expected POSIX helper executable, got %+v", commandPlan)
	}
	if len(commandPlan.Arguments) < 12 || commandPlan.Arguments[0] != "exec" {
		t.Fatalf("expected POSIX helper arguments, got %+v", commandPlan.Arguments)
	}
	if !strings.Contains(strings.Join(commandPlan.Arguments, " "), "--cwd "+workspaceRootPath) {
		t.Fatalf("expected helper cwd argument, got %+v", commandPlan.Arguments)
	}
	if commandPlan.WorkingDirectoryPath != workspaceRootPath {
		t.Fatalf("expected helper process to start from workspace root, got %+v", commandPlan)
	}
	if commandPlan.EnvironmentVariables["HOME"] != workspaceRootPath {
		t.Fatalf("expected POSIX HOME environment, got %+v", commandPlan.EnvironmentVariables)
	}
	if commandPlan.EnvironmentVariables["PATH"] != CanonicalRuntimePATH {
		t.Fatalf("expected canonical runtime PATH, got %+v", commandPlan.EnvironmentVariables)
	}
	if commandPlan.EnvironmentVariables["BLUECLAW_REQUESTER_TMP"] != workspaceRootPath+"/tmp" {
		t.Fatalf("expected requester tmp environment, got %+v", commandPlan.EnvironmentVariables)
	}
	if _, isPresent := commandPlan.EnvironmentVariables["BLUECLAW_TASK_TMP"]; isPresent {
		t.Fatalf("expected no task tmp environment outside a task run, got %+v", commandPlan.EnvironmentVariables)
	}
	if commandPlan.EnvironmentVariables["BLUECLAW_REQUESTER_ARTIFACTS"] != workspaceRootPath+"/artifacts" {
		t.Fatalf("expected requester artifacts environment, got %+v", commandPlan.EnvironmentVariables)
	}
	if commandPlan.EnvironmentVariables["TMPDIR"] != workspaceRootPath+"/tmp/.runtime/tmp" {
		t.Fatalf("expected requester runtime tmp environment, got %+v", commandPlan.EnvironmentVariables)
	}
	if commandPlan.EnvironmentVariables["BUN_TMPDIR"] != workspaceRootPath+"/tmp/.runtime/bun/tmp" {
		t.Fatalf("expected requester Bun tmp environment, got %+v", commandPlan.EnvironmentVariables)
	}
	if commandPlan.EnvironmentVariables["BUN_INSTALL"] != workspaceRootPath+"/tmp/.runtime/bun/install" {
		t.Fatalf("expected requester Bun install environment, got %+v", commandPlan.EnvironmentVariables)
	}
	if commandPlan.Timeout != 3*time.Second {
		t.Fatalf("expected timeout to survive POSIX wrapping, got %+v", commandPlan.Timeout)
	}
}

func TestSanitizeEnvironmentIgnoresRequesterPATH(t *testing.T) {
	environmentVariables := sanitizeEnvironmentVariables(map[string]string{
		"PATH":                           "/workspace/private/people/person-1/bin",
		"HOME":                           "/workspace/private/people/person-1",
		"BLUECLAW_BUILTIN_SKILLS_PYTHON": "/opt/blueclaw/builtin-skills-venv/bin/python",
	}, "/workspace")

	if environmentVariables["PATH"] != CanonicalRuntimePATH {
		t.Fatalf("expected requester PATH to be ignored, got %+v", environmentVariables)
	}
	if environmentVariables["BLUECLAW_BUILTIN_SKILLS_PYTHON"] != "/opt/blueclaw/builtin-skills-venv/bin/python" {
		t.Fatalf("expected managed skills runtime to survive sanitization, got %+v", environmentVariables)
	}
}

func TestApplyPOSIXEnvironmentPreservesCanonicalPATH(t *testing.T) {
	environmentVariables := applyPOSIXEnvironment(map[string]string{
		"PATH": "/workspace/private/people/person-1/bin",
	}, ExecutionIdentity{
		HomeDirectoryPath: "/workspace/private/people/person-1",
	})

	if environmentVariables["PATH"] != CanonicalRuntimePATH {
		t.Fatalf("expected canonical runtime PATH after POSIX environment, got %+v", environmentVariables)
	}
}

func TestCommandPlanKeepsPrivateCWDInsideHelperArguments(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root cannot build terminal command plans")
	}

	currentUser, errorValue := user.Current()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	currentGroup, errorValue := user.LookupGroupId(currentUser.Gid)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	workspaceRootPath := t.TempDir()
	privateWorkingDirectoryPath := workspaceRootPath + "/private/people/person-1/tmp/task/deck"
	commandGuardrailService := NewCommandGuardrailService(config.TerminalConfiguration{
		Mode:                  "firecrackerGuest",
		WorkspaceRootPath:     workspaceRootPath,
		AllowNetwork:          true,
		AllowInteractiveShell: true,
		POSIXHelperPath:       "/usr/local/bin/blueclaw-posix-helper",
		TimeoutSecond:         3,
	})

	commandPlan, errorValue := commandGuardrailService.BuildCommandPlan(CommandRequest{
		Command:              "pwd",
		WorkingDirectoryPath: privateWorkingDirectoryPath,
		ExecutionIdentity: ExecutionIdentity{
			UserName:          currentUser.Username,
			GroupName:         currentGroup.Name,
			HomeDirectoryPath: workspaceRootPath,
		},
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if commandPlan.WorkingDirectoryPath != workspaceRootPath {
		t.Fatalf("expected helper process cwd to avoid requester-private path, got %+v", commandPlan)
	}
	if !strings.Contains(strings.Join(commandPlan.Arguments, " "), "--cwd "+privateWorkingDirectoryPath) {
		t.Fatalf("expected requester cwd only in helper arguments, got %+v", commandPlan.Arguments)
	}
}

func TestCommandPlanDefersPathAccessToPOSIXPermissions(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root cannot build terminal command plans")
	}

	workspaceRootPath := t.TempDir()
	commandGuardrailService := NewCommandGuardrailService(config.TerminalConfiguration{
		Mode:                  "firecrackerGuest",
		WorkspaceRootPath:     workspaceRootPath,
		AllowNetwork:          true,
		AllowInteractiveShell: true,
		TimeoutSecond:         3,
	})

	commandPlan, errorValue := commandGuardrailService.BuildCommandPlan(CommandRequest{
		Command:              "cat /etc/passwd",
		WorkingDirectoryPath: workspaceRootPath,
	})

	if errorValue != nil {
		t.Fatalf("expected operating system permissions to remain the path boundary: %v", errorValue)
	}
	if commandPlan.Stdin != "cat /etc/passwd" {
		t.Fatalf("expected command to pass through unchanged, got %q", commandPlan.Stdin)
	}
}

func TestCommandGuardrailAllowsWorkspaceCapabilityCLIExecutable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root cannot build terminal command plans")
	}

	workspaceRootPath := t.TempDir()
	capabilityPath := workspaceRootPath + "/tools/capability"
	if errorValue := os.MkdirAll(workspaceRootPath+"/tools", 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(capabilityPath, []byte("#!/bin/sh\n"), 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	commandGuardrailService := NewCommandGuardrailService(config.TerminalConfiguration{
		Mode:                  "firecrackerGuest",
		WorkspaceRootPath:     workspaceRootPath,
		AllowNetwork:          true,
		AllowInteractiveShell: true,
		TimeoutSecond:         3,
	})

	commandPlan, errorValue := commandGuardrailService.BuildCommandPlan(CommandRequest{
		ExecutableName:       capabilityPath,
		Arguments:            []string{"list"},
		WorkingDirectoryPath: workspaceRootPath,
	})

	if errorValue != nil {
		t.Fatalf("expected workspace capability CLI to be allowed, got %v", errorValue)
	}
	expectedCapabilityPath, errorValue := filepath.EvalSymlinks(capabilityPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if commandPlan.ExecutablePath != expectedCapabilityPath {
		t.Fatalf("expected capability executable path, got %+v", commandPlan)
	}
}
