package task

import (
	"crypto/sha256"
	"fmt"
)

type BriefingKind string

const (
	BriefingKindMorning BriefingKind = "morning"
	BriefingKindWeekly  BriefingKind = "weekly"
)

var BriefingKinds = []BriefingKind{BriefingKindMorning, BriefingKindWeekly}

var briefingScheduleIDSeeds = map[BriefingKind]string{
	BriefingKindMorning: "morning-briefing:",
	BriefingKindWeekly:  "weekly-report:",
}

func BriefingScheduleID(kind BriefingKind, personID string) string {
	digest := sha256.Sum256([]byte(briefingScheduleIDSeeds[kind] + personID))
	return fmt.Sprintf("%x", digest)
}

func BriefingKindOf(schedule Schedule) (BriefingKind, bool) {
	if schedule.CreatorPersonID == "" {
		return "", false
	}
	for _, kind := range BriefingKinds {
		if schedule.ScheduleID == BriefingScheduleID(kind, schedule.CreatorPersonID) {
			return kind, true
		}
	}
	return "", false
}

func IsBriefing(schedule Schedule) bool {
	_, isBriefing := BriefingKindOf(schedule)
	return isBriefing
}
