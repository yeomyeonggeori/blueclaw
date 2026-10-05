package acpharness

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/toolcallprogress"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type publishedToolCatalog struct {
	handlerServer    *httptest.Server
	resolver         *mcpserver.SessionTokenRequesterResolver
	revokeCount      int
	publishedToolSet mcpserver.RequesterToolSet
}

func newPublishedToolCatalog(t *testing.T) *publishedToolCatalog {
	t.Helper()
	grantCount := 0
	resolver := mcpserver.NewSessionTokenRequesterResolver(func() string {
		grantCount++
		return "session-token-" + strconv.Itoa(grantCount)
	})
	handlerServer := httptest.NewServer(mcpserver.NewToolCatalogHandler(resolver, "test"))
	t.Cleanup(handlerServer.Close)
	return &publishedToolCatalog{handlerServer: handlerServer, resolver: resolver}
}

func (catalog *publishedToolCatalog) PublishToolCatalog(requesterToolSet mcpserver.RequesterToolSet) (string, string, func(), error) {
	catalog.publishedToolSet = requesterToolSet
	sessionToken, errorValue := catalog.resolver.GrantSessionToken(requesterToolSet)
	if errorValue != nil {
		return "", "", func() {}, errorValue
	}
	return catalog.handlerServer.URL, sessionToken, func() {
		catalog.revokeCount++
		catalog.resolver.RevokeSessionToken(sessionToken)
	}, nil
}

type daemonExecutedTool struct {
	toolName          string
	requesterPersonID string
}

func requesterToolSet(t *testing.T, requesterPersonID string, executed *[]daemonExecutedTool) *toolcontract.ToolSet {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{"note_write"})
	toolSet.AllowTestReplacement()
	errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
		ID:              "test:note_write",
		Name:            "note_write",
		Description:     "write a note",
		Visibility:      toolcontract.ToolVisibilityModel,
		InputSchema:     json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
		SideEffectClass: toolcontract.ToolSideEffectStateChange,
		ResultContract:  &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
	}, func(ctx context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		*executed = append(*executed, daemonExecutedTool{toolName: invocation.ToolName, requesterPersonID: requesterPersonID})
		return toolcontract.ToolSuccessData("note written", json.RawMessage(`{}`)), nil
	})
	if errorValue != nil {
		t.Fatalf("expected the tool to register: %v", errorValue)
	}
	return toolSet
}

type inProcessAgentProcess struct {
	agent *externalAgent
}

func (process *inProcessAgentProcess) Start(ctx context.Context) (io.Writer, io.Reader, func() error, error) {
	clientToAgentReader, clientToAgentWriter := io.Pipe()
	agentToClientReader, agentToClientWriter := io.Pipe()
	agentDone := make(chan struct{})
	go func() {
		defer close(agentDone)
		process.agent.serve(ctx, agentToClientWriter, clientToAgentReader)
	}()
	return clientToAgentWriter, agentToClientReader, func() error {
		_ = clientToAgentWriter.Close()
		<-agentDone
		return nil
	}, nil
}

type externalAgent struct {
	acceptsNoHTTPToolCatalog bool
	toolNameToCall           string
	toolArguments            map[string]any
	finalMessage             string
	observedCatalog          []string
	toolCallError            error
	observedToolCatalog      acp.McpServer
	observedPrompt           string
	observedPromptMeta       map[string]any
	toolCallUpdates          []acp.SessionUpdate
	permissionRequest        *acp.RequestPermissionRequest
	observedSessionMeta      map[string]any
	permissionOutcome        acp.RequestPermissionOutcome
	connection               *acp.AgentSideConnection
}

func (agent *externalAgent) serve(ctx context.Context, output io.Writer, input io.Reader) {
	agent.connection = acp.NewAgentSideConnection(agent, output, input)
	<-agent.connection.Done()
	_ = ctx
}

