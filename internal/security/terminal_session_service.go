package security

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/config"

	"github.com/creack/pty"
)

const (
	commandAbandonGrace   = 5 * time.Second
	commandReaperInterval = 30 * time.Second
	sessionCloseGrace     = 2 * time.Second
	sessionKillGrace      = 3 * time.Second
)

var commandWaitHeartbeatInterval = 60 * time.Second

type CommandResult struct {
	ExitCode      int    `json:"exitCode"`
	Stdout        string `json:"stdout"`
	Stderr        string `json:"stderr"`
	TimedOut      bool   `json:"timedOut"`
	Signal        string `json:"signal,omitempty"`
	OutputTrimmed bool   `json:"outputTrimmed"`
}

type TerminalSessionStatus struct {
	SessionID     string `json:"sessionID"`
	Status        string `json:"status"`
	ExitCode      int    `json:"exitCode"`
	Stdout        string `json:"stdout,omitempty"`
	Stderr        string `json:"stderr,omitempty"`
	RecentOutput  string `json:"recentOutput,omitempty"`
	OutputTrimmed bool   `json:"outputTrimmed,omitempty"`
}

type TerminalSession struct {
	SessionID           string
	command             *exec.Cmd
	standardInputWriter interface {
		Write([]byte) (int, error)
		Close() error
	}
	cancelFunction       context.CancelFunc
	ptyFile              *os.File
	standardOutputBuffer *outputRingBuffer
	standardErrorBuffer  *outputRingBuffer
	mutex                sync.RWMutex
	isExited             bool
	exitCode             int
	exited               chan struct{}
	processGroupID       int
}

type TerminalSessionService struct {
	commandGuardrailService CommandGuardrailService
	mutex                   sync.RWMutex
	terminalSessions        map[string]*TerminalSession
}

func NewTerminalSessionService(terminalConfiguration config.TerminalConfiguration) *TerminalSessionService {
	return &TerminalSessionService{
		commandGuardrailService: NewCommandGuardrailService(terminalConfiguration),
		terminalSessions:        map[string]*TerminalSession{},
	}
}

