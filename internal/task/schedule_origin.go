package task

import "strings"

const ScheduleOriginConversationIDPrefix = "schedule:"

func ScheduleOriginConversationID(taskScheduleID string) string {
	return ScheduleOriginConversationIDPrefix + strings.TrimSpace(taskScheduleID)
}

func IsScheduleStartedTaskRun(taskRun TaskRun) bool {
	return strings.HasPrefix(strings.TrimSpace(taskRun.OriginConversationID), ScheduleOriginConversationIDPrefix)
}
