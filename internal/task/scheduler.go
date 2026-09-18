package task

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

var (
	errorInvalidSchedule         = errors.New("invalid task schedule")
	errorInvalidCronExpression   = errors.New("invalid cron expression")
	errorUnableToFindNextTaskRun = errors.New("unable to find next task run")
)

type Scheduler struct{}

type cronExpressionMatcher struct {
	allowsAnyValue bool
	allowedValues  map[int]struct{}
}

type parsedCronExpression struct {
	minute     cronExpressionMatcher
	hour       cronExpressionMatcher
	dayOfMonth cronExpressionMatcher
	month      cronExpressionMatcher
	dayOfWeek  cronExpressionMatcher
}

func (scheduler Scheduler) ScheduleTask(taskRun TaskRun) TaskRun {
	taskRun.Status = TaskStatusPlanned
	return taskRun
}

func (scheduler Scheduler) InitializeSchedule(schedule Schedule, referenceTime time.Time) (Schedule, error) {
	if scheduleReachedRunLimit(schedule) {
		schedule.NextRunAt = nil
		return schedule, nil
	}
	if scheduleExpired(schedule, referenceTime) {
		schedule.NextRunAt = nil
		return schedule, nil
	}

	nextRunAt, isActive, errorValue := scheduler.calculateNextRunAt(schedule, referenceTime)
	if errorValue != nil {
		return Schedule{}, errorValue
	}
	if !isActive {
		schedule.NextRunAt = nil
		return schedule, nil
	}

	schedule.NextRunAt = &nextRunAt
	return schedule, nil
}

func (scheduler Scheduler) AdvanceSchedule(schedule Schedule, executedAt time.Time) (Schedule, error) {
	schedule.LastRunAt = &executedAt
	schedule.CompletedRunCount++

	if scheduleReachedRunLimit(schedule) {
		schedule.NextRunAt = nil
		return schedule, nil
	}
	if scheduleExpired(schedule, executedAt) {
		schedule.NextRunAt = nil
		return schedule, nil
	}

	nextRunAt, isActive, errorValue := scheduler.calculateNextRunAt(schedule, executedAt)
	if errorValue != nil {
		return Schedule{}, errorValue
	}
	if !isActive {
		schedule.NextRunAt = nil
		return schedule, nil
	}

	schedule.NextRunAt = &nextRunAt
	return schedule, nil
}

func scheduleReachedRunLimit(schedule Schedule) bool {
	return schedule.MaxRunCount > 0 && schedule.CompletedRunCount >= schedule.MaxRunCount
}

func scheduleExpired(schedule Schedule, referenceTime time.Time) bool {
	return schedule.ExpiresAt != nil && !schedule.ExpiresAt.IsZero() && !schedule.ExpiresAt.After(referenceTime)
}

func (scheduler Scheduler) IsScheduleDue(schedule Schedule, referenceTime time.Time) bool {
	if scheduleExpired(schedule, referenceTime) {
		return false
	}
	if schedule.NextRunAt == nil {
		return false
	}

	return !schedule.NextRunAt.After(referenceTime)
}

func (scheduler Scheduler) calculateNextRunAt(schedule Schedule, referenceTime time.Time) (time.Time, bool, error) {
	switch schedule.Kind {
	case ScheduleKindOnce:
		return scheduler.calculateOneTimeNextRunAt(schedule, referenceTime)
	case ScheduleKindInterval:
		return scheduler.calculateIntervalNextRunAt(schedule, referenceTime)
	case ScheduleKindCron:
		return scheduler.calculateCronNextRunAt(schedule, referenceTime)
	default:
		return time.Time{}, false, errorInvalidSchedule
	}
}

func (scheduler Scheduler) calculateOneTimeNextRunAt(schedule Schedule, referenceTime time.Time) (time.Time, bool, error) {
	if schedule.RunAt == nil {
		return time.Time{}, false, errorInvalidSchedule
	}
	if schedule.LastRunAt != nil {
		return time.Time{}, false, nil
	}
	if schedule.RunAt.Before(referenceTime) {
		return time.Time{}, false, nil
	}

	return *schedule.RunAt, true, nil
}

