package e2e

import (
	"fmt"
	"os"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/bluecollaracp"
	"github.com/yeomyeonggeori/blueclaw/internal/bluecollarharness"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessselection"
)

type harnessUnderTest struct {
	name string
	use  func() error
}

var activeHarnessName = harnessselection.BundledHarnessName

func harnessMatrix() []harnessUnderTest {
	return []harnessUnderTest{
		{name: harnessselection.BundledHarnessName, use: func() error { UseAgentHarnessFactory(bluecollarharness.New); return nil }},
		{name: harnessselection.BundledACPHarnessName, use: func() error { return UseBundledACPHarness(bluecollaracp.NewFactory) }},
	}
}

func runUnderEveryHarness(mainTesting *testing.M) int {
	exitCode := 0
	for _, harness := range harnessMatrix() {
		if errorValue := harness.use(); errorValue != nil {
			fmt.Fprintf(os.Stderr, "harness %s: %v\n", harness.name, errorValue)
			return 1
		}
		activeHarnessName = harness.name
		fmt.Printf("=== harness %s\n", harness.name)
		if runCode := mainTesting.Run(); runCode != 0 {
			exitCode = runCode
		}
	}
	return exitCode
}
