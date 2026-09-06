package agentruntime

import (
	"slices"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestMorningBriefingScheduledLaunchHasReadOnlyToolCeiling(t *testing.T) {
	personID := "person-1"
	ceiling := registeredToolNameCeilingForLaunch(TaskLaunchRequest{
		Source:            TaskLaunchSourceScheduled,
		RequesterPersonID: personID,
		ScheduledRun:      agentcontract.ScheduledRunContext{ScheduleID: task.MorningBriefingScheduleID(personID)},
	})
	for _, requiredTool := range []string{"task_list", "event_list", "conversation_history", "memory_search", "persona_read"} {
		if !slices.Contains(ceiling, requiredTool) {
			t.Fatalf("expected %s in briefing ceiling %v", requiredTool, ceiling)
		}
	}
	for _, forbiddenTool := range []string{"message_send", "task_update", "persona_update", "schedule_cancel", "shell"} {
		if slices.Contains(ceiling, forbiddenTool) {
			t.Fatalf("forbidden tool %s in briefing ceiling %v", forbiddenTool, ceiling)
		}
	}
}

func TestMorningBriefingCeilingOnlyAppliesToScheduledMatchingID(t *testing.T) {
	personID := "person-1"
	if ceiling := registeredToolNameCeilingForLaunch(TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		RequesterPersonID: personID,
		ScheduledRun:      agentcontract.ScheduledRunContext{ScheduleID: task.MorningBriefingScheduleID(personID)},
	}); ceiling != nil {
		t.Fatalf("foreground launch unexpectedly received briefing ceiling %v", ceiling)
	}
	if ceiling := registeredToolNameCeilingForLaunch(TaskLaunchRequest{
		Source:            TaskLaunchSourceScheduled,
		RequesterPersonID: personID,
		ScheduledRun:      agentcontract.ScheduledRunContext{ScheduleID: "ordinary-schedule"},
	}); ceiling != nil {
		t.Fatalf("ordinary schedule unexpectedly received briefing ceiling %v", ceiling)
	}
}
