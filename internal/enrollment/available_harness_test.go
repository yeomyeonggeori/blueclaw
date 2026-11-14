package enrollment

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/harnessselection"
)

func TestSetupOffersEveryCommandHarnessTheDaemonCanRun(t *testing.T) {
	binDirectory := t.TempDir()
	for _, commandHarness := range harnessselection.CommandHarnesses() {
		if errorValue := os.WriteFile(filepath.Join(binDirectory, commandHarness.ExecutableName), []byte("#!/bin/sh\n"), 0o755); errorValue != nil {
			t.Fatalf("expected a stand-in executable: %v", errorValue)
		}
	}
	t.Setenv("PATH", binDirectory)

	offered := map[string]string{}
	for _, harnessChoice := range AvailableHarnesses() {
		offered[harnessChoice.Name] = harnessChoice.AgentCommandPath
	}

	if _, isOffered := offered[harnessselection.BundledACPHarnessName]; !isOffered {
		t.Fatalf("the built-in harness needs nothing installed, got %v", offered)
	}
	for _, commandHarness := range harnessselection.CommandHarnesses() {
		if offered[commandHarness.Name] != filepath.Join(binDirectory, commandHarness.ExecutableName) {
			t.Fatalf("setup offers what the daemon can select, and %q is installed, got %v", commandHarness.Name, offered)
		}
	}
}
