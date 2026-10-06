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

var scenariosThatParkTheRunMidTurn = map[string]bool{
	"ask_choice_reply_acceptance":             true,
	"ask_choice_reply_over_acp":               true,
	"ask_root_message_starts_a_task":          true,
	"ask_root_message_starts_a_task_over_acp": true,
	"calendar_event_lifecycle_acceptance":     true,
	"channel_post_acceptance":                 true,
	"dm_send_confirm_acceptance":              true,
	"host_update_now_acceptance":              true,
	"host_update_off_hours_acceptance":        true,
}

func skipWhereTheHarnessCannotEndTheTurnOnAParkedRun(t *testing.T, scenarioNames ...string) {
	t.Helper()
	if activeHarnessName != harnessselection.BundledACPHarnessName {
		return
	}
	for _, scenarioName := range scenarioNames {
		if scenariosThatParkTheRunMidTurn[scenarioName] {
			t.Skip("blueclaw parks the run in the middle of the agent's turn and the harness does not yet end the turn on it (#537 step 4)")
		}
	}
}
