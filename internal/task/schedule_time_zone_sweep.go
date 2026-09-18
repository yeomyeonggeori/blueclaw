package task

import (
	"log/slog"
	"strings"
)

func SweepEmptyScheduleTimeZone(repository ScheduleTimeZoneRepairRepository, companyTimeZone string, logger *slog.Logger) {
	emptyCount, errorValue := repository.CountEmptyScheduleTimeZone()
	if errorValue != nil {
		logger.Warn("task_schedule.time_zone_sweep_failed", "stage", "count", "error", errorValue)
		return
	}
	if emptyCount == 0 {
		return
	}
	filledTimeZone := strings.TrimSpace(companyTimeZone)
	if filledTimeZone == "" {
		logger.Warn("task_schedule.time_zone_empty", "count", emptyCount, "reason", "the company names no time zone to fill them from")
		return
	}
	filledCount, errorValue := repository.FillEmptyScheduleTimeZone(filledTimeZone)
	if errorValue != nil {
		logger.Warn("task_schedule.time_zone_sweep_failed", "stage", "fill", "error", errorValue)
		return
	}
	logger.Info("task_schedule.time_zone_filled", "count", filledCount, "timeZone", filledTimeZone)
}
