package postgres

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func TestMorningBriefingConfigurationChangeTracksCadenceSeparately(t *testing.T) {
	existing := managedMorningBriefingSchedule{
		name: "Morning briefing", prompt: "brief", executionMode: "agent", agentProfileName: "default",
		platform: "mattermost", conversationID: "channel", replyTargetID: "reply", timeZone: "Asia/Seoul",
		kind: "cron", cronExpression: "0 8 * * *", nextRunAt: timePointer(time.Date(2026, 9, 7, 8, 0, 0, 0, time.UTC)),
	}
	desired := task.TaskSchedule{
		Name: "Morning briefing", Prompt: "brief", ExecutionMode: task.TaskScheduleExecutionModeAgent, AgentProfileName: "default",
		Platform: "mattermost", ConversationID: "channel", ReplyTargetID: "reply", TimeZone: "Asia/Seoul",
		Kind: task.TaskScheduleKindCron, CronExpression: "0 8 * * *", NextRunAt: timePointer(time.Date(2026, 9, 8, 8, 0, 0, 0, time.UTC)),
	}
	changed, cadenceChanged := morningBriefingConfigurationChanged(existing, desired)
	if changed || cadenceChanged {
		t.Fatalf("expected an unchanged configuration, got changed=%v cadenceChanged=%v", changed, cadenceChanged)
	}
	desired.CronExpression = "30 8 * * *"
	changed, cadenceChanged = morningBriefingConfigurationChanged(existing, desired)
	if !changed || !cadenceChanged {
		t.Fatalf("expected cadence change, got changed=%v cadenceChanged=%v", changed, cadenceChanged)
	}
	desired.CronExpression = existing.cronExpression
	desired.Prompt = "updated brief"
	changed, cadenceChanged = morningBriefingConfigurationChanged(existing, desired)
	if !changed || cadenceChanged {
		t.Fatalf("expected prompt-only change, got changed=%v cadenceChanged=%v", changed, cadenceChanged)
	}
}

func TestMorningBriefingMigrationProtectsManagedSchedules(t *testing.T) {
	document, errorValue := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "031_morning_briefing.sql"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, fragment := range []string{
		"person_id text PRIMARY KEY REFERENCES person(person_id) ON DELETE RESTRICT",
		"task_schedule_id text NOT NULL UNIQUE REFERENCES task_schedule(task_schedule_id) ON DELETE RESTRICT",
	} {
		if !strings.Contains(string(document), fragment) {
			t.Fatalf("expected migration to contain %q", fragment)
		}
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}