func (agent *externalAgent) Initialize(_ context.Context, request acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion:   request.ProtocolVersion,
		AgentCapabilities: acp.AgentCapabilities{McpCapabilities: acp.McpCapabilities{Http: !agent.acceptsNoHTTPToolCatalog}},
	}, nil
}

func (agent *externalAgent) NewSession(ctx context.Context, request acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	if len(request.McpServers) != 1 {
		return acp.NewSessionResponse{}, io.ErrUnexpectedEOF
	}
	agent.observedToolCatalog = request.McpServers[0]
	agent.observedSessionMeta = request.Meta
	if request.McpServers[0].Http == nil {
		return acp.NewSessionResponse{SessionId: "session-1"}, nil
	}
	toolCatalog := request.McpServers[0].Http
	bearerToken := ""
	for _, header := range toolCatalog.Headers {
		if strings.EqualFold(header.Name, "Authorization") {
			bearerToken = strings.TrimPrefix(header.Value, "Bearer ")
		}
	}
	clientSession, errorValue := mcp.NewClient(&mcp.Implementation{Name: "external-agent", Version: "test"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   toolCatalog.Url,
		HTTPClient: &http.Client{Transport: bearerHeader{bearerToken: bearerToken}},
	}, nil)
	if errorValue != nil {
		agent.toolCallError = errorValue
		return acp.NewSessionResponse{SessionId: "session-1"}, nil
	}
	defer clientSession.Close()
	toolList, errorValue := clientSession.ListTools(ctx, nil)
	if errorValue != nil {
		agent.toolCallError = errorValue
		return acp.NewSessionResponse{SessionId: "session-1"}, nil
	}
	for _, tool := range toolList.Tools {
		agent.observedCatalog = append(agent.observedCatalog, tool.Name)
	}
	if _, errorValue := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: agent.toolNameToCall, Arguments: agent.toolArguments}); errorValue != nil {
		agent.toolCallError = errorValue
	}
	return acp.NewSessionResponse{SessionId: "session-1"}, nil
}

func (agent *externalAgent) Prompt(ctx context.Context, request acp.PromptRequest) (acp.PromptResponse, error) {
	agent.observedPromptMeta = request.Meta
	for _, update := range agent.toolCallUpdates {
		if errorValue := agent.connection.SessionUpdate(ctx, acp.SessionNotification{SessionId: request.SessionId, Update: update}); errorValue != nil {
			return acp.PromptResponse{}, errorValue
		}
	}
	if agent.permissionRequest != nil {
		permissionRequest := *agent.permissionRequest
		permissionRequest.SessionId = request.SessionId
		response, errorValue := agent.connection.RequestPermission(ctx, permissionRequest)
		if errorValue != nil {
			return acp.PromptResponse{}, errorValue
		}
		agent.permissionOutcome = response.Outcome
	}
	for _, contentBlock := range request.Prompt {
		if contentBlock.Text != nil {
			agent.observedPrompt += contentBlock.Text.Text
		}
	}
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (agent *externalAgent) Cancel(context.Context, acp.CancelNotification) error { return nil }
func (agent *externalAgent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}
func (agent *externalAgent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}
func (agent *externalAgent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}
func (agent *externalAgent) SetSessionConfigOption(context.Context, acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, nil
}
func (agent *externalAgent) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, nil
}
func (agent *externalAgent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, nil
}
func (agent *externalAgent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, nil
}

type bearerHeader struct {
	bearerToken string
}

func (header bearerHeader) RoundTrip(request *http.Request) (*http.Response, error) {
	request.Header.Set("Authorization", "Bearer "+header.bearerToken)
	return http.DefaultTransport.RoundTrip(request)
}

