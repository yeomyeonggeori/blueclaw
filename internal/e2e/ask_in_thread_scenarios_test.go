package e2e

import (
	"context"
	"testing"
)

var scenariosThatAskForApproval = []string{
	"calendar_event_lifecycle_acceptance",
	"dm_send_confirm_acceptance",
	"channel_post_acceptance",
	"host_update_now_acceptance",
	"host_update_off_hours_acceptance",
	"ask_choice_hold_acceptance",
}

func TestApprovalAndChoiceScenariosEndAsExpectedWhenTheQuestionIsAskedInTheThread(t *testing.T) {
	for _, scenarioName := range scenariosThatAskForApproval {
		t.Run(scenarioName, func(t *testing.T) {
			scenario, errorValue := BuiltinScenario(scenarioName, t.TempDir())
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if _, errorValue := RunVirtualSession(context.Background(), scenario); errorValue != nil {
				t.Fatalf("%s: %v", scenarioName, errorValue)
			}
		})
	}
}
