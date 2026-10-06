package task

import "github.com/yeomyeonggeori/bluecollar/agentcontract"

const (
	TaskEventBlueclawTaskExecutionDuration = "blueclaw.task.execution_duration"

	TaskEventAmbientDutyLaunch = "agent.ambient_duty_launch"

	TaskEventLaunchFailureReply  = "blueclaw.launch.failure_reply"
	TaskEventLaunchFailureReport = "blueclaw.launch.failure_report"
	TaskEventLaunchLimitReply    = "blueclaw.launch.limit_reply"
	TaskEventLaunchLimitStop     = "blueclaw.launch.limit_stop"
	TaskEventLaunchGoalBlocked   = "blueclaw.launch.goal_blocked"

	TaskEventConnectorFilesUndelivered     = "blueclaw.connector.files_undelivered"
	TaskEventConnectorStopOutboxSuppressed = "blueclaw.connector.stop_outbox_suppressed"
)

func IsAskRequestedEvent(eventName string) bool {
	return eventName == agentcontract.TaskEventAskRequested || eventName == agentcontract.TaskEventAgentInputRequested
}
