package security

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
)

func TestRunCommandUsesBashStdinInFirecrackerGuestMode(t *testing.T) {
	terminalSessionService := NewTerminalSessionService(testTerminalConfiguration(t))

	commandResult, errorValue := terminalSessionService.RunCommand(context.Background(), CommandRequest{
		Command: "printf 'blueclaw\\n'",
	})

	if errorValue != nil {
		t.Fatalf("expected command to succeed: %v", errorValue)
	}
	if commandResult.Stdout != "blueclaw\n" || commandResult.ExitCode != 0 {
		t.Fatalf("expected bash stdout and exit code, got %+v", commandResult)
	}
}

func TestRunCommandRequiresPreparedWorkspaceWorkingDirectoryInFirecrackerGuestMode(t *testing.T) {
	terminalConfiguration := testTerminalConfiguration(t)
	terminalSessionService := NewTerminalSessionService(terminalConfiguration)
	workingDirectoryPath := terminalConfiguration.WorkspaceRootPath + "/.blueclaw/tmp/slides"

	commandResult, errorValue := terminalSessionService.RunCommand(context.Background(), CommandRequest{
		Command:              "printf 'ready' > result.txt",
		WorkingDirectoryPath: workingDirectoryPath,
	})

	if errorValue == nil {
		t.Fatalf("expected missing working directory to fail, got %+v", commandResult)
	}
	if !strings.Contains(commandResult.Stderr, "no such file or directory") {
		t.Fatalf("expected missing directory detail, got %+v", commandResult)
	}
}

func TestRunCommandReportsTimeout(t *testing.T) {
	terminalSessionService := NewTerminalSessionService(testTerminalConfiguration(t))

	commandResult, errorValue := terminalSessionService.RunCommand(context.Background(), CommandRequest{
		Command:       "sleep 2",
		TimeoutSecond: 1,
	})

	if errorValue == nil || !commandResult.TimedOut {
		t.Fatalf("expected timed out command result, got result=%+v error=%v", commandResult, errorValue)
	}
}

func TestRunCommandTimeoutKillsChildProcesses(t *testing.T) {
	terminalSessionService := NewTerminalSessionService(testTerminalConfiguration(t))
	startedAt := time.Now()

	commandResult, errorValue := terminalSessionService.RunCommand(context.Background(), CommandRequest{
		Command:       "sh -c 'sleep 30 & wait'",
		TimeoutSecond: 1,
	})

	if errorValue == nil || !commandResult.TimedOut {
		t.Fatalf("expected timed out command result, got result=%+v error=%v", commandResult, errorValue)
	}
	if time.Since(startedAt) > 5*time.Second {
		t.Fatalf("expected process group timeout to return quickly, took %s", time.Since(startedAt))
	}
}

func TestAwaitCommandCompletionAbandonsUnreapableCommand(t *testing.T) {
	ctx, cancelFunction := context.WithCancel(context.Background())
	cancelFunction()
	runResult := make(chan error)

	startedAt := time.Now()
	errorValue, abandoned := awaitCommandCompletion(ctx, runResult, 500*time.Millisecond)
	elapsed := time.Since(startedAt)

	if !abandoned {
		t.Fatalf("expected command to be reported as abandoned, got errorValue=%v", errorValue)
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("expected abandonment to resolve within grace period, took %s", elapsed)
	}
}

func TestRunCommandTimedOutResultIncludesPartialOutput(t *testing.T) {
	terminalSessionService := NewTerminalSessionService(testTerminalConfiguration(t))

	commandResult, errorValue := terminalSessionService.RunCommand(context.Background(), CommandRequest{
		Command:       "sh -c 'printf partial; sleep 30'",
		TimeoutSecond: 1,
	})

	if errorValue == nil || !commandResult.TimedOut {
		t.Fatalf("expected timed out command result, got result=%+v error=%v", commandResult, errorValue)
	}
	if !strings.Contains(commandResult.Stdout, "partial") {
		t.Fatalf("expected partial stdout to be preserved, got %+v", commandResult)
	}
}

func TestProcessIDsWithEnvironmentMarkerMatchesExactRecords(t *testing.T) {
	procRootPath := t.TempDir()
	writeFakeProcessEnviron(t, procRootPath, "101", "PATH=/bin\x00BLUECLAW_TERMINAL_SCOPE=7-3\x00HOME=/tmp\x00")
	writeFakeProcessEnviron(t, procRootPath, "102", "BLUECLAW_TERMINAL_SCOPE=7-30\x00")
	writeFakeProcessEnviron(t, procRootPath, "103", "OTHER=1\x00")
	if errorValue := os.MkdirAll(filepath.Join(procRootPath, "not-a-pid"), 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}

	processIDs := processIDsWithEnvironmentMarker(procRootPath, "BLUECLAW_TERMINAL_SCOPE=7-3")

	if len(processIDs) != 1 || processIDs[0] != 101 {
		t.Fatalf("expected only process 101 to match, got %v", processIDs)
	}
}

