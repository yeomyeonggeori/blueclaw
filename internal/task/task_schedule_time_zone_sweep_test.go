package task

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

type recordingTimeZoneRepairRepository struct {
	emptyCount      int
	countError      error
	fillError       error
	filledTimeZones []string
}

func (repository *recordingTimeZoneRepairRepository) CountEmptyTaskScheduleTimeZone() (int, error) {
	return repository.emptyCount, repository.countError
}

func (repository *recordingTimeZoneRepairRepository) FillEmptyTaskScheduleTimeZone(timeZone string) (int, error) {
	if repository.fillError != nil {
		return 0, repository.fillError
	}
	repository.filledTimeZones = append(repository.filledTimeZones, timeZone)
	return repository.emptyCount, nil
}

func sweepWithLoggedOutput(repository *recordingTimeZoneRepairRepository, companyTimeZone string) string {
	logOutput := &bytes.Buffer{}
	SweepEmptyTaskScheduleTimeZone(repository, companyTimeZone, slog.New(slog.NewTextHandler(logOutput, nil)))
	return logOutput.String()
}

func TestAStoredScheduleWithNoZoneTakesTheCompanys(t *testing.T) {
	repository := &recordingTimeZoneRepairRepository{emptyCount: 3}

	logOutput := sweepWithLoggedOutput(repository, "Asia/Seoul")

	if len(repository.filledTimeZones) != 1 || repository.filledTimeZones[0] != "Asia/Seoul" {
		t.Fatalf("expected one fill with the company's zone, got %v", repository.filledTimeZones)
	}
	if !strings.Contains(logOutput, "task_schedule.time_zone_filled") || !strings.Contains(logOutput, "count=3") {
		t.Fatalf("expected one line naming how many were filled, got %s", logOutput)
	}
}

func TestASweepThatFindsNoEmptyZoneWritesNothing(t *testing.T) {
	repository := &recordingTimeZoneRepairRepository{}

	logOutput := sweepWithLoggedOutput(repository, "Asia/Seoul")

	if len(repository.filledTimeZones) != 0 {
		t.Fatalf("nothing was empty, so nothing is written, got %v", repository.filledTimeZones)
	}
	if logOutput != "" {
		t.Fatalf("a startup with nothing to repair says nothing, got %s", logOutput)
	}
}

func TestACompanyWithNoZoneOfItsOwnSaysHowManyAreEmpty(t *testing.T) {
	repository := &recordingTimeZoneRepairRepository{emptyCount: 2}

	logOutput := sweepWithLoggedOutput(repository, "  ")

	if len(repository.filledTimeZones) != 0 {
		t.Fatalf("there was no zone to fill from, so nothing is written, got %v", repository.filledTimeZones)
	}
	if !strings.Contains(logOutput, "task_schedule.time_zone_empty") || !strings.Contains(logOutput, "count=2") {
		t.Fatalf("expected the count said plainly, got %s", logOutput)
	}
}

func TestASweepThatCannotReadTheRowsSaysSoAndLeavesThemAlone(t *testing.T) {
	repository := &recordingTimeZoneRepairRepository{emptyCount: 4, countError: errors.New("connection refused")}

	logOutput := sweepWithLoggedOutput(repository, "Asia/Seoul")

	if len(repository.filledTimeZones) != 0 {
		t.Fatalf("a failed count fills nothing, got %v", repository.filledTimeZones)
	}
	if !strings.Contains(logOutput, "task_schedule.time_zone_sweep_failed") {
		t.Fatalf("expected the failure named, got %s", logOutput)
	}
}
