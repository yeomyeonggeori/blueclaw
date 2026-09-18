package task

import (
	"errors"
	"testing"
	"time"
)

func scheduleCreateContextFixture(referenceTime time.Time) ScheduleCreateContext {
	return ScheduleCreateContext{
		CreatorPersonID: "person-이샘플",
		Delivery: ScheduleDeliveryBinding{
			Platform:       "buzz",
			ConversationID: "channel-1",
			ReplyTargetID:  "post-1",
		},
		CompanyTimeZone: "Asia/Seoul",
		ReferenceTime:   referenceTime,
	}
}

func TestBuildScheduleCreateRefusesAnIncompleteDeliveryBinding(t *testing.T) {
	referenceTime := time.Now().UTC()
	input := ScheduleCreateInput{TaskInstruction: "brief me", Kind: "once", RunAt: referenceTime.Add(time.Hour).Format(time.RFC3339)}
	for expectedError, createContext := range map[error]ScheduleCreateContext{
		ErrScheduleRequesterRequired:    {Delivery: ScheduleDeliveryBinding{Platform: "buzz", ConversationID: "channel-1", ReplyTargetID: "post-1"}, ReferenceTime: referenceTime},
		ErrScheduleConversationRequired: {CreatorPersonID: "person-이샘플", Delivery: ScheduleDeliveryBinding{ReplyTargetID: "post-1"}, ReferenceTime: referenceTime},
		ErrScheduleReplyTargetRequired:  {CreatorPersonID: "person-이샘플", Delivery: ScheduleDeliveryBinding{Platform: "buzz", ConversationID: "channel-1"}, ReferenceTime: referenceTime},
	} {
		if _, errorValue := BuildScheduleCreate(input, createContext); !errors.Is(errorValue, expectedError) {
			t.Fatalf("error = %v, want %v", errorValue, expectedError)
		}
	}
}

func TestBuildScheduleCreateRequiresARepeatPolicyBound(t *testing.T) {
	referenceTime := time.Now().UTC()
	createContext := scheduleCreateContextFixture(referenceTime)
	for expectedError, repeatPolicy := range map[error]string{
		ErrScheduleRepeatPolicyRequired: "",
		ErrScheduleFiniteBoundRequired:  "finite",
	} {
		_, errorValue := BuildScheduleCreate(ScheduleCreateInput{
			TaskInstruction: "brief me",
			Kind:            "interval",
			IntervalSecond:  3600,
			RepeatPolicy:    repeatPolicy,
		}, createContext)
		if !errors.Is(errorValue, expectedError) {
			t.Fatalf("repeatPolicy %q error = %v, want %v", repeatPolicy, errorValue, expectedError)
		}
	}
}

func TestInitializeScheduleCreateRefusesASchedulePastItsOnlyRun(t *testing.T) {
	referenceTime := time.Now().UTC()

	_, errorValue := InitializeScheduleCreate(ScheduleCreateInput{
		TaskInstruction: "brief me",
		Kind:            "once",
		RunAt:           referenceTime.Add(-time.Hour).Format(time.RFC3339),
	}, scheduleCreateContextFixture(referenceTime))

	if !errors.Is(errorValue, ErrScheduleNoFutureRun) {
		t.Fatalf("error = %v, want %v", errorValue, ErrScheduleNoFutureRun)
	}
}

func TestInitializeScheduleCreateFallsBackToTheDefaultProfileAndTaskInstruction(t *testing.T) {
	referenceTime := time.Now().UTC()

	schedule, errorValue := InitializeScheduleCreate(ScheduleCreateInput{
		TaskInstruction: "주간 보고를 정리한다",
		Kind:            "once",
		RunAt:           referenceTime.Add(time.Hour).Format(time.RFC3339),
	}, scheduleCreateContextFixture(referenceTime))

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if schedule.AgentProfileName != "default" || schedule.Name != "주간 보고를 정리한다" || schedule.TimeZone != "Asia/Seoul" {
		t.Fatalf("unexpected schedule: %+v", schedule)
	}
}

func TestApplyScheduleUpdateRefusesAnInvalidKindInsteadOfFallingBackToOnce(t *testing.T) {
	referenceTime := time.Now().UTC()
	nextRunAt := referenceTime.Add(time.Hour)
	kind := "weekly"

	_, errorValue := ApplyScheduleUpdate(Schedule{
		ScheduleID:     "schedule-1",
		Kind:           ScheduleKindInterval,
		IntervalSecond: 3600,
		NextRunAt:      &nextRunAt,
	}, ScheduleUpdateInput{Kind: &kind}, "Asia/Seoul", referenceTime)

	if !errors.Is(errorValue, ErrScheduleKindInvalid) {
		t.Fatalf("error = %v, want %v", errorValue, ErrScheduleKindInvalid)
	}
}

func TestApplyScheduleUpdateClearsTheCadenceFieldsTheNewKindCannotUse(t *testing.T) {
	referenceTime := time.Now().UTC()
	nextRunAt := referenceTime.Add(time.Hour)
	kind := string(ScheduleKindOnce)
	runAt := nextRunAt.Format(time.RFC3339)

	updatedSchedule, errorValue := ApplyScheduleUpdate(Schedule{
		ScheduleID:     "schedule-1",
		Kind:           ScheduleKindInterval,
		IntervalSecond: 3600,
		MaxRunCount:    5,
		NextRunAt:      &nextRunAt,
	}, ScheduleUpdateInput{Kind: &kind, RunAt: &runAt}, "Asia/Seoul", referenceTime)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if updatedSchedule.IntervalSecond != 0 || updatedSchedule.MaxRunCount != 0 {
		t.Fatalf("unexpected cadence: %+v", updatedSchedule)
	}
}

func TestApplyScheduleUpdateRefusesABlankTaskInstruction(t *testing.T) {
	referenceTime := time.Now().UTC()
	nextRunAt := referenceTime.Add(time.Hour)
	taskInstruction := "   "

	_, errorValue := ApplyScheduleUpdate(Schedule{
		ScheduleID: "schedule-1",
		Kind:       ScheduleKindOnce,
		RunAt:      &nextRunAt,
		NextRunAt:  &nextRunAt,
	}, ScheduleUpdateInput{TaskInstruction: &taskInstruction}, "Asia/Seoul", referenceTime)

	if !errors.Is(errorValue, ErrScheduleTaskInstructionRequired) {
		t.Fatalf("error = %v, want %v", errorValue, ErrScheduleTaskInstructionRequired)
	}
}

func TestScheduleUpdateChangesNothingAnswersAnEmptyInput(t *testing.T) {
	description := "주간 보고"
	if !ScheduleUpdateChangesNothing(ScheduleUpdateInput{}) {
		t.Fatal("an empty update input should change nothing")
	}
	if ScheduleUpdateChangesNothing(ScheduleUpdateInput{Description: &description}) {
		t.Fatal("a description update should change something")
	}
}
