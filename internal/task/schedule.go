package task

import (
	"strings"
	"time"
)

func ScheduleSessionID(scheduleID string) string {
	return "schedule:" + strings.TrimSpace(scheduleID)
}

type ScheduleKind string

const (
	ScheduleKindOnce     ScheduleKind = "once"
	ScheduleKindInterval ScheduleKind = "interval"
	ScheduleKindCron     ScheduleKind = "cron"
)

type ScheduleExecutionMode string

const (
	ScheduleExecutionModeAgent   ScheduleExecutionMode = "agent"
	ScheduleExecutionModeMessage ScheduleExecutionMode = "message"
)

type Schedule struct {
	ScheduleID        string                `json:"taskScheduleID"`
	CreatorPersonID   string                `json:"creatorPersonID"`
	Name              string                `json:"name"`
	Prompt            string                `json:"prompt"`
	ExecutionMode     ScheduleExecutionMode `json:"executionMode"`
	AgentProfileName  string                `json:"agentProfileName"`
	Platform          string                `json:"platform"`
	ConversationID    string                `json:"conversationID"`
	ReplyTargetID     string                `json:"replyTargetID"`
	TimeZone          string                `json:"timeZone"`
	Kind              ScheduleKind          `json:"kind"`
	RunAt             *time.Time            `json:"runAt"`
	IntervalSecond    int                   `json:"intervalSecond"`
	CronExpression    string                `json:"cronExpression"`
	MaxRunCount       int                   `json:"maxRunCount,omitempty"`
	CompletedRunCount int                   `json:"completedRunCount"`
	ExpiresAt         *time.Time            `json:"expiresAt"`
	NextRunAt         *time.Time            `json:"nextRunAt"`
	LastRunAt         *time.Time            `json:"lastRunAt"`
	LastTaskRunID     string                `json:"lastTaskRunID"`
	LeaseOwner        string                `json:"leaseOwner"`
	LeasedUntil       *time.Time            `json:"leasedUntil"`
	FailureCount      int                   `json:"failureCount"`
	LastError         string                `json:"lastError"`
	NextAttemptAt     *time.Time            `json:"nextAttemptAt"`
	CreatedAt         time.Time             `json:"createdAt"`
	UpdatedAt         time.Time             `json:"updatedAt"`
}
