package task

import (
	"crypto/sha256"
	"fmt"
)

func MorningBriefingScheduleID(personID string) string {
	digest := sha256.Sum256([]byte("morning-briefing:" + personID))
	return fmt.Sprintf("%x", digest)
}

func IsMorningBriefing(schedule Schedule) bool {
	return schedule.CreatorPersonID != "" && schedule.ScheduleID == MorningBriefingScheduleID(schedule.CreatorPersonID)
}