func (terminalSessionService *TerminalSessionService) RunCommand(ctx context.Context, commandRequest CommandRequest) (CommandResult, error) {
	commandPlan, errorValue := terminalSessionService.commandGuardrailService.BuildCommandPlan(commandRequest)
	if errorValue != nil {
		return CommandResult{ExitCode: -1, Stderr: errorValue.Error()}, errorValue
	}
	if errorValue := terminalSessionService.prepareWorkingDirectory(commandPlan.WorkingDirectoryPath); errorValue != nil {
		return CommandResult{Stderr: errorValue.Error()}, errorValue
	}

	ctx, cancelFunction := context.WithTimeout(ctx, commandPlan.Timeout)
	defer cancelFunction()

	command := exec.CommandContext(ctx, commandPlan.ExecutablePath, commandPlan.Arguments...)
	configureCommandGroupKill(command)
	command.Dir = commandPlan.WorkingDirectoryPath
	scopeMarker := nextTerminalCommandScopeMarker()
	command.Env = append(mapEnvironmentVariables(commandPlan.EnvironmentVariables), scopeMarker)
	if strings.TrimSpace(commandPlan.Stdin) != "" {
		command.Stdin = strings.NewReader(commandPlan.Stdin)
	}

	outputMaximumBytes := terminalSessionService.commandOutputMaxBytes(commandPlan)
	standardOutputBuffer := newOutputRingBuffer(outputMaximumBytes)
	standardErrorBuffer := newOutputRingBuffer(outputMaximumBytes)
	command.Stdout = standardOutputBuffer
	command.Stderr = standardErrorBuffer

	runResult := make(chan error, 1)
	go func() {
		runResult <- command.Run()
	}()

	errorValue, isAbandoned := awaitCommandCompletion(ctx, runResult, command.WaitDelay+commandAbandonGrace)
	if isAbandoned {
		terminalSessionService.abandonUnreapableProcessGroup(command, runResult, scopeMarker)
		return CommandResult{
			ExitCode:      -1,
			Stdout:        truncateString(standardOutputBuffer.String(), outputMaximumBytes),
			Stderr:        truncateString(standardErrorBuffer.String(), outputMaximumBytes),
			TimedOut:      true,
			OutputTrimmed: terminalOutputWasTrimmed(standardOutputBuffer, standardErrorBuffer),
		}, errors.New("command timed out")
	}
	exitCode := 0
	if command.ProcessState != nil {
		exitCode = command.ProcessState.ExitCode()
	}
	killingSignal := killingSignalName(command.ProcessState)

	if ctx.Err() == context.DeadlineExceeded {
		sweepEscapedCommandProcesses(scopeMarker)
		return CommandResult{
			ExitCode:      exitCode,
			Stdout:        truncateString(standardOutputBuffer.String(), outputMaximumBytes),
			Stderr:        truncateString(standardErrorBuffer.String(), outputMaximumBytes),
			TimedOut:      true,
			Signal:        killingSignal,
			OutputTrimmed: terminalOutputWasTrimmed(standardOutputBuffer, standardErrorBuffer),
		}, errors.New("command timed out")
	}

	if errorValue != nil {
		standardError := standardErrorBuffer.String()
		if strings.TrimSpace(standardError) == "" {
			standardError = errorValue.Error()
		}
		return CommandResult{
			ExitCode:      exitCode,
			Stdout:        truncateString(standardOutputBuffer.String(), outputMaximumBytes),
			Stderr:        truncateString(standardError, outputMaximumBytes),
			Signal:        killingSignal,
			OutputTrimmed: terminalOutputWasTrimmed(standardOutputBuffer, standardErrorBuffer),
		}, errorValue
	}

	return CommandResult{
		ExitCode:      exitCode,
		Stdout:        truncateString(standardOutputBuffer.String(), outputMaximumBytes),
		Stderr:        truncateString(standardErrorBuffer.String(), outputMaximumBytes),
		Signal:        killingSignal,
		OutputTrimmed: terminalOutputWasTrimmed(standardOutputBuffer, standardErrorBuffer),
	}, nil
}

// The kernel taking a process away and the process deciding to fail are different
// answers, and an exit code alone cannot tell them apart: a shell reports both as a
// number, and a command that traps its signal exits 0 after being cut short.
func killingSignalName(processState *os.ProcessState) string {
	if processState == nil {
		return ""
	}
	waitStatus, isWaitStatus := processState.Sys().(syscall.WaitStatus)
	if !isWaitStatus || !waitStatus.Signaled() {
		return ""
	}
	return waitStatus.Signal().String()
}

func terminalOutputWasTrimmed(standardOutputBuffer *outputRingBuffer, standardErrorBuffer *outputRingBuffer) bool {
	return standardOutputBuffer.IsTrimmed() || standardErrorBuffer.IsTrimmed()
}

func configureCommandGroupKill(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = 2 * time.Second
}

func awaitCommandCompletion(ctx context.Context, runResult <-chan error, abandonGrace time.Duration) (errorValue error, abandoned bool) {
	heartbeatTicker := time.NewTicker(commandWaitHeartbeatInterval)
	defer heartbeatTicker.Stop()
	waitStartedAt := time.Now()
	for {
		select {
		case errorValue = <-runResult:
			return errorValue, false
		case <-heartbeatTicker.C:
			slog.Info("terminal_run command still running", "elapsedSeconds", int(time.Since(waitStartedAt).Seconds()))
		case <-ctx.Done():
			select {
			case errorValue = <-runResult:
				return errorValue, false
			case <-time.After(abandonGrace):
				return nil, true
			}
		}
	}
}

