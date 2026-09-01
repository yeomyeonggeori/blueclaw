package task

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

var (
	errorInvalidTaskSchedule     = errors.New("invalid task schedule")
	errorInvalidCronExpression   = errors.New("invalid cron expression")
	errorUnableToFindNextTaskRun = errors.New("unable to find next task run")
)

type TaskScheduler struct{}

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

func (taskScheduler TaskScheduler) ScheduleTask(taskRun TaskRun) TaskRun {
	taskRun.Status = TaskStatusPlanned
	return taskRun
}

func (taskScheduler TaskScheduler) InitializeTaskSchedule(taskSchedule TaskSchedule, referenceTime time.Time) (TaskSchedule, error) {
	if taskScheduleReachedRunLimit(taskSchedule) {
		taskSchedule.NextRunAt = nil
		return taskSchedule, nil
	}
	if taskScheduleExpired(taskSchedule, referenceTime) {
		taskSchedule.NextRunAt = nil
		return taskSchedule, nil
	}

	nextRunAt, isActive, errorValue := taskScheduler.calculateNextRunAt(taskSchedule, referenceTime)
	if errorValue != nil {
		return TaskSchedule{}, errorValue
	}
	if !isActive {
		taskSchedule.NextRunAt = nil
		return taskSchedule, nil
	}

	taskSchedule.NextRunAt = &nextRunAt
	return taskSchedule, nil
}

func (taskScheduler TaskScheduler) AdvanceTaskSchedule(taskSchedule TaskSchedule, executedAt time.Time) (TaskSchedule, error) {
	taskSchedule.LastRunAt = &executedAt
	taskSchedule.CompletedRunCount++

	if taskScheduleReachedRunLimit(taskSchedule) {
		taskSchedule.NextRunAt = nil
		return taskSchedule, nil
	}
	if taskScheduleExpired(taskSchedule, executedAt) {
		taskSchedule.NextRunAt = nil
		return taskSchedule, nil
	}

	nextRunAt, isActive, errorValue := taskScheduler.calculateNextRunAt(taskSchedule, executedAt)
	if errorValue != nil {
		return TaskSchedule{}, errorValue
	}
	if !isActive {
		taskSchedule.NextRunAt = nil
		return taskSchedule, nil
	}

	taskSchedule.NextRunAt = &nextRunAt
	return taskSchedule, nil
}

func taskScheduleReachedRunLimit(taskSchedule TaskSchedule) bool {
	return taskSchedule.MaxRunCount > 0 && taskSchedule.CompletedRunCount >= taskSchedule.MaxRunCount
}

func taskScheduleExpired(taskSchedule TaskSchedule, referenceTime time.Time) bool {
	return taskSchedule.ExpiresAt != nil && !taskSchedule.ExpiresAt.IsZero() && !taskSchedule.ExpiresAt.After(referenceTime)
}

func (taskScheduler TaskScheduler) IsTaskScheduleDue(taskSchedule TaskSchedule, referenceTime time.Time) bool {
	if taskScheduleExpired(taskSchedule, referenceTime) {
		return false
	}
	if taskSchedule.NextRunAt == nil {
		return false
	}

	return !taskSchedule.NextRunAt.After(referenceTime)
}

func (taskScheduler TaskScheduler) calculateNextRunAt(taskSchedule TaskSchedule, referenceTime time.Time) (time.Time, bool, error) {
	switch taskSchedule.Kind {
	case TaskScheduleKindOnce:
		return taskScheduler.calculateOneTimeNextRunAt(taskSchedule, referenceTime)
	case TaskScheduleKindInterval:
		return taskScheduler.calculateIntervalNextRunAt(taskSchedule, referenceTime)
	case TaskScheduleKindCron:
		return taskScheduler.calculateCronNextRunAt(taskSchedule, referenceTime)
	default:
		return time.Time{}, false, errorInvalidTaskSchedule
	}
}

func (taskScheduler TaskScheduler) calculateOneTimeNextRunAt(taskSchedule TaskSchedule, referenceTime time.Time) (time.Time, bool, error) {
	if taskSchedule.RunAt == nil {
		return time.Time{}, false, errorInvalidTaskSchedule
	}
	if taskSchedule.LastRunAt != nil {
		return time.Time{}, false, nil
	}
	if taskSchedule.RunAt.Before(referenceTime) {
		return time.Time{}, false, nil
	}

	return *taskSchedule.RunAt, true, nil
}

func (taskScheduler TaskScheduler) calculateIntervalNextRunAt(taskSchedule TaskSchedule, referenceTime time.Time) (time.Time, bool, error) {
	if taskSchedule.IntervalSecond <= 0 {
		return time.Time{}, false, errorInvalidTaskSchedule
	}

	intervalDuration := time.Duration(taskSchedule.IntervalSecond) * time.Second
	if taskSchedule.LastRunAt == nil && taskSchedule.RunAt == nil {
		return referenceTime, true, nil
	}
	baseTime := referenceTime
	if taskSchedule.LastRunAt != nil {
		baseTime = *taskSchedule.LastRunAt
	} else if taskSchedule.RunAt != nil {
		baseTime = *taskSchedule.RunAt
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

func (taskScheduler TaskScheduler) calculateCronNextRunAt(taskSchedule TaskSchedule, referenceTime time.Time) (time.Time, bool, error) {
	if strings.TrimSpace(taskSchedule.CronExpression) == "" {
		return time.Time{}, false, errorInvalidTaskSchedule
	}
	location, errorValue := taskScheduler.taskScheduleLocation(taskSchedule)
	if errorValue != nil {
		return time.Time{}, false, errorValue
	}

	parsedExpression, errorValue := parseCronExpression(taskSchedule.CronExpression)
	if errorValue != nil {
		return time.Time{}, false, errorValue
	}

	searchTime := referenceTime.In(location)
	if taskSchedule.RunAt != nil && taskSchedule.RunAt.After(referenceTime) {
		searchTime = taskSchedule.RunAt.In(location).Add(-time.Minute)
	}

	nextRunAt, errorValue := parsedExpression.findNextRunAt(searchTime)
	if errorValue != nil {
		return time.Time{}, false, errorValue
	}

	return nextRunAt.UTC(), true, nil
}

func (taskScheduler TaskScheduler) taskScheduleLocation(taskSchedule TaskSchedule) (*time.Location, error) {
	location, errorValue := ScheduleLocation(taskSchedule.TimeZone)
	if errorValue != nil {
		return nil, errorInvalidTaskSchedule
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
