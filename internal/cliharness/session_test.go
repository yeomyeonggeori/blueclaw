package cliharness

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type recordingSessionPublisher struct {
	mutex    sync.Mutex
	sessions []mcpserver.HarnessSession
	endpoint string
	resolver *mcpserver.SessionTokenRequesterResolver
}

func (publisher *recordingSessionPublisher) PublishToolCatalog(requesterToolSet mcpserver.RequesterToolSet) (string, string, func(), error) {
	publisher.mutex.Lock()
	publisher.sessions = append(publisher.sessions, requesterToolSet.HarnessSession)
	publisher.mutex.Unlock()
	sessionToken, errorValue := publisher.resolver.GrantSessionToken(requesterToolSet)
	if errorValue != nil {
		return "", "", func() {}, errorValue
	}
	return publisher.endpoint, sessionToken, func() { publisher.resolver.RevokeSessionToken(sessionToken) }, nil
}

func (publisher *recordingSessionPublisher) published() []mcpserver.HarnessSession {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()
	return append([]mcpserver.HarnessSession{}, publisher.sessions...)
}

func sessionTestHarness(t *testing.T, agentCommand AgentCommand) (*Harness, *recordingSessionPublisher) {
	t.Helper()
	resolver := mcpserver.NewSessionTokenRequesterResolver(func() string { return "session-token" })
	catalogServer := httptest.NewServer(mcpserver.NewToolCatalogHandler(resolver, "test"))
	t.Cleanup(catalogServer.Close)
	publisher := &recordingSessionPublisher{endpoint: catalogServer.URL, resolver: resolver}
	return New(agentCommand, publisher, nil), publisher
}

func emptyToolSet(t *testing.T) *toolcontract.ToolSet {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{"noop"})
	toolSet.AllowTestReplacement()
	if errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
		ID: "test:noop", Name: "noop", Description: "noop",
		Visibility:     toolcontract.ToolVisibilityModel,
		InputSchema:    json.RawMessage(`{"type":"object","properties":{}}`),
		ResultContract: &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object"}`)},
	}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolSuccessData("ok", json.RawMessage(`{}`)), nil
	}); errorValue != nil {
		t.Fatalf("expected the tool to register: %v", errorValue)
	}
	return toolSet
}

func runEchoingTurn(t *testing.T, harness *Harness, request agentcontract.AgentTurnRequest) string {
	t.Helper()
	request.ToolSet = emptyToolSet(t)
	if request.WorkspaceRootPath == "" {
		request.WorkspaceRootPath = t.TempDir()
	}
	turnResult, errorValue := harness.RunTurn(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}
	return turnResult.FinishMessage
}

func TestOneTaskRunKeepsOneHarnessConversation(t *testing.T) {
	harness, publisher := sessionTestHarness(t, AgentCommand{
		Path:             "/bin/echo",
		HarnessName:      "test-harness",
		SessionArguments: func(sessionID string, isResuming bool) []string { return []string{sessionID} },
	})

	request := agentcontract.AgentTurnRequest{RequesterPersonID: "person-1", ExistingTaskRunID: "task-run-1", Prompt: "첫 턴"}
	firstOutput := runEchoingTurn(t, harness, request)
	secondOutput := runEchoingTurn(t, harness, request)

	if firstOutput != secondOutput {
		t.Fatalf("expected the same task run to reuse one conversation, got %q then %q", firstOutput, secondOutput)
	}
	published := publisher.published()
	if len(published) != 2 || published[0].SessionID != published[1].SessionID {
		t.Fatalf("expected one session identity across the task run, got %+v", published)
	}
	if !published[0].IsResumable || published[0].HarnessName != "test-harness" {
		t.Fatalf("expected a resumable session naming its harness, got %+v", published[0])
	}
}

func TestADifferentTaskRunGetsADifferentConversation(t *testing.T) {
	harness, publisher := sessionTestHarness(t, AgentCommand{
		Path:             "/bin/echo",
		HarnessName:      "test-harness",
		SessionArguments: func(sessionID string, isResuming bool) []string { return []string{sessionID} },
	})

	runEchoingTurn(t, harness, agentcontract.AgentTurnRequest{RequesterPersonID: "person-1", ExistingTaskRunID: "task-run-1", Prompt: "a"})
	runEchoingTurn(t, harness, agentcontract.AgentTurnRequest{RequesterPersonID: "person-1", ExistingTaskRunID: "task-run-2", Prompt: "b"})

	published := publisher.published()
	if published[0].SessionID == published[1].SessionID {
		t.Fatalf("expected two task runs to be two conversations, both were %q", published[0].SessionID)
	}
}

func TestResumingAnApprovedCallReopensTheSameConversation(t *testing.T) {
	harness, _ := sessionTestHarness(t, AgentCommand{
		Path:        "/bin/echo",
		HarnessName: "test-harness",
		SessionArguments: func(sessionID string, isResuming bool) []string {
			if isResuming {
				return []string{"resumed", sessionID}
			}
			return []string{"fresh", sessionID}
		},
	})

	request := agentcontract.AgentTurnRequest{RequesterPersonID: "person-1", ExistingTaskRunID: "task-run-1", Prompt: "첫 턴"}
	firstOutput := runEchoingTurn(t, harness, request)
	request.IsRuntimeRestartResume = true
	resumedOutput := runEchoingTurn(t, harness, request)

	if !strings.Contains(firstOutput, "fresh") || !strings.Contains(resumedOutput, "resumed") {
		t.Fatalf("expected an approval continuation to resume rather than start fresh, got %q then %q", firstOutput, resumedOutput)
	}
	firstSessionID := strings.Fields(firstOutput)
	resumedSessionID := strings.Fields(resumedOutput)
	if len(firstSessionID) < 2 || len(resumedSessionID) < 2 || firstSessionID[1] != resumedSessionID[1] {
		t.Fatalf("expected the resumed conversation to be the held one, got %q then %q", firstOutput, resumedOutput)
	}
}

func TestAHarnessThatCannotResumeSaysSoRatherThanPretending(t *testing.T) {
	harness, publisher := sessionTestHarness(t, AgentCommand{Path: "/bin/echo", HarnessName: "no-resume"})

	runEchoingTurn(t, harness, agentcontract.AgentTurnRequest{RequesterPersonID: "person-1", ExistingTaskRunID: "task-run-1", Prompt: "a"})

	published := publisher.published()
	if len(published) != 1 || published[0].IsResumable {
		t.Fatalf("expected a harness with no session verbs to report itself unresumable, got %+v", published)
	}
	if published[0].HarnessName != "no-resume" {
		t.Fatalf("expected the harness to still be named, got %+v", published[0])
	}
	_ = time.Now
}
