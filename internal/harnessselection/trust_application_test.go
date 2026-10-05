package harnessselection

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type sessionMetaRecordingAgent struct {
	acp.Agent
	observedSessionMeta map[string]any
}

func (agent *sessionMetaRecordingAgent) Initialize(_ context.Context, request acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion:   request.ProtocolVersion,
		AgentCapabilities: acp.AgentCapabilities{McpCapabilities: acp.McpCapabilities{Http: true}},
	}, nil
}

func (agent *sessionMetaRecordingAgent) NewSession(_ context.Context, request acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	agent.observedSessionMeta = request.Meta
	return acp.NewSessionResponse{SessionId: "session-1"}, nil
}

func (agent *sessionMetaRecordingAgent) Prompt(context.Context, acp.PromptRequest) (acp.PromptResponse, error) {
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (agent *sessionMetaRecordingAgent) serve(output io.Writer, input io.Reader) {
	connection := acp.NewAgentSideConnection(agent, output, input)
	<-connection.Done()
}

type inProcessProcessStarter struct {
	agent *sessionMetaRecordingAgent
}

func (starter inProcessProcessStarter) StartProcess(context.Context, security.CommandRequest) (security.StreamingProcess, error) {
	clientToAgentReader, clientToAgentWriter := io.Pipe()
	agentToClientReader, agentToClientWriter := io.Pipe()
	agentDone := make(chan struct{})
	go func() {
		defer close(agentDone)
		starter.agent.serve(agentToClientWriter, clientToAgentReader)
	}()
	return security.StreamingProcess{
		Input:  clientToAgentWriter,
		Output: agentToClientReader,
		Wait: func() error {
			_ = clientToAgentWriter.Close()
			<-agentDone
			return nil
		},
	}, nil
}

type inProcessAgentActor struct {
	security.WorkspaceActor
	inProcessProcessStarter
}

type inProcessAgentRunner struct {
	agent *sessionMetaRecordingAgent
}

func (runner inProcessAgentRunner) Requester(context.Context, security.WorkspaceActorRequest) (security.WorkspaceActor, error) {
	return inProcessAgentActor{inProcessProcessStarter: inProcessProcessStarter{agent: runner.agent}}, nil
}

func TestTheExternalHarnessOpensItsSessionWithTheTrustLineItsDefinitionDeclares(t *testing.T) {
	agent := &sessionMetaRecordingAgent{}
	resolver := mcpserver.NewSessionTokenRequesterResolver(func() string { return "session-token" })
	selectedFactory, errorValue := Select(
		config.HarnessConfiguration{Name: ExternalHarnessName, AgentCommandPath: "/usr/bin/true"},
		nil,
		ToolCatalogEndpoint{URL: "http://127.0.0.1:1/catalog", Resolver: resolver},
		SandboxProcessBoundary{Runner: inProcessAgentRunner{agent: agent}, WorkspaceRootPath: "/workspace"},
	)
	if errorValue != nil {
		t.Fatalf("expected the configured external harness: %v", errorValue)
	}
	harness, _ := selectedFactory(harnessdriver.Dependencies{})

	_, errorValue = harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		Prompt:            "hello",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           singleToolSet(t),
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}

	declared, _ := json.Marshal(externalAgentToolCatalogTrust.SessionMeta)
	observed, _ := json.Marshal(agent.observedSessionMeta)
	if len(externalAgentToolCatalogTrust.SessionMeta) == 0 || string(observed) != string(declared) {
		t.Fatalf("expected the session to carry the declared trust %s, got %s", declared, observed)
	}
}