func (terminalSessionService *TerminalSessionService) abandonUnreapableProcessGroup(command *exec.Cmd, runResult <-chan error, scopeMarker string) {
	if command.Process == nil {
		return
	}
	processGroupID := command.Process.Pid
	slog.Warn("terminal_run abandoned unreapable process group", "pgid", processGroupID, "command", command.Path)
	sweepEscapedCommandProcesses(scopeMarker)

	go func() {
		ticker := time.NewTicker(commandReaperInterval)
		defer ticker.Stop()
		for {
			select {
			case <-runResult:
				slog.Info("abandoned process group reaped", "pgid", processGroupID)
				sweepEscapedCommandProcesses(scopeMarker)
				return
			case <-ticker.C:
				_ = syscall.Kill(-processGroupID, syscall.SIGKILL)
				sweepEscapedCommandProcesses(scopeMarker)
			}
		}
	}()
}

var terminalCommandScopeCounter atomic.Uint64

func nextTerminalCommandScopeMarker() string {
	return fmt.Sprintf("BLUECLAW_TERMINAL_SCOPE=%d-%d", os.Getpid(), terminalCommandScopeCounter.Add(1))
}

func sweepEscapedCommandProcesses(scopeMarker string) {
	processIDs := processIDsWithEnvironmentMarker("/proc", scopeMarker)
	killedCount := 0
	for _, processID := range processIDs {
		if syscall.Kill(-processID, syscall.SIGKILL) == nil || syscall.Kill(processID, syscall.SIGKILL) == nil {
			killedCount++
		}
	}
	if killedCount > 0 {
		slog.Warn("terminal_run killed escaped command processes", "count", killedCount, "scope", scopeMarker)
	}
}

func processIDsWithEnvironmentMarker(procRootPath string, scopeMarker string) []int {
	entries, errorValue := os.ReadDir(procRootPath)
	if errorValue != nil {
		return nil
	}
	markerRecord := append([]byte(scopeMarker), 0)
	ownProcessID := os.Getpid()
	var processIDs []int
	for _, entry := range entries {
		processID, errorValue := strconv.Atoi(entry.Name())
		if errorValue != nil || processID == ownProcessID {
			continue
		}
		environ, errorValue := os.ReadFile(filepath.Join(procRootPath, entry.Name(), "environ"))
		if errorValue != nil {
			continue
		}
		if bytes.Contains(environ, markerRecord) {
			processIDs = append(processIDs, processID)
		}
	}
	return processIDs
}

func (terminalSessionService *TerminalSessionService) prepareWorkingDirectory(workingDirectoryPath string) error {
	if terminalSessionService.commandGuardrailService.terminalConfiguration.Mode != "firecrackerGuest" {
		return nil
	}
	fileInformation, errorValue := os.Stat(workingDirectoryPath)
	if errorValue != nil {
		return errorValue
	}
	if !fileInformation.IsDir() {
		return errors.New("working directory is not a directory")
	}
	return nil
}

func (terminalSessionService *TerminalSessionService) StartInteractiveSession(commandRequest CommandRequest) (string, error) {
	commandRequest.IsInteractive = true
	commandPlan, errorValue := terminalSessionService.commandGuardrailService.BuildCommandPlan(commandRequest)
	if errorValue != nil {
		return "", errorValue
	}
	if terminalSessionService.sessionCount() >= terminalSessionService.sessionMaxCount() {
		return "", errors.New("terminal session limit reached")
	}
	if errorValue := terminalSessionService.prepareWorkingDirectory(commandPlan.WorkingDirectoryPath); errorValue != nil {
		return "", errorValue
	}

	ctx, cancelFunction := context.WithTimeout(context.Background(), maxDuration(commandPlan.Timeout, 30*time.Minute))
	command := exec.CommandContext(ctx, commandPlan.ExecutablePath, commandPlan.Arguments...)
	command.Dir = commandPlan.WorkingDirectoryPath
	command.Env = mapEnvironmentVariables(commandPlan.EnvironmentVariables)

	sessionID := newIdentifier()
	terminalSession := &TerminalSession{
		SessionID:            sessionID,
		command:              command,
		cancelFunction:       cancelFunction,
		standardOutputBuffer: newOutputRingBuffer(terminalSessionService.outputMaxBytes()),
		standardErrorBuffer:  newOutputRingBuffer(terminalSessionService.outputMaxBytes()),
		exitCode:             -1,
		exited:               make(chan struct{}),
	}
	if commandPlan.IsPTY {
		errorValue = terminalSession.startPTY(commandPlan)
	} else {
		errorValue = terminalSession.startPipe(commandPlan)
	}
	if errorValue != nil {
		cancelFunction()
		return "", errorValue
	}
	terminalSession.rememberProcessGroup()
	go terminalSession.wait()

	terminalSessionService.mutex.Lock()
	terminalSessionService.terminalSessions[sessionID] = terminalSession
	terminalSessionService.mutex.Unlock()

	if strings.TrimSpace(commandPlan.Stdin) != "" {
		_, _ = terminalSession.standardInputWriter.Write([]byte(commandPlan.Stdin + "\n"))
	}
	return sessionID, nil
}

