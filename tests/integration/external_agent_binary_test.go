package integration

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/acpharness"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func buildExternalAgentBinary(t *testing.T) string {
	t.Helper()
	if _, errorValue := exec.LookPath("go"); errorValue != nil {
		t.Skip("go toolchain is unavailable, so the external agent binary cannot be built")
	}
	binaryPath := filepath.Join(t.TempDir(), "acptestagent")
	buildCommand := exec.Command("go", "build", "-o", binaryPath, "github.com/yeomyeonggeori/blueclaw/tests/acptestagent")
	buildCommand.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, errorValue := buildCommand.CombinedOutput(); errorValue != nil {
		t.Fatalf("expected the external agent binary to build: %v\n%s", errorValue, output)
	}
	return binaryPath
}

type countingRequesterResolver struct {
	mutex          sync.Mutex
	inner          *mcpserver.SessionTokenRequesterResolver
	resolvedTokens []string
}

func (resolver *countingRequesterResolver) ResolveRequester(bearerToken string) (mcpserver.RequesterToolSet, error) {
	resolver.mutex.Lock()
	resolver.resolvedTokens = append(resolver.resolvedTokens, bearerToken)
	resolver.mutex.Unlock()
	return resolver.inner.ResolveRequester(bearerToken)
}

func (resolver *countingRequesterResolver) resolutionCount() int {
	resolver.mutex.Lock()
	defer resolver.mutex.Unlock()
	return len(resolver.resolvedTokens)
}

type countingToolCatalogPublisher struct {
	endpointURL string
	inner       *mcpserver.SessionTokenRequesterResolver
	revokeCount int
}

func (publisher *countingToolCatalogPublisher) PublishToolCatalog(requesterToolSet mcpserver.RequesterToolSet) (string, string, func(), error) {
	sessionToken, errorValue := publisher.inner.GrantSessionToken(requesterToolSet)
	if errorValue != nil {
		return "", "", func() {}, errorValue
	}
	return publisher.endpointURL, sessionToken, func() {
		publisher.revokeCount++
		publisher.inner.RevokeSessionToken(sessionToken)
	}, nil
}

func externalAgentToolSet(t *testing.T) *toolcontract.ToolSet {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{"meeting_note_write"})
	toolSet.AllowTestReplacement()
	errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
		ID:             "test:meeting_note_write",
		Name:           "meeting_note_write",
		Description:    "write a meeting note",
		Visibility:     toolcontract.ToolVisibilityModel,
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
		ResultContract: &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
	}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolSuccessData("written", json.RawMessage(`{}`)), nil
	})
	if errorValue != nil {
		t.Fatalf("expected the tool to register: %v", errorValue)
	}
	return toolSet
}

func TestARealExternalAgentBinaryTakesTheRequesterToolCatalog(t *testing.T) {
	binaryPath := buildExternalAgentBinary(t)

	grantCount := 0
	innerResolver := mcpserver.NewSessionTokenRequesterResolver(func() string {
		grantCount++
		return "session-token-" + strconv.Itoa(grantCount)
	})
	countingResolver := &countingRequesterResolver{inner: innerResolver}
	catalogServer := httptest.NewServer(mcpserver.NewToolCatalogHandler(countingResolver, "test"))
	t.Cleanup(catalogServer.Close)
	publisher := &countingToolCatalogPublisher{endpointURL: catalogServer.URL, inner: innerResolver}

	harness := acpharness.New(acpharness.AgentCommand{
		Path:      binaryPath,
		Arguments: []string{"--tool-name", "meeting_note_write", "--tool-arguments-json", `{"text":"회의록"}`},
	}, publisher, nil)

	_, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           externalAgentToolSet(t),
	})
	if errorValue != nil {
		t.Fatalf("expected a real external agent process to accept the requester's tool catalog: %v", errorValue)
	}
	if countingResolver.resolutionCount() == 0 {
		t.Fatal("expected the spawned agent to authenticate to the tool catalog with the granted token")
	}
	if publisher.revokeCount != 1 {
		t.Fatalf("expected the catalog grant to be revoked when the turn ended, got %d", publisher.revokeCount)
	}
}

func TestARealExternalAgentBinaryCannotStartASessionWithoutAValidCatalogToken(t *testing.T) {
	binaryPath := buildExternalAgentBinary(t)

	innerResolver := mcpserver.NewSessionTokenRequesterResolver(func() string { return "session-token" })
	catalogServer := httptest.NewServer(mcpserver.NewToolCatalogHandler(innerResolver, "test"))
	t.Cleanup(catalogServer.Close)

	harness := acpharness.New(acpharness.AgentCommand{
		Path:      binaryPath,
		Arguments: []string{"--tool-name", "meeting_note_write", "--tool-arguments-json", `{"text":"회의록"}`},
	}, forgedTokenPublisher{endpointURL: catalogServer.URL}, nil)

	_, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           externalAgentToolSet(t),
	})
	if errorValue == nil {
		t.Fatal("expected a forged catalog token to stop the session, so the previous test's success means the token was honoured")
	}
	if !strings.Contains(strings.ToLower(errorValue.Error()), "catalog") {
		t.Fatalf("expected the failure to name the tool catalog, got %v", errorValue)
	}
}

type forgedTokenPublisher struct {
	endpointURL string
}

func (publisher forgedTokenPublisher) PublishToolCatalog(mcpserver.RequesterToolSet) (string, string, func(), error) {
	return publisher.endpointURL, "session-token-forged", func() {}, nil
}
