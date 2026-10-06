//go:build !nobundledharness

package main

import "testing"

const bundledHarnessPackage = bluecollarModulePrefix + "loop"

func TestTheDefaultBuildStillShipsTheBundledHarness(testInstance *testing.T) {
	for _, packagePath := range listedDependencies(testInstance, ".") {
		if packagePath == bundledHarnessPackage {
			return
		}
	}
	testInstance.Fatalf("the default build no longer links %s, so the bundled harness would be unreachable", bundledHarnessPackage)
}
