//go:build linux || darwin

package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func requireUnprivilegedDaemonProcess(t *testing.T) string {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("blueclaw refuses terminal execution when its own process is root, so this proof runs unprivileged like production")
	}
	helperPath := strings.TrimSpace(os.Getenv("BLUECLAW_TEST_POSIX_HELPER_PATH"))
	if helperPath == "" {
		t.Skip("set BLUECLAW_TEST_POSIX_HELPER_PATH to a setuid-root blueclaw-posix-helper to prove kernel enforcement")
	}
	helperInformation, errorValue := os.Stat(helperPath)
	if errorValue != nil {
		t.Skipf("the configured posix helper is unreadable: %v", errorValue)
	}
	if helperInformation.Mode()&os.ModeSetuid == 0 {
		t.Skipf("%s is not setuid, so it cannot project a person onto a linux user", helperPath)
	}
	return helperPath
}

func writePolicyDocument(t *testing.T, personID string) string {
	t.Helper()
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	policyDocument := policy.PolicyDocument{
		People: []policy.PersonPolicy{{PersonID: personID, Emails: []string{personID + "@example.com"}}},
	}
	document, errorValue := json.MarshalIndent(policyDocument, "", "  ")
	if errorValue != nil {
		t.Fatalf("expected the policy document to encode: %v", errorValue)
	}
	if errorValue := os.WriteFile(policyPath, document, 0o644); errorValue != nil {
		t.Fatalf("expected the policy document to be written: %v", errorValue)
	}
	return policyPath
}

func TestTheKernelRunsACatalogToolAsTheRequesterUnprivilegedUser(t *testing.T) {
	posixHelperPath := requireUnprivilegedDaemonProcess(t)

	const requesterPersonID = "person-kernel-proof"
	removeProjectedIdentitiesAfter(t, []string{requesterPersonID}, nil)
	workspaceRootPath, errorValue := os.MkdirTemp("", "blueclaw-kernel-proof")
	if errorValue != nil {
		t.Fatalf("expected a workspace root: %v", errorValue)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(workspaceRootPath)
	})
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
			t.Fatalf("expected the workspace path to be traversable by the requester: %v", chmodError)
		}
	}
	terminalConfiguration := config.TerminalConfiguration{
		Mode:              "native",
		WorkspaceRootPath: workspaceRootPath,
		TimeoutSecond:     60,
		OutputMaxBytes:    65536,
		SessionMaxCount:   2,
		POSIXHelperPath:   posixHelperPath,
	}

	synchronizer := security.NewPOSIXSynchronizer(terminalConfiguration, writePolicyDocument(t, requesterPersonID))
	personAccess := policy.PersonAccess{PersonID: requesterPersonID, SecurityLevelRank: 100}
	provisioner := security.NewPOSIXRequesterWorkspaceProvisioner(synchronizer)
	if errorValue := provisioner.ProvisionRequesterWorkspace(context.Background(), personAccess, workspaceRootPath); errorValue != nil {
		t.Fatalf("expected the requester to be projected onto a linux user: %v", errorValue)
	}

	projectedUserName := security.LinuxPersonUserName(requesterPersonID)
	projectedUser, errorValue := user.Lookup(projectedUserName)
	if errorValue != nil {
		t.Fatalf("expected %q to exist as a real linux user: %v", projectedUserName, errorValue)
	}
	if projectedUser.Uid == "0" {
		t.Fatalf("expected the requester to be unprivileged, got uid %s", projectedUser.Uid)
	}

	terminalService := security.NewShellService(terminalConfiguration)
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspaceRootPath)
	toolCatalogBuilder.UseTerminalService(terminalService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{"default": {toolcontract.ShellToolName}}, nil)
	toolSet := toolCatalogBuilder.BuildToolSet(agentruntime.ToolCatalogRequest{
		RequesterPersonID: requesterPersonID,
		ProfileName:       "default",
		Prompt:            "누구로 실행되는지 알려줘",
		PersonAccess:      personAccess,
	})

	if _, errorValue := mcpserver.NewToolCatalogServer(mcpserver.RequesterToolSet{RequesterPersonID: requesterPersonID, ToolSet: toolSet}, "test"); errorValue != nil {
		t.Fatalf("expected the catalog to publish the requester's tools: %v", errorValue)
	}

	toolResult, errorValue := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.ShellToolName,
		Input:    json.RawMessage(`{"command":"id -un && id -u"}`),
	})
	if errorValue != nil {
		t.Fatalf("expected the tool to run: %v", errorValue)
	}
	if toolResult.Failed() {
		t.Fatalf("expected the tool to succeed, got %+v", toolResult.Failure)
	}
	commandOutput := toolResult.Output.Content
	if !strings.Contains(commandOutput, projectedUserName) {
		t.Fatalf("expected the kernel to run the command as %q, got %q", projectedUserName, commandOutput)
	}
	if strings.Contains(commandOutput, "\n0\n") || strings.HasSuffix(strings.TrimSpace(commandOutput), "\n0") {
		t.Fatalf("expected an unprivileged uid, got %q", commandOutput)
	}
	if !strings.Contains(commandOutput, projectedUser.Uid) {
		t.Fatalf("expected uid %s in the output, got %q", projectedUser.Uid, commandOutput)
	}
}

func TestTheRequesterPrivateDirectoryIsNotReadableBySomeoneElse(t *testing.T) {
	posixHelperPath := requireUnprivilegedDaemonProcess(t)
	_ = posixHelperPath

	firstUserName := security.LinuxPersonUserName("person-alpha")
	secondUserName := security.LinuxPersonUserName("person-beta")
	if firstUserName == secondUserName {
		t.Fatalf("expected two people to project to two linux users, both were %q", firstUserName)
	}
	if !strings.HasPrefix(firstUserName, "bc_person_") {
		t.Fatalf("expected a namespaced person user, got %q", firstUserName)
	}
}
