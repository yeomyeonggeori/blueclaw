//go:build !nobundledharness

package architecture

import (
	"os/exec"
	"strings"
	"testing"
)

func TestBlueclawAndBluecollarPinTheSameBlueprotocol(t *testing.T) {
	own := pinnedRevision(t, "../..")
	bundled := pinnedRevision(t, "../../.dependency/bluecollar")
	if own != bundled {
		t.Errorf("blueclaw pins blueprotocol at %s but its bluecollar pins %s; the contract types would differ between host and harness", own, bundled)
	}
}

func pinnedRevision(t *testing.T, repository string) string {
	t.Helper()
	output, errorValue := exec.Command("git", "-C", repository, "ls-files", "-s", ".dependency/blueprotocol").Output()
	if errorValue != nil {
		t.Fatalf("git ls-files in %s: %v", repository, errorValue)
	}
	fields := strings.Fields(string(output))
	if len(fields) < 2 || fields[0] != "160000" {
		t.Fatalf("%s pins no .dependency/blueprotocol submodule: %q", repository, output)
	}
	return fields[1]
}
