//go:build appliance

package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// The appliance scenarios run the bundled scripts of skills this repository does
// not ship, so they need that appliance's workspace beside the checkout. A
// standalone checkout finds no bundle at all and skips. Finding some but not the
// rest means the appliance moved one, and skipping there would turn the whole
// gate off while still reporting ok.
func TestMain(mainTesting *testing.M) {
	foundTools, missingTools := ScenarioCapabilityAvailability()
	if len(missingTools) > 0 && len(foundTools) == 0 {
		fmt.Printf("skipping the appliance scenarios: %s names no capability tool catalog\n", ScenarioCapabilityCatalogVariable)
		os.Exit(0)
	}
	if len(missingTools) > 0 {
		fmt.Printf("the catalog in %s carries no descriptor for %s\n", ScenarioCapabilityCatalogVariable, strings.Join(missingTools, ", "))
		os.Exit(1)
	}
	foundSkills, missingSkills := ScenarioSkillAvailability()
	if len(missingSkills) > 0 && len(foundSkills) == 0 {
		fmt.Printf("skipping the appliance scenarios: %s names no skill root\n", ScenarioSkillRootsVariable)
		os.Exit(0)
	}
	if len(missingSkills) > 0 {
		fmt.Printf("the skill roots in %s carry no bundle for %s; they carry %s\n", ScenarioSkillRootsVariable, strings.Join(missingSkills, ", "), strings.Join(foundSkills, ", "))
		os.Exit(1)
	}
	os.Exit(mainTesting.Run())
}
