//go:build linux || darwin

package integration

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

// The product's central claim, on real Linux: two people sharing one Blueclaw
// get two Linux users, and neither can reach the other's private workspace.
// Needs root to create users, so it skips everywhere else.
func TestTwoPeopleGetSeparatePOSIXWorkspaces(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("run as root to exercise POSIX workspace separation")
	}
	helperPath := strings.TrimSpace(os.Getenv("BLUECLAW_TEST_POSIX_HELPER"))
	if helperPath == "" {
		t.Skip("set BLUECLAW_TEST_POSIX_HELPER to the installed blueclaw-posix-helper")
	}

	requireBlueclawServiceAccount(t)
	removeProjectedIdentitiesAfter(t, []string{"person-one", "person-two"}, []string{"member"})
	workspaceRootPath := traversableTempDir(t)
	policyPath := writeTwoPersonPolicy(t)
	terminalConfiguration := config.TerminalConfiguration{
		Mode:              "native",
		WorkspaceRootPath: workspaceRootPath,
		POSIXHelperPath:   helperPath,
		TimeoutSecond:     60,
		AllowNetwork:      true,
	}
	provisioner := security.NewPOSIXRequesterWorkspaceProvisioner(security.NewPOSIXSynchronizer(terminalConfiguration, policyPath))

	for _, personID := range []string{"person-one", "person-two"} {
		personAccess := policy.PersonAccess{PersonID: personID, Circles: []string{"member"}}
		if errorValue := provisioner.ProvisionRequesterWorkspace(context.Background(), personAccess, workspaceRootPath); errorValue != nil {
			t.Fatalf("expected %s to be provisioned: %v", personID, errorValue)
		}
	}

	firstUserName := security.LinuxPersonUserName("person-one")
	secondUserName := security.LinuxPersonUserName("person-two")
	firstHome := filepath.Join(workspaceRootPath, "private", "people", "person-one")
	secondHome := filepath.Join(workspaceRootPath, "private", "people", "person-two")

	if ownerOf(t, firstHome) != firstUserName {
		t.Fatalf("expected %s to own %s, got %s", firstUserName, firstHome, ownerOf(t, firstHome))
	}
	if ownerOf(t, secondHome) != secondUserName {
		t.Fatalf("expected %s to own %s, got %s", secondUserName, secondHome, ownerOf(t, secondHome))
	}
	if ownerOf(t, firstHome) == ownerOf(t, secondHome) {
		t.Fatal("expected two people to own two different private workspaces")
	}

	writeAs(t, firstUserName, filepath.Join(firstHome, "notes.txt"), "owned by one")
	if ownerOf(t, filepath.Join(firstHome, "notes.txt")) != firstUserName {
		t.Fatal("expected a file written by a person to be owned by that person")
	}
	if canReadAs(t, secondUserName, filepath.Join(firstHome, "notes.txt")) {
		t.Fatal("expected one person to be unable to read another person's private file")
	}
}

func traversableTempDir(t *testing.T) string {
	t.Helper()
	directoryPath := t.TempDir()
	for _, path := range ancestorsBelowTemporaryRoot(directoryPath, os.TempDir()) {
		if errorValue := os.Chmod(path, 0o755); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	return directoryPath
}

func ancestorsBelowTemporaryRoot(directoryPath string, temporaryRootPath string) []string {
	paths := []string{}
	for path := directoryPath; path != temporaryRootPath && strings.HasPrefix(path, temporaryRootPath); path = filepath.Dir(path) {
		paths = append(paths, path)
	}
	return paths
}

func writeTwoPersonPolicy(t *testing.T) string {
	t.Helper()
	document := `{"people":[
	  {"personID":"person-one","displayName":"One","emails":["one@example.com"],"securityLevelName":"member","securityLevelRank":50,"grantedClasses":["internal"],"circles":["member"]},
	  {"personID":"person-two","displayName":"Two","emails":["two@example.com"],"securityLevelName":"member","securityLevelRank":50,"grantedClasses":["internal"],"circles":["member"]}
	],"circles":[{"circleID":"member","displayName":"Member"}]}`
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	if errorValue := os.WriteFile(policyPath, []byte(document), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	return policyPath
}

func ownerOf(t *testing.T, path string) string {
	t.Helper()
	pathInformation, errorValue := os.Stat(path)
	if errorValue != nil {
		t.Fatalf("expected %s to exist: %v", path, errorValue)
	}
	ownership, isOwnershipKnown := pathInformation.Sys().(*syscall.Stat_t)
	if !isOwnershipKnown {
		t.Fatalf("expected %s to report POSIX ownership", path)
	}
	owner, errorValue := user.LookupId(strconv.FormatUint(uint64(ownership.Uid), 10))
	if errorValue != nil {
		t.Fatalf("expected uid %d to resolve to a user: %v", ownership.Uid, errorValue)
	}
	return owner.Username
}

func writeAs(t *testing.T, userName string, path string, content string) {
	t.Helper()
	if output, errorValue := shellAs(t, userName, "printf %s "+content+" > "+path).CombinedOutput(); errorValue != nil {
		t.Fatalf("expected %s to write %s: %v %s", userName, path, errorValue, output)
	}
}

func canReadAs(t *testing.T, userName string, path string) bool {
	t.Helper()
	return shellAs(t, userName, "cat "+path).Run() == nil
}

func shellAs(t *testing.T, userName string, shellCommand string) *exec.Cmd {
	t.Helper()
	resolvedUser, errorValue := user.Lookup(userName)
	if errorValue != nil {
		t.Fatalf("expected %s to exist: %v", userName, errorValue)
	}
	userID, errorValue := strconv.ParseUint(resolvedUser.Uid, 10, 32)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	groupID, errorValue := strconv.ParseUint(resolvedUser.Gid, 10, 32)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	command := exec.Command("/bin/sh", "-c", shellCommand)
	command.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uint32(userID), Gid: uint32(groupID)},
	}
	return command
}
