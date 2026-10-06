//go:build !nobundledharness

package e2e

import (
	"fmt"
	"os"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/defaultharness"
)

func runUnderBundledHarness(mainTesting *testing.M) int {
	if errorValue := UseBundledACPHarness(defaultharness.NewFactory); errorValue != nil {
		fmt.Fprintf(os.Stderr, "bundled harness: %v\n", errorValue)
		return 1
	}
	return mainTesting.Run()
}
