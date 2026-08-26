package agentruntime

import (
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestStandingDutyToolNamesExcludeOutwardAndSystemTools(t *testing.T) {
	for _, duty := range agentcontract.StandingDuties() {
		allowed := map[string]bool{}
		for _, toolName := range duty.ToolNames {
			allowed[toolName] = true
		}
		for _, forbiddenToolName := range []string{"message_send", "message_update", "terminal_run", "web_fetch", "file_write", "ask_input", "task_delete", "event_delete"} {
			if allowed[forbiddenToolName] {
				t.Fatalf("standing duty %q must not allow %q", duty.Name, forbiddenToolName)
			}
		}
		if len(duty.ToolNames) == 0 {
			t.Fatalf("standing duty %q must name the tools it may use", duty.Name)
		}
	}
}

func TestAmbientLaunchCeilingComesFromTheMatchedDuty(t *testing.T) {
	duty, isKnownDuty := agentcontract.StandingDutyByName("calendar_upkeep")
	if !isKnownDuty {
		t.Fatalf("expected calendar_upkeep to be a registered standing duty")
	}

	ceiling := registeredToolNameCeilingForLaunch(TaskLaunchRequest{
		AmbientDuty: agentcontract.AmbientDutyContext{IsMatch: true, Name: "calendar_upkeep"},
	})

	if len(ceiling) != len(duty.ToolNames) {
		t.Fatalf("expected the ceiling to be the duty tool names, got %+v", ceiling)
	}
	for index, toolName := range duty.ToolNames {
		if ceiling[index] != toolName {
			t.Fatalf("expected the ceiling to be the duty tool names, got %+v", ceiling)
		}
	}
}

func TestAddressedLaunchHasNoToolCeiling(t *testing.T) {
	if ceiling := registeredToolNameCeilingForLaunch(TaskLaunchRequest{}); ceiling != nil {
		t.Fatalf("expected an addressed launch to keep the full catalog, got %+v", ceiling)
	}
}

func TestUnknownDutyNameLeavesNoCeilingAndNoMatch(t *testing.T) {
	ambientDuty := agentcontract.AmbientDutyContext{IsMatch: true, Name: "invented_duty", Confidence: 0.99}

	if normalized := ambientDuty.Normalized(); normalized.IsMatch {
		t.Fatalf("expected an unregistered duty name to be dropped, got %+v", normalized)
	}
	if ceiling := registeredToolNameCeilingForLaunch(TaskLaunchRequest{AmbientDuty: ambientDuty}); ceiling != nil {
		t.Fatalf("expected no ceiling for an unregistered duty name, got %+v", ceiling)
	}
}
