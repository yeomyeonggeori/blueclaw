package main

import (
	"os/exec"
	"strings"
	"testing"
)

const bluecollarModulePrefix = "github.com/yeomyeonggeori/bluecollar/"

func TestTheNoBundledHarnessBuildLinksNothingFromBluecollar(testInstance *testing.T) {
	for _, packagePath := range listedDependencies(testInstance, "-tags", "nobundledharness", "../...") {
		if strings.HasPrefix(packagePath, bluecollarModulePrefix) {
			testInstance.Errorf("a -tags nobundledharness build of ./cmd/... still depends on %s; something imports bluecollar outside internal/defaultharness", packagePath)
		}
	}
}

func listedDependencies(testInstance *testing.T, arguments ...string) []string {
	testInstance.Helper()
	command := append([]string{"list", "-deps"}, arguments...)
	output, errorValue := exec.Command("go", command...).Output()
	if errorValue != nil {
		testInstance.Fatalf("go %s: %v", strings.Join(command, " "), errorValue)
	}
	return strings.Fields(string(output))
}