func (terminalSessionService *TerminalSessionService) WriteSessionInput(sessionID string, input string) (TerminalSessionStatus, error) {
	terminalSession, isFound := terminalSessionService.findSession(sessionID)
	if !isFound {
		return TerminalSessionStatus{}, errors.New("terminal session not found")
	}

	_, errorValue := terminalSession.standardInputWriter.Write([]byte(input))
	if errorValue != nil {
		return TerminalSessionStatus{}, errorValue
	}

	time.Sleep(50 * time.Millisecond)
	return terminalSession.status(), nil
}

func (terminalSessionService *TerminalSessionService) StatusSession(sessionID string) (TerminalSessionStatus, error) {
	terminalSession, isFound := terminalSessionService.findSession(sessionID)
	if !isFound {
		return TerminalSessionStatus{}, errors.New("terminal session not found")
	}
	return terminalSession.status(), nil
}

func (terminalSessionService *TerminalSessionService) CloseSession(sessionID string) error {
	terminalSession, isFound := terminalSessionService.takeSession(sessionID)
	if !isFound {
		return errors.New("terminal session not found")
	}
	return terminalSession.closeAndAwaitExit()
}

// Every live session, so a shutdown does not leave a shell behind holding the
// requester's workspace open.
func (terminalSessionService *TerminalSessionService) CloseAllSessions() error {
	var firstError error
	for _, sessionID := range terminalSessionService.sessionIdentifiers() {
		if errorValue := terminalSessionService.CloseSession(sessionID); errorValue != nil && firstError == nil {
			firstError = errorValue
		}
	}
	return firstError
}

func (terminalSessionService *TerminalSessionService) sessionIdentifiers() []string {
	terminalSessionService.mutex.RLock()
	defer terminalSessionService.mutex.RUnlock()
	sessionIdentifiers := make([]string, 0, len(terminalSessionService.terminalSessions))
	for sessionID := range terminalSessionService.terminalSessions {
		sessionIdentifiers = append(sessionIdentifiers, sessionID)
	}
	return sessionIdentifiers
}

// Taken out of the registry before anything is killed, so a status read never finds a
// session in the middle of dying and the service lock is not held across the wait.
func (terminalSessionService *TerminalSessionService) findSession(sessionID string) (*TerminalSession, bool) {
	terminalSessionService.mutex.RLock()
	defer terminalSessionService.mutex.RUnlock()
	terminalSession, isFound := terminalSessionService.terminalSessions[sessionID]
	return terminalSession, isFound
}

func (terminalSessionService *TerminalSessionService) takeSession(sessionID string) (*TerminalSession, bool) {
	terminalSessionService.mutex.Lock()
	defer terminalSessionService.mutex.Unlock()
	terminalSession, isFound := terminalSessionService.terminalSessions[sessionID]
	if !isFound {
		return nil, false
	}
	delete(terminalSessionService.terminalSessions, sessionID)
	return terminalSession, true
}

