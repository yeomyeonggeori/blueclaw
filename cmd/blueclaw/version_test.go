package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const revisionSymbol = "github.com/yeomyeonggeori/blueclaw/internal/buildrevision.injected"

func buildBlueclaw(t *testing.T, arguments ...string) string {
	t.Helper()
	binaryPath := filepath.Join(t.TempDir(), "blueclaw")
	command := exec.Command("go", append(append([]string{"build", "-o", binaryPath}, arguments...), ".")...)
	if output, errorValue := command.CombinedOutput(); errorValue != nil {
		t.Fatalf("building the binary failed: %v\n%s", errorValue, output)
	}
	return binaryPath
}

func TestTheBuiltBinaryReportsTheRevisionItWasBuiltFrom(t *testing.T) {
	binaryPath := buildBlueclaw(t, "-ldflags", "-X "+revisionSymbol+"=abc123def456")

	output, errorValue := exec.Command(binaryPath, "--version").CombinedOutput()

	if errorValue != nil {
		t.Fatalf("running the built binary failed: %v\n%s", errorValue, output)
	}
	if strings.TrimSpace(string(output)) != "abc123def456" {
		t.Fatalf("a deploy that cannot ask a binary which revision it is has to trust a green exit code, and this repository has been wrong twice that way: %q", output)
	}
}

func TestABinaryWithNoRevisionSaysSoInsteadOfGuessing(t *testing.T) {
	binaryPath := buildBlueclaw(t)

	output, errorValue := exec.Command(binaryPath, "--version").CombinedOutput()

	if errorValue != nil {
		t.Fatalf("running the built binary failed: %v\n%s", errorValue, output)
	}
	reported := strings.TrimSpace(string(output))
	if reported != "unknown" && len(reported) < 7 {
		t.Fatalf("an unstamped build reports unknown, or whatever the toolchain recorded, and never a plausible-looking wrong answer: %q", reported)
	}
}
