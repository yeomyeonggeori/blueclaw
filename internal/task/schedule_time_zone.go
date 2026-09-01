package task

import (
	"strings"
	"time"
)

func ScheduleTimeZoneName(timeZone string) string {
	if named := strings.TrimSpace(timeZone); named != "" {
		return named
	}
	return time.UTC.String()
}

func ScheduleLocation(timeZone string) (*time.Location, error) {
	return time.LoadLocation(ScheduleTimeZoneName(timeZone))
}