func (terminalSession *TerminalSession) closeAndAwaitExit() error {
	_ = terminalSession.standardInputWriter.Close()
	if terminalSession.ptyFile != nil {
		_ = terminalSession.ptyFile.Close()
	}
	if terminalSession.cancelFunction != nil {
		terminalSession.cancelFunction()
	}
	hasExited := terminalSession.awaitExit(sessionCloseGrace)
	terminalSession.killWhatItStarted()
	if hasExited || terminalSession.awaitExit(sessionKillGrace) {
		return nil
	}
	slog.Warn("terminal session did not exit after being killed", "sessionID", terminalSession.SessionID)
	return errors.New("terminal session " + terminalSession.SessionID + " did not exit after it was killed")
}

// Read while the shell is alive, because a group cannot be found from a process that
// has already gone and the children it left behind are exactly what has to be found.
func (terminalSession *TerminalSession) rememberProcessGroup() {
	if terminalSession.command.Process == nil {
		return
	}
	processGroupID, errorValue := syscall.Getpgid(terminalSession.command.Process.Pid)
	if errorValue != nil || processGroupID == syscall.Getpgrp() {
		return
	}
	terminalSession.processGroupID = processGroupID
}

// Killing the shell leaves whatever the shell started, and a shell that ended on its
// own leaves it too, so the group is signalled either way. A group with nothing left
// in it is a no-op.
func (terminalSession *TerminalSession) killWhatItStarted() {
	if terminalSession.processGroupID != 0 {
		_ = syscall.Kill(-terminalSession.processGroupID, syscall.SIGKILL)
		return
	}
	if terminalSession.command.Process != nil {
		_ = terminalSession.command.Process.Kill()
	}
}

func (terminalSession *TerminalSession) awaitExit(grace time.Duration) bool {
	select {
	case <-terminalSession.exited:
		return true
	case <-time.After(grace):
		return false
	}
}

func (terminalSession *TerminalSession) startPipe(commandPlan CommandPlan) error {
	terminalSession.command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	standardInputWriter, errorValue := terminalSession.command.StdinPipe()
	if errorValue != nil {
		return errorValue
	}
	standardOutputPipe, errorValue := terminalSession.command.StdoutPipe()
	if errorValue != nil {
		return errorValue
	}
	standardErrorPipe, errorValue := terminalSession.command.StderrPipe()
	if errorValue != nil {
		return errorValue
	}
	terminalSession.standardInputWriter = standardInputWriter
	if errorValue = terminalSession.command.Start(); errorValue != nil {
		return errorValue
	}
	go copyBuffer(terminalSession.standardOutputBuffer, standardOutputPipe)
	go copyBuffer(terminalSession.standardErrorBuffer, standardErrorPipe)
	return nil
}

func (terminalSession *TerminalSession) startPTY(commandPlan CommandPlan) error {
	ptyFile, errorValue := pty.Start(terminalSession.command)
	if errorValue != nil {
		return errorValue
	}
	terminalSession.ptyFile = ptyFile
	terminalSession.standardInputWriter = ptyFile
	go copyBuffer(terminalSession.standardOutputBuffer, ptyFile)
	_ = commandPlan
	return nil
}

func (terminalSession *TerminalSession) wait() {
	errorValue := terminalSession.command.Wait()
	exitCode := 0
	if terminalSession.command.ProcessState != nil {
		exitCode = terminalSession.command.ProcessState.ExitCode()
	} else if errorValue != nil {
		exitCode = -1
	}
	terminalSession.mutex.Lock()
	terminalSession.exitCode = exitCode
	terminalSession.isExited = true
	terminalSession.mutex.Unlock()
	close(terminalSession.exited)
}

