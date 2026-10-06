package e2e

import (
	"fmt"
	"os"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/bluecollaracp"
)

func runUnderBundledHarness(mainTesting *testing.M) int {
	if errorValue := UseBundledACPHarness(bluecollaracp.NewFactory); errorValue != nil {
		fmt.Fprintf(os.Stderr, "bundled harness: %v\n", errorValue)
		return 1
	}
	return mainTesting.Run()
}