func (scheduler Scheduler) calculateIntervalNextRunAt(schedule Schedule, referenceTime time.Time) (time.Time, bool, error) {
	if schedule.IntervalSecond <= 0 {
		return time.Time{}, false, errorInvalidSchedule
	}

	intervalDuration := time.Duration(schedule.IntervalSecond) * time.Second
	if schedule.LastRunAt == nil && schedule.RunAt == nil {
		return referenceTime, true, nil
	}
	baseTime := referenceTime
	if schedule.LastRunAt != nil {
		baseTime = *schedule.LastRunAt
	} else if schedule.RunAt != nil {
		baseTime = *schedule.RunAt
	}

	if baseTime.After(referenceTime) {
		return baseTime, true, nil
	}

	nextRunAt := baseTime
	for !nextRunAt.After(referenceTime) {
		nextRunAt = nextRunAt.Add(intervalDuration)
	}

	return nextRunAt, true, nil
}

func (scheduler Scheduler) calculateCronNextRunAt(schedule Schedule, referenceTime time.Time) (time.Time, bool, error) {
	if strings.TrimSpace(schedule.CronExpression) == "" {
		return time.Time{}, false, errorInvalidSchedule
	}
	location, errorValue := scheduler.scheduleLocation(schedule)
	if errorValue != nil {
		return time.Time{}, false, errorValue
	}

	parsedExpression, errorValue := parseCronExpression(schedule.CronExpression)
	if errorValue != nil {
		return time.Time{}, false, errorValue
	}

	searchTime := referenceTime.In(location)
	if schedule.RunAt != nil && schedule.RunAt.After(referenceTime) {
		searchTime = schedule.RunAt.In(location).Add(-time.Minute)
	}

	nextRunAt, errorValue := parsedExpression.findNextRunAt(searchTime)
	if errorValue != nil {
		return time.Time{}, false, errorValue
	}

	return nextRunAt.UTC(), true, nil
}

func (scheduler Scheduler) scheduleLocation(schedule Schedule) (*time.Location, error) {
	location, errorValue := ScheduleLocation(schedule.TimeZone)
	if errorValue != nil {
		return nil, errorInvalidSchedule
	}
	return location, nil
}

func parseCronExpression(cronExpression string) (parsedCronExpression, error) {
	fieldTexts := strings.Fields(cronExpression)
	if len(fieldTexts) != 5 {
		return parsedCronExpression{}, errorInvalidCronExpression
	}

	minute, errorValue := parseCronExpressionMatcher(fieldTexts[0], 0, 59, false)
	if errorValue != nil {
		return parsedCronExpression{}, errorValue
	}
	hour, errorValue := parseCronExpressionMatcher(fieldTexts[1], 0, 23, false)
	if errorValue != nil {
		return parsedCronExpression{}, errorValue
	}
	dayOfMonth, errorValue := parseCronExpressionMatcher(fieldTexts[2], 1, 31, false)
	if errorValue != nil {
		return parsedCronExpression{}, errorValue
	}
	month, errorValue := parseCronExpressionMatcher(fieldTexts[3], 1, 12, false)
	if errorValue != nil {
		return parsedCronExpression{}, errorValue
	}
	dayOfWeek, errorValue := parseCronExpressionMatcher(fieldTexts[4], 0, 7, true)
	if errorValue != nil {
		return parsedCronExpression{}, errorValue
	}

	return parsedCronExpression{
		minute:     minute,
		hour:       hour,
		dayOfMonth: dayOfMonth,
		month:      month,
		dayOfWeek:  dayOfWeek,
	}, nil
}

func parseCronExpressionMatcher(fieldText string, minimumValue int, maximumValue int, wrapsSevenToZero bool) (cronExpressionMatcher, error) {
	if fieldText == "*" {
		return cronExpressionMatcher{allowsAnyValue: true}, nil
	}

	allowedValues := map[int]struct{}{}
	for _, segment := range strings.Split(fieldText, ",") {
		errorValue := addCronExpressionSegment(strings.TrimSpace(segment), minimumValue, maximumValue, wrapsSevenToZero, allowedValues)
		if errorValue != nil {
			return cronExpressionMatcher{}, errorValue
		}
	}
	if len(allowedValues) == 0 {
		return cronExpressionMatcher{}, errorInvalidCronExpression
	}

	return cronExpressionMatcher{allowedValues: allowedValues}, nil
}

