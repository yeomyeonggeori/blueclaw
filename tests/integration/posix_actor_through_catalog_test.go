package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type recordingWorkspaceActorFactory struct {
	mutex            sync.Mutex
	requestedActors  []security.WorkspaceActorRequest
	commandsExecuted []security.CommandRequest
}

func (factory *recordingWorkspaceActorFactory) CanListDirectory(context.Context) bool {
	return true
}

func (factory *recordingWorkspaceActorFactory) Requester(_ context.Context, request security.WorkspaceActorRequest) (security.WorkspaceActor, error) {
	factory.mutex.Lock()
	factory.requestedActors = append(factory.requestedActors, request)
	factory.mutex.Unlock()
	return &recordingWorkspaceActor{factory: factory}, nil
}

func (factory *recordingWorkspaceActorFactory) requestedPersonIDs() []string {
	factory.mutex.Lock()
	defer factory.mutex.Unlock()
	personIDs := []string{}
	for _, request := range factory.requestedActors {
		personIDs = append(personIDs, request.PersonAccess.PersonID)
	}
	return personIDs
}

type recordingWorkspaceActor struct {
	factory *recordingWorkspaceActorFactory
}

func (actor *recordingWorkspaceActor) Run(_ context.Context, request security.CommandRequest) (security.CommandResult, error) {
	actor.factory.mutex.Lock()
	actor.factory.commandsExecuted = append(actor.factory.commandsExecuted, request)
	actor.factory.mutex.Unlock()
	return security.CommandResult{Stdout: "ran as the requester"}, nil
}

func (actor *recordingWorkspaceActor) MkdirAll(context.Context, string) error { return nil }
func (actor *recordingWorkspaceActor) WriteFile(context.Context, string, []byte) error {
	return nil
}
func (actor *recordingWorkspaceActor) ReadFile(context.Context, string, int64) ([]byte, error) {
	return nil, nil
}
func (actor *recordingWorkspaceActor) BundleDirectory(context.Context, string, security.WorkspaceActorBundleOptions) (security.WorkspaceActorBundle, error) {
	return security.WorkspaceActorBundle{}, nil
}
func (actor *recordingWorkspaceActor) ListDirectory(context.Context, string) ([]security.WorkspaceActorDirectoryEntry, error) {
	return nil, nil
}
func (actor *recordingWorkspaceActor) Stat(context.Context, string) (security.WorkspaceActorStat, error) {
	return security.WorkspaceActorStat{}, nil
}

func TestAToolCalledThroughTheCatalogReachesTheRequesterPOSIXActor(t *testing.T) {
	workspaceRootPath := t.TempDir()
	actorFactory := &recordingWorkspaceActorFactory{}
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspaceRootPath)
	toolCatalogBuilder.UseTerminalService(security.NewShellService(config.TerminalConfiguration{
		Mode:              "native",
		WorkspaceRootPath: workspaceRootPath,
		TimeoutSecond:     30,
		OutputMaxBytes:    32768,
		SessionMaxCount:   1,
	}))
	toolCatalogBuilder.UseWorkspaceActorFactory(actorFactory)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {toolcontract.ShellToolName},
	}, nil)

	toolSet := toolCatalogBuilder.BuildToolSet(agentruntime.ToolCatalogRequest{
		RequesterPersonID: "person-1",
		ProfileName:       "default",
		Prompt:            "워크스페이스 파일 목록 보여줘",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
	})

	resolver := mcpserver.NewSessionTokenRequesterResolver(func() string { return "session-token" })
	sessionToken, errorValue := resolver.GrantSessionToken(mcpserver.RequesterToolSet{RequesterPersonID: "person-1", ToolSet: toolSet})
	if errorValue != nil {
		t.Fatalf("expected a catalog grant: %v", errorValue)
	}
	catalogServer := httptest.NewServer(mcpserver.NewToolCatalogHandler(resolver, "test"))
	t.Cleanup(catalogServer.Close)

	clientSession, errorValue := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "test"}, nil).Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   catalogServer.URL,
		HTTPClient: &http.Client{Transport: catalogBearer{bearerToken: sessionToken}},
	}, nil)
	if errorValue != nil {
		t.Fatalf("expected the agent to reach the catalog: %v", errorValue)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	callResult, errorValue := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      toolcontract.ShellToolName,
		Arguments: map[string]any{"command": "ls"},
	})
	if errorValue != nil {
		t.Fatalf("expected the tool call to reach the daemon: %v", errorValue)
	}
	if callResult.IsError {
		t.Fatalf("expected the tool to run through the requester's actor, got %+v", callResult.StructuredContent)
	}

	requestedPersonIDs := actorFactory.requestedPersonIDs()
	if len(requestedPersonIDs) == 0 {
		t.Fatal("expected a tool called through the catalog to resolve a workspace actor, not to run as the daemon process")
	}
	for _, personID := range requestedPersonIDs {
		if personID != "person-1" {
			t.Fatalf("expected every actor to be the requester, got %q", personID)
		}
	}
	if len(actorFactory.commandsExecuted) == 0 {
		t.Fatal("expected the command to be executed through the requester's actor")
	}
}

func TestTheRequesterPOSIXIdentityIsDerivedFromThePersonNotTheProcess(t *testing.T) {
	firstIdentity := security.ExecutionIdentityForPersonAccess(policy.PersonAccess{PersonID: "person-1"}, t.TempDir())
	secondIdentity := security.ExecutionIdentityForPersonAccess(policy.PersonAccess{PersonID: "person-2"}, t.TempDir())

	if firstIdentity.UserName == "" || secondIdentity.UserName == "" {
		t.Fatalf("expected each person to project to a linux user, got %q and %q", firstIdentity.UserName, secondIdentity.UserName)
	}
	if firstIdentity.UserName == secondIdentity.UserName {
		t.Fatalf("expected two people to project to two users, both were %q", firstIdentity.UserName)
	}
}

type catalogBearer struct {
	bearerToken string
}

func (bearer catalogBearer) RoundTrip(request *http.Request) (*http.Response, error) {
	request.Header.Set("Authorization", "Bearer "+bearer.bearerToken)
	return http.DefaultTransport.RoundTrip(request)
}