func (terminalSession *TerminalSession) status() TerminalSessionStatus {
	terminalSession.mutex.RLock()
	isExited := terminalSession.isExited
	exitCode := terminalSession.exitCode
	terminalSession.mutex.RUnlock()
	status := "running"
	if isExited {
		status = "exited"
	}
	stdout := terminalSession.standardOutputBuffer.String()
	stderr := terminalSession.standardErrorBuffer.String()
	return TerminalSessionStatus{
		SessionID:     terminalSession.SessionID,
		Status:        status,
		ExitCode:      exitCode,
		Stdout:        stdout,
		Stderr:        stderr,
		RecentOutput:  stdout + stderr,
		OutputTrimmed: terminalSession.standardOutputBuffer.IsTrimmed() || terminalSession.standardErrorBuffer.IsTrimmed(),
	}
}

func (terminalSessionService *TerminalSessionService) sessionCount() int {
	terminalSessionService.mutex.RLock()
	defer terminalSessionService.mutex.RUnlock()
	return len(terminalSessionService.terminalSessions)
}

func (terminalSessionService *TerminalSessionService) sessionMaxCount() int {
	if terminalSessionService.commandGuardrailService.terminalConfiguration.SessionMaxCount <= 0 {
		return 4
	}
	return terminalSessionService.commandGuardrailService.terminalConfiguration.SessionMaxCount
}

func (terminalSessionService *TerminalSessionService) outputMaxBytes() int {
	if terminalSessionService.commandGuardrailService.terminalConfiguration.OutputMaxBytes <= 0 {
		return 32768
	}
	return terminalSessionService.commandGuardrailService.terminalConfiguration.OutputMaxBytes
}

func (terminalSessionService *TerminalSessionService) commandOutputMaxBytes(commandPlan CommandPlan) int {
	if commandPlan.OutputMaximumBytes > 0 {
		return commandPlan.OutputMaximumBytes
	}
	return terminalSessionService.outputMaxBytes()
}

func mapEnvironmentVariables(environmentVariables map[string]string) []string {
	mappedEnvironmentVariables := []string{}
	for name, value := range environmentVariables {
		mappedEnvironmentVariables = append(mappedEnvironmentVariables, name+"="+value)
	}
	return mappedEnvironmentVariables
}

func copyBuffer(buffer interface{ Write([]byte) (int, error) }, reader interface{ Read([]byte) (int, error) }) {
	_, _ = io.Copy(buffer, reader)
}

type outputRingBuffer struct {
	mutex     sync.RWMutex
	maxBytes  int
	content   []byte
	isTrimmed bool
}

func newOutputRingBuffer(maxBytes int) *outputRingBuffer {
	if maxBytes <= 0 {
		maxBytes = 32768
	}
	return &outputRingBuffer{maxBytes: maxBytes}
}

func (outputRingBuffer *outputRingBuffer) Write(value []byte) (int, error) {
	outputRingBuffer.mutex.Lock()
	defer outputRingBuffer.mutex.Unlock()
	outputRingBuffer.content = append(outputRingBuffer.content, value...)
	if len(outputRingBuffer.content) > outputRingBuffer.maxBytes {
		outputRingBuffer.content = outputRingBuffer.content[len(outputRingBuffer.content)-outputRingBuffer.maxBytes:]
		outputRingBuffer.isTrimmed = true
	}
	return len(value), nil
}

func (outputRingBuffer *outputRingBuffer) String() string {
	outputRingBuffer.mutex.RLock()
	defer outputRingBuffer.mutex.RUnlock()
	return string(outputRingBuffer.content)
}

func (outputRingBuffer *outputRingBuffer) IsTrimmed() bool {
	outputRingBuffer.mutex.RLock()
	defer outputRingBuffer.mutex.RUnlock()
	return outputRingBuffer.isTrimmed
}

func truncateString(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	return value[:maxBytes]
}

func maxDuration(left time.Duration, right time.Duration) time.Duration {
	if left > right {
		return left
	}
	return right
}

func newIdentifier() string {
	return time.Now().UTC().Format("20060102150405.000000000")
}
