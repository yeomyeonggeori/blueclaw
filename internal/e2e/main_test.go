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
	foundSkills, missingSkills := ScenarioSkillAvailability()
	if len(missingSkills) > 0 && len(foundSkills) == 0 {
		fmt.Printf("skipping the appliance scenarios: no workspace skill bundle beside this checkout\n")
		os.Exit(0)
	}
	if len(missingSkills) > 0 {
		fmt.Printf("the appliance beside this checkout has no skill bundle for %s; it ships %s\n", strings.Join(missingSkills, ", "), strings.Join(foundSkills, ", "))
		os.Exit(1)
	}
	os.Exit(mainTesting.Run())
}
