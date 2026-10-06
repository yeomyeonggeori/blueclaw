//go:build nobundledharness

package buildflavor

import (
	"os/exec"
	"path/filepath"
	"strings"
)

const IsBundledHarnessBuild = false

func GoFlags() []string {
	return []string{"-tags", "nobundledharness", "-modfile", filepath.Join(repositoryRoot(), "go.nobundled.mod")}
}

func repositoryRoot() string {
	output, errorValue := exec.Command("go", "env", "GOMOD").Output()
	if errorValue != nil {
		panic("go env GOMOD: " + errorValue.Error())
	}
	return filepath.Dir(strings.TrimSpace(string(output)))
}