func addCronExpressionSegment(segment string, minimumValue int, maximumValue int, wrapsSevenToZero bool, allowedValues map[int]struct{}) error {
	if segment == "" {
		return errorInvalidCronExpression
	}

	rangeText := segment
	stepValue := 1
	if strings.Contains(segment, "/") {
		parts := strings.SplitN(segment, "/", 2)
		if len(parts) != 2 {
			return errorInvalidCronExpression
		}

		rangeText = parts[0]
		parsedStepValue, errorValue := strconv.Atoi(parts[1])
		if errorValue != nil || parsedStepValue <= 0 {
			return errorInvalidCronExpression
		}
		stepValue = parsedStepValue
	}

	startValue, endValue, errorValue := parseCronExpressionRange(rangeText, minimumValue, maximumValue, wrapsSevenToZero)
	if errorValue != nil {
		return errorValue
	}

	for candidateValue := startValue; candidateValue <= endValue; candidateValue += stepValue {
		allowedValues[normalizeCronExpressionValue(candidateValue, wrapsSevenToZero)] = struct{}{}
	}

	return nil
}

func parseCronExpressionRange(rangeText string, minimumValue int, maximumValue int, wrapsSevenToZero bool) (int, int, error) {
	if rangeText == "*" || rangeText == "" {
		return minimumValue, maximumValue, nil
	}
	if strings.Contains(rangeText, "-") {
		parts := strings.SplitN(rangeText, "-", 2)
		if len(parts) != 2 {
			return 0, 0, errorInvalidCronExpression
		}

		startValue, errorValue := parseCronExpressionValue(parts[0], minimumValue, maximumValue, wrapsSevenToZero)
		if errorValue != nil {
			return 0, 0, errorValue
		}
		endValue, errorValue := parseCronExpressionValue(parts[1], minimumValue, maximumValue, wrapsSevenToZero)
		if errorValue != nil {
			return 0, 0, errorValue
		}
		if startValue > endValue {
			return 0, 0, errorInvalidCronExpression
		}

		return startValue, endValue, nil
	}

	value, errorValue := parseCronExpressionValue(rangeText, minimumValue, maximumValue, wrapsSevenToZero)
	if errorValue != nil {
		return 0, 0, errorValue
	}

	return value, value, nil
}

func parseCronExpressionValue(valueText string, minimumValue int, maximumValue int, wrapsSevenToZero bool) (int, error) {
	value, errorValue := strconv.Atoi(valueText)
	if errorValue != nil {
		return 0, errorInvalidCronExpression
	}

	if value < minimumValue || value > maximumValue {
		return 0, errorInvalidCronExpression
	}

	return normalizeCronExpressionValue(value, wrapsSevenToZero), nil
}

func normalizeCronExpressionValue(value int, wrapsSevenToZero bool) int {
	if wrapsSevenToZero && value == 7 {
		return 0
	}

	return value
}

func (parsedExpression parsedCronExpression) findNextRunAt(referenceTime time.Time) (time.Time, error) {
	candidateTime := referenceTime.Truncate(time.Minute)
	if !candidateTime.After(referenceTime) {
		candidateTime = candidateTime.Add(time.Minute)
	}

	searchLimit := candidateTime.AddDate(5, 0, 0)
	for !candidateTime.After(searchLimit) {
		if parsedExpression.matches(candidateTime) {
			return candidateTime, nil
		}

		candidateTime = candidateTime.Add(time.Minute)
	}

	return time.Time{}, errorUnableToFindNextTaskRun
}

func (parsedExpression parsedCronExpression) matches(candidateTime time.Time) bool {
	if !parsedExpression.minute.matches(candidateTime.Minute()) {
		return false
	}
	if !parsedExpression.hour.matches(candidateTime.Hour()) {
		return false
	}
	if !parsedExpression.month.matches(int(candidateTime.Month())) {
		return false
	}

	return parsedExpression.matchesDay(candidateTime)
}

func (parsedExpression parsedCronExpression) matchesDay(candidateTime time.Time) bool {
	matchesDayOfMonth := parsedExpression.dayOfMonth.matches(candidateTime.Day())
	matchesDayOfWeek := parsedExpression.dayOfWeek.matches(int(candidateTime.Weekday()))
	hasDayOfMonthConstraint := !parsedExpression.dayOfMonth.allowsAnyValue
	hasDayOfWeekConstraint := !parsedExpression.dayOfWeek.allowsAnyValue

	if hasDayOfMonthConstraint && hasDayOfWeekConstraint {
		return matchesDayOfMonth || matchesDayOfWeek
	}
	if hasDayOfMonthConstraint {
		return matchesDayOfMonth
	}
	if hasDayOfWeekConstraint {
		return matchesDayOfWeek
	}

	return true
}

func (cronExpressionMatcher cronExpressionMatcher) matches(value int) bool {
	if cronExpressionMatcher.allowsAnyValue {
		return true
	}

	_, isFound := cronExpressionMatcher.allowedValues[value]
	return isFound
}