func TestHarnessRunsAnOutOfProcessAgentWhoseToolCallsExecuteInsideTheDaemon(t *testing.T) {
	executed := []daemonExecutedTool{}
	toolCatalog := newPublishedToolCatalog(t)
	agent := &externalAgent{toolNameToCall: "note_write", toolArguments: map[string]any{"text": "회의록"}}
	harness := New(&inProcessAgentProcess{agent: agent}, toolCatalog, nil)

	turnResult, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	})
	if errorValue != nil {
		t.Fatalf("expected the external agent turn to run: %v", errorValue)
	}
	if agent.toolCallError != nil {
		t.Fatalf("expected the agent to reach the tool catalog: %v", agent.toolCallError)
	}
	if len(agent.observedCatalog) != 1 || agent.observedCatalog[0] != "note_write" {
		t.Fatalf("expected the agent to see the requester's catalog, got %+v", agent.observedCatalog)
	}
	if len(executed) != 1 || executed[0].toolName != "note_write" || executed[0].requesterPersonID != "person-1" {
		t.Fatalf("expected the daemon to execute the tool as the requester, got %+v", executed)
	}
	if turnResult.TaskRun.Status != "completed" {
		t.Fatalf("expected a completed turn, got %+v", turnResult.TaskRun)
	}
	if toolCatalog.revokeCount != 1 {
		t.Fatalf("expected the tool catalog session to be revoked after the turn, got %d", toolCatalog.revokeCount)
	}
}

func TestAnExternalAgentsToolCallsAreForwardedToTheTurnsToolCallObserver(t *testing.T) {
	executed := []daemonExecutedTool{}
	started := acp.StartToolCall("call-1", "Read notes.md", acp.WithStartStatus(acp.ToolCallStatusInProgress))
	finished := acp.UpdateToolCall("call-1", acp.WithUpdateStatus(acp.ToolCallStatusCompleted))
	agent := &externalAgent{toolCallUpdates: []acp.SessionUpdate{started, finished}}
	harness := New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), nil)
	observed := []acp.SessionUpdate{}
	ctx := toolcallprogress.WithObserver(context.Background(), func(update acp.SessionUpdate) { observed = append(observed, update) })

	_, errorValue := harness.RunTurn(ctx, agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	})

	if errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}
	if len(observed) != 2 || observed[0].ToolCall == nil || observed[0].ToolCall.Title != "Read notes.md" || observed[1].ToolCallUpdate == nil || *observed[1].ToolCallUpdate.Status != acp.ToolCallStatusCompleted {
		t.Fatalf("the observer was told %+v, expected the agent's tool_call then tool_call_update", observed)
	}
}

func TestAnAgentThatBringsItsOwnShellIsNotHandedOurs(t *testing.T) {
	executed := []daemonExecutedTool{}
	toolCatalog := newPublishedToolCatalog(t)
	harness := New(&inProcessAgentProcess{agent: &externalAgent{}}, toolCatalog, nil)

	harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	})

	if toolCatalog.publishedToolSet.ToolAudience != mcpserver.ToolAudienceSelfEquipped {
		t.Fatalf("an ACP agent runs inside the requester's identity because it brings tools of its own, so the catalog must not offer the same job twice, got audience %q", toolCatalog.publishedToolSet.ToolAudience)
	}
}

func TestHarnessRefusesATurnWithNoRequester(t *testing.T) {
	executed := []daemonExecutedTool{}
	harness := New(&inProcessAgentProcess{agent: &externalAgent{}}, newPublishedToolCatalog(t), nil)

	if _, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		Prompt:  "회의록 정리해줘",
		ToolSet: requesterToolSet(t, "person-1", &executed),
	}); errorValue == nil {
		t.Fatal("expected a turn with no requester to be refused, because tools execute as the requester")
	}
}

