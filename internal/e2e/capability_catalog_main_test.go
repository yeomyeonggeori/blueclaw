//go:build !appliance

package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// These scenarios drive the tools of whatever product binds them, offered
// through the capability catalog variable. A standalone checkout is offered
// none and skips. Being offered a catalog carrying some tools and not the rest
// means the product moved one, and skipping there would turn the gate off while
// still reporting ok.
func TestMain(mainTesting *testing.M) {
	foundTools, missingTools := ScenarioCapabilityAvailability()
	if len(missingTools) > 0 && len(foundTools) == 0 {
		fmt.Printf("skipping the virtual session tests: %s names no capability tool catalog\n", ScenarioCapabilityCatalogVariable)
		os.Exit(0)
	}
	if len(missingTools) > 0 {
		fmt.Printf("the catalog in %s carries no descriptor for %s; it carries %s\n", ScenarioCapabilityCatalogVariable, strings.Join(missingTools, ", "), strings.Join(foundTools, ", "))
		os.Exit(1)
	}
	os.Exit(mainTesting.Run())
}
