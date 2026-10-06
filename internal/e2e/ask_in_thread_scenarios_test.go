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
}

func TestApprovalAndChoiceScenariosEndTheSameWhenTheQuestionIsAskedInTheThread(t *testing.T) {
	for _, scenarioName := range scenariosThatAskForApproval {
		for _, isAskedInThread := range []bool{false, true} {
			label := scenarioName + "/parked"
			if isAskedInThread {
				label = scenarioName + "/asked_in_thread"
			}
			t.Run(label, func(t *testing.T) {
				scenario, errorValue := BuiltinScenario(scenarioName, t.TempDir())
				if errorValue != nil {
					t.Fatal(errorValue)
				}
				if isAskedInThread {
					scenario = AskedInThread(scenario)
				}
				if _, errorValue := RunVirtualSession(context.Background(), scenario); errorValue != nil {
					t.Fatalf("%s: %v", label, errorValue)
				}
			})
		}
	}
}