func writeFakeProcessEnviron(t *testing.T, procRootPath string, processID string, environ string) {
	t.Helper()
	if errorValue := os.MkdirAll(filepath.Join(procRootPath, processID), 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(procRootPath, processID, "environ"), []byte(environ), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func TestRunCommandIncludesProcessErrorWhenStderrIsEmpty(t *testing.T) {
	terminalSessionService := NewTerminalSessionService(testTerminalConfiguration(t))

	commandResult, errorValue := terminalSessionService.RunCommand(context.Background(), CommandRequest{
		Command: "definitely_missing_blueclaw_binary",
	})

	if errorValue == nil {
		t.Fatal("expected command error")
	}
	if strings.TrimSpace(commandResult.Stderr) == "" {
		t.Fatalf("expected stderr to explain the process error, got %+v", commandResult)
	}
}

func TestRunCommandReportsTrimmedOutput(t *testing.T) {
	terminalConfiguration := testTerminalConfiguration(t)
	terminalConfiguration.OutputMaxBytes = 16
	terminalSessionService := NewTerminalSessionService(terminalConfiguration)

	commandResult, errorValue := terminalSessionService.RunCommand(context.Background(), CommandRequest{
		Command: "printf 'abcdefghijklmnopqrstuvwxyz'",
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !commandResult.OutputTrimmed || len(commandResult.Stdout) > terminalConfiguration.OutputMaxBytes {
		t.Fatalf("expected explicit bounded output, got %+v", commandResult)
	}
}

func TestTerminalSessionUsesPTYLifecycle(t *testing.T) {
	terminalSessionService := NewTerminalSessionService(testTerminalConfiguration(t))
	workspaceRootPath := terminalSessionService.commandGuardrailService.terminalConfiguration.WorkspaceRootPath

	sessionID, errorValue := terminalSessionService.StartInteractiveSession(CommandRequest{
		WorkingDirectoryPath: workspaceRootPath,
		IsPTY:                true,
	})
	if errorValue != nil {
		t.Fatalf("expected PTY session to start: %v", errorValue)
	}
	defer terminalSessionService.CloseSession(sessionID)

	writeStatus, errorValue := terminalSessionService.WriteSessionInput(sessionID, "printf 'hello-pty\\n'\n")
	if errorValue != nil {
		t.Fatalf("expected PTY write to succeed: %v", errorValue)
	}
	if writeStatus.SessionID != sessionID || writeStatus.Status != "running" {
		t.Fatalf("expected PTY write to preserve session status, got %+v", writeStatus)
	}

	var sessionStatus TerminalSessionStatus
	for range 20 {
		sessionStatus, errorValue = terminalSessionService.StatusSession(sessionID)
		if errorValue != nil {
			t.Fatalf("expected PTY status: %v", errorValue)
		}
		if strings.Contains(sessionStatus.RecentOutput, "hello-pty") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if sessionStatus.SessionID != sessionID || sessionStatus.Status != "running" {
		t.Fatalf("expected running PTY session, got %+v", sessionStatus)
	}
	if !strings.Contains(sessionStatus.RecentOutput, "hello-pty") {
		t.Fatalf("expected recent PTY output, got %+v", sessionStatus)
	}
}

func TestTerminalSessionLimit(t *testing.T) {
	terminalConfiguration := testTerminalConfiguration(t)
	terminalConfiguration.SessionMaxCount = 1
	terminalSessionService := NewTerminalSessionService(terminalConfiguration)
	workspaceRootPath := terminalConfiguration.WorkspaceRootPath

	sessionID, errorValue := terminalSessionService.StartInteractiveSession(CommandRequest{
		WorkingDirectoryPath: workspaceRootPath,
		IsPTY:                true,
	})
	if errorValue != nil {
		t.Fatalf("expected first session to start: %v", errorValue)
	}
	defer terminalSessionService.CloseSession(sessionID)

	_, errorValue = terminalSessionService.StartInteractiveSession(CommandRequest{
		WorkingDirectoryPath: workspaceRootPath,
		IsPTY:                true,
	})
	if errorValue == nil || !strings.Contains(errorValue.Error(), "session limit") {
		t.Fatalf("expected session cap error, got %v", errorValue)
	}
}

func TestRunCommandTimeoutReturnsFailureResultWithPartialOutput(t *testing.T) {
	terminalSessionService := NewTerminalSessionService(testTerminalConfiguration(t))
	startedAt := time.Now()

	commandResult, errorValue := terminalSessionService.RunCommand(context.Background(), CommandRequest{
		Command:       "echo before-stall; sleep 30",
		TimeoutSecond: 1,
	})

	if errorValue == nil {
		t.Fatalf("expected timeout error, got result=%+v", commandResult)
	}
	if !commandResult.TimedOut {
		t.Fatalf("expected timed out command result, got %+v", commandResult)
	}
	if commandResult.ExitCode != -1 {
		t.Fatalf("expected exit code -1 on timeout, got %d", commandResult.ExitCode)
	}
	if !strings.Contains(commandResult.Stdout, "before-stall") {
		t.Fatalf("expected partial stdout preserved on timeout, got %q", commandResult.Stdout)
	}
	if elapsed := time.Since(startedAt); elapsed > 10*time.Second {
		t.Fatalf("expected timeout result within enforcement bound, took %s", elapsed)
	}
}

func TestAwaitCommandCompletionAbandonsSilentCommand(t *testing.T) {
	expiredContext, cancelFunction := context.WithCancel(context.Background())
	cancelFunction()
	runResult := make(chan error)
	startedAt := time.Now()

	errorValue, abandoned := awaitCommandCompletion(expiredContext, runResult, 50*time.Millisecond)

	if !abandoned {
		t.Fatalf("expected abandoned completion, got error=%v", errorValue)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("expected abandonment within grace, took %s", elapsed)
	}
}

func TestAwaitCommandCompletionReturnsResultDeliveredDuringGrace(t *testing.T) {
	expiredContext, cancelFunction := context.WithCancel(context.Background())
	cancelFunction()
	runResult := make(chan error, 1)
	runResult <- nil

	errorValue, abandoned := awaitCommandCompletion(expiredContext, runResult, time.Second)

	if abandoned || errorValue != nil {
		t.Fatalf("expected delivered run result, got abandoned=%v error=%v", abandoned, errorValue)
	}
}

func testTerminalConfiguration(t *testing.T) config.TerminalConfiguration {
	t.Helper()
	workspaceRootPath := t.TempDir()
	return config.TerminalConfiguration{
		Mode:                  "firecrackerGuest",
		WorkspaceRootPath:     workspaceRootPath,
		AllowNetwork:          true,
		AllowInteractiveShell: true,
		TimeoutSecond:         3,
		OutputMaxBytes:        1024,
		SessionMaxCount:       4,
	}
}

func TestRunCommandReportsTheSignalThatEndedItSeparatelyFromTheExitCode(t *testing.T) {
	terminalSessionService := NewTerminalSessionService(testTerminalConfiguration(t))

	killedResult, errorValue := terminalSessionService.RunCommand(context.Background(), CommandRequest{
		Command: "kill -TERM $$",
	})

	if errorValue == nil {
		t.Fatalf("a command the kernel took away did not fail: %+v", killedResult)
	}
	if killedResult.Signal != "terminated" {
		t.Fatalf("the model has to be able to tell a process that was taken away from one that decided to fail, and an exit code alone cannot say which: %+v", killedResult)
	}
	if killedResult.TimedOut {
		t.Fatalf("nothing timed out here, and folding one fact into another is how a cut-short run reads as something else: %+v", killedResult)
	}

	ordinaryFailure, _ := terminalSessionService.RunCommand(context.Background(), CommandRequest{Command: "exit 3"})
	if ordinaryFailure.Signal != "" || ordinaryFailure.ExitCode != 3 {
		t.Fatalf("a command that chose its own exit code was signalled by nobody: %+v", ordinaryFailure)
	}
}

func TestATimedOutCommandSaysHowItWasEnded(t *testing.T) {
	terminalSessionService := NewTerminalSessionService(testTerminalConfiguration(t))

	commandResult, errorValue := terminalSessionService.RunCommand(context.Background(), CommandRequest{
		Command:       "sleep 5",
		TimeoutSecond: 1,
	})

	if errorValue == nil || !commandResult.TimedOut {
		t.Fatalf("expected the timeout to be reported: result=%+v error=%v", commandResult, errorValue)
	}
	if commandResult.Signal == "" {
		t.Fatalf("a run cut short by us reads as an ordinary failure unless the result says a signal ended it: %+v", commandResult)
	}
}
