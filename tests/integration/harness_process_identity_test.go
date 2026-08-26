//go:build linux || darwin

package integration

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/cliharness"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type identityCatalogPublisher struct {
	endpointURL string
	resolver    *mcpserver.SessionTokenRequesterResolver
}

func (publisher identityCatalogPublisher) PublishToolCatalog(requesterToolSet mcpserver.RequesterToolSet) (string, string, func(), error) {
	sessionToken, errorValue := publisher.resolver.GrantSessionToken(requesterToolSet)
	if errorValue != nil {
		return "", "", func() {}, errorValue
	}
	return publisher.endpointURL, sessionToken, func() { publisher.resolver.RevokeSessionToken(sessionToken) }, nil
}

func TestTheHarnessProcessItselfRunsAsTheRequester(t *testing.T) {
	posixHelperPath := requireUnprivilegedDaemonProcess(t)

	const requesterPersonID = "person-harness-identity"
	removeProjectedIdentitiesAfter(t, []string{requesterPersonID}, nil)
	workspaceRootPath, errorValue := os.MkdirTemp("", "blueclaw-harness-identity")
	if errorValue != nil {
		t.Fatalf("expected a workspace root: %v", errorValue)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspaceRootPath) })
	for traversedPath := workspaceRootPath; traversedPath != "/" && traversedPath != "."; traversedPath = filepath.Dir(traversedPath) {
		pathInformation, statError := os.Stat(traversedPath)
		if statError != nil {
			break
		}
		ownership, isOwnershipKnown := pathInformation.Sys().(*syscall.Stat_t)
		if !isOwnershipKnown || int(ownership.Uid) != os.Geteuid() {
			break
		}
		if chmodError := os.Chmod(traversedPath, 0o755); chmodError != nil {
			t.Fatalf("expected the workspace path to be traversable: %v", chmodError)
		}
	}

	terminalConfiguration := config.TerminalConfiguration{
		Mode:              "native",
		WorkspaceRootPath: workspaceRootPath,
		TimeoutSecond:     60,
		OutputMaxBytes:    65536,
		SessionMaxCount:   2,
		AllowNetwork:      true,
		POSIXHelperPath:   posixHelperPath,
	}
	personAccess := policy.PersonAccess{PersonID: requesterPersonID, SecurityLevelRank: 100}
	synchronizer := security.NewPOSIXSynchronizer(terminalConfiguration, writePolicyDocument(t, requesterPersonID))
	if errorValue := security.NewPOSIXRequesterWorkspaceProvisioner(synchronizer).ProvisionRequesterWorkspace(context.Background(), personAccess, workspaceRootPath); errorValue != nil {
		t.Fatalf("expected the requester to be projected onto a linux user: %v", errorValue)
	}
	projectedUserName := security.LinuxPersonUserName(requesterPersonID)
	projectedUser, errorValue := user.Lookup(projectedUserName)
	if errorValue != nil {
		t.Fatalf("expected %q to exist: %v", projectedUserName, errorValue)
	}

	resolver := mcpserver.NewSessionTokenRequesterResolver(func() string { return "session-token-identity" })
	catalogServer := httptest.NewServer(mcpserver.NewToolCatalogHandler(resolver, "test"))
	t.Cleanup(catalogServer.Close)

	terminalService := security.NewShellService(terminalConfiguration)
	harness := cliharness.New(cliharness.AgentCommand{
		Path:            "/bin/sh",
		PromptArguments: []string{"-c", "id -u"},
	}, identityCatalogPublisher{endpointURL: catalogServer.URL, resolver: resolver}, nil)
	harness.UseRequesterProcessRunner(terminalService.WorkspaceActorFactory(), workspaceRootPath)

	turnResult, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: requesterPersonID,
		Prompt:            "unused",
		WorkspaceRootPath: workspaceRootPath,
		ToolSet:           harnessIdentityToolSet(t),
	})
	if errorValue != nil {
		t.Fatalf("expected the harness process to run: %v", errorValue)
	}

	reportedUID := strings.TrimSpace(turnResult.FinishMessage)
	if reportedUID != projectedUser.Uid {
		t.Fatalf("expected the harness process itself to run as %s (uid %s), it reported uid %q", projectedUserName, projectedUser.Uid, reportedUID)
	}
	if reportedUID == "0" || reportedUID == strconv.Itoa(os.Geteuid()) {
		t.Fatalf("expected the harness process to leave the daemon process identity behind, got uid %q", reportedUID)
	}
}

func harnessIdentityToolSet(t *testing.T) *toolcontract.ToolSet {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{"noop"})
	toolSet.AllowTestReplacement()
	errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
		ID:             "test:noop",
		Name:           "noop",
		Description:    "does nothing",
		Visibility:     toolcontract.ToolVisibilityModel,
		InputSchema:    json.RawMessage(`{"type":"object","properties":{}}`),
		ResultContract: &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object"}`)},
	}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolSuccessData("ok", json.RawMessage(`{}`)), nil
	})
	if errorValue != nil {
		t.Fatalf("expected the tool to register: %v", errorValue)
	}
	return toolSet
}
