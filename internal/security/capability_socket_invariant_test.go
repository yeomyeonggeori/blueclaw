package security

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestCapabilitySocket(t *testing.T, mode os.FileMode) string {
	t.Helper()
	shortDirectory, temporaryDirectoryError := os.MkdirTemp("", "bcsock")
	if temporaryDirectoryError != nil {
		t.Fatalf("create short temp directory: %v", temporaryDirectoryError)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortDirectory) })
	socketPath := filepath.Join(shortDirectory, "capability.sock")
	listener, listenError := net.Listen("unix", socketPath)
	if listenError != nil {
		t.Fatalf("listen unix socket: %v", listenError)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if chmodError := os.Chmod(socketPath, mode); chmodError != nil {
		t.Fatalf("chmod socket: %v", chmodError)
	}
	return socketPath
}

func TestVerifyCapabilitySocketInvariantPassesWhenGroupIsNotARequesterGroupAndModeExcludesOther(t *testing.T) {
	socketPath := newTestCapabilitySocket(t, 0o660)

	resolveGroup := func(path string) (string, os.FileMode, error) {
		if path != socketPath {
			t.Fatalf("unexpected socket path: %s", path)
		}
		return "blueclaw", 0o660, nil
	}
	resolveRequesterGroups := func() ([]string, error) {
		return []string{"bc_shared", "bc_circle_member", "bc_person_abcd1234"}, nil
	}

	result, verifyError := VerifyCapabilitySocketInvariant(socketPath, resolveGroup, resolveRequesterGroups)
	if verifyError != nil {
		t.Fatalf("expected no error, got %v", verifyError)
	}
	if result.Skipped {
		t.Fatal("expected check to run, got skipped")
	}
	if result.GroupName != "blueclaw" {
		t.Fatalf("unexpected group name: %s", result.GroupName)
	}
}

func TestVerifyCapabilitySocketInvariantFailsWhenModeGrantsOtherAccess(t *testing.T) {
	socketPath := newTestCapabilitySocket(t, 0o666)

	resolveGroup := func(path string) (string, os.FileMode, error) {
		return "blueclaw", 0o666, nil
	}
	resolveRequesterGroups := func() ([]string, error) {
		return []string{"bc_shared"}, nil
	}

	_, verifyError := VerifyCapabilitySocketInvariant(socketPath, resolveGroup, resolveRequesterGroups)
	if verifyError == nil {
		t.Fatal("expected error for other-accessible mode")
	}
	if !strings.Contains(verifyError.Error(), "0666") {
		t.Fatalf("expected error to mention the mode, got: %v", verifyError)
	}
}

func TestVerifyCapabilitySocketInvariantFailsWhenRequesterGroupOwnsSocket(t *testing.T) {
	socketPath := newTestCapabilitySocket(t, 0o660)

	resolveGroup := func(path string) (string, os.FileMode, error) {
		return "bc_shared", 0o660, nil
	}
	resolveRequesterGroups := func() ([]string, error) {
		return []string{"bc_shared", "bc_circle_member"}, nil
	}

	_, verifyError := VerifyCapabilitySocketInvariant(socketPath, resolveGroup, resolveRequesterGroups)
	if verifyError == nil {
		t.Fatal("expected error when a requester group owns the socket")
	}
	if !strings.Contains(verifyError.Error(), "bc_shared") {
		t.Fatalf("expected error to mention the colliding group, got: %v", verifyError)
	}
}

func TestVerifyCapabilitySocketInvariantSkipsWhenSocketMissing(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "does-not-exist.sock")

	resolveGroup := func(path string) (string, os.FileMode, error) {
		return "", 0, os.ErrNotExist
	}
	resolveRequesterGroups := func() ([]string, error) {
		t.Fatal("requester group resolver should not be called when the socket is missing")
		return nil, nil
	}

	result, verifyError := VerifyCapabilitySocketInvariant(socketPath, resolveGroup, resolveRequesterGroups)
	if verifyError != nil {
		t.Fatalf("expected no error for missing socket, got %v", verifyError)
	}
	if !result.Skipped {
		t.Fatal("expected result to be skipped for missing socket")
	}
	if result.SkipReason == "" {
		t.Fatal("expected a skip reason for missing socket")
	}
}

func TestVerifyCapabilitySocketInvariantSkipsWhenNoSocketPathConfigured(t *testing.T) {
	resolveGroup := func(path string) (string, os.FileMode, error) {
		t.Fatal("group resolver should not be called when no socket path is configured")
		return "", 0, nil
	}
	resolveRequesterGroups := func() ([]string, error) {
		t.Fatal("requester group resolver should not be called when no socket path is configured")
		return nil, nil
	}

	result, verifyError := VerifyCapabilitySocketInvariant("", resolveGroup, resolveRequesterGroups)
	if verifyError != nil {
		t.Fatalf("expected no error when no socket path is configured, got %v", verifyError)
	}
	if !result.Skipped {
		t.Fatal("expected result to be skipped when no socket path is configured")
	}
}