func TestAnAgentThatTakesTheCatalogOverStdioIsGivenItThatWay(t *testing.T) {
	executed := []daemonExecutedTool{}
	agent := &externalAgent{acceptsNoHTTPToolCatalog: true}
	harness := New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), nil)
	harness.UseToolCatalogBridge("/usr/local/bin/blueclaw")

	if _, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	}); errorValue != nil {
		t.Fatalf("stdio is the transport every agent must support, so a turn using it has to run: %v", errorValue)
	}

	stdioCatalog := agent.observedToolCatalog.Stdio
	if stdioCatalog == nil {
		t.Fatalf("expected the catalog to be offered over stdio, got %+v", agent.observedToolCatalog)
	}
	if stdioCatalog.Command != "/usr/local/bin/blueclaw" {
		t.Fatalf("expected the bridge command, got %q", stdioCatalog.Command)
	}
	environmentNames := []string{}
	for _, environmentVariable := range stdioCatalog.Env {
		environmentNames = append(environmentNames, environmentVariable.Name)
	}
	if len(environmentNames) != 2 {
		t.Fatalf("the bridge needs the endpoint and the session token to reach the catalog, got %+v", environmentNames)
	}
}

func TestAnAgentThatCannotReachTheToolCatalogIsRefusedRatherThanRunWithoutIt(t *testing.T) {
	executed := []daemonExecutedTool{}
	harness := New(&inProcessAgentProcess{agent: &externalAgent{acceptsNoHTTPToolCatalog: true}}, newPublishedToolCatalog(t), nil)

	_, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	})
	if errorValue == nil {
		t.Fatal("expected an agent that cannot accept the tool catalog to be refused, because a turn answered from no tools looks like a successful turn")
	}
}

func TestAnAgentIsToldWhoItIsBeforeItIsGivenTheRequest(t *testing.T) {
	executed := []daemonExecutedTool{}
	agent := &externalAgent{}
	harness := New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), nil)
	harness.UseInstructionBundleLoader(func() agentcontract.InstructionBundle {
		return agentcontract.InstructionBundle{Prompt: "Answer from evidence you have gathered."}
	})

	harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		RequesterName:     "Ada",
		AgentIdentity:     agentcontract.AgentIdentity{Name: "인턴킴", Handle: "@internkim"},
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	})

	for _, expectedFragment := range []string{"Answer from evidence you have gathered.", "인턴킴", "Ada", "회의록 정리해줘"} {
		if !strings.Contains(agent.observedPrompt, expectedFragment) {
			t.Fatalf("an external agent that is told none of this answers as nobody, expected %q in:\n%s", expectedFragment, agent.observedPrompt)
		}
	}
}

func TestAnExternalAgentIsHandedTheCallTheHostCarriedOutForIt(t *testing.T) {
	executed := []daemonExecutedTool{}
	agent := &externalAgent{}
	harness := New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), nil)

	harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
		CarriedOutCalls: []agentcontract.CarriedOutCall{{
			ToolName:  "note_write",
			ToolInput: json.RawMessage(`{"text":"회의록"}`),
			Result:    toolcontract.ToolSuccessData("note written", json.RawMessage(`{}`)),
		}},
	})

	handback, isPresent := agent.observedPromptMeta[agentcontract.CarriedOutCallMetaKey]
	if !isPresent {
		t.Fatalf("an agent that is not handed this reissues the approved call, got meta %+v", agent.observedPromptMeta)
	}
	encoded, errorValue := json.Marshal(handback)
	if errorValue != nil || !strings.Contains(string(encoded), "note_write") {
		t.Fatalf("expected the carried out call on the wire, got %s (%v)", encoded, errorValue)
	}
	if !strings.Contains(agent.observedPrompt, "note_write") {
		t.Fatalf("expected the prompt to name the call the host already made, got:\n%s", agent.observedPrompt)
	}
}

func TestATurnWithNothingCarriedOutSendsNoHandback(t *testing.T) {
	executed := []daemonExecutedTool{}
	agent := &externalAgent{}
	harness := New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), nil)

	harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	})

	if _, isPresent := agent.observedPromptMeta[agentcontract.CarriedOutCallMetaKey]; isPresent {
		t.Fatalf("expected an ordinary turn to carry no handback, got %+v", agent.observedPromptMeta)
	}
}
