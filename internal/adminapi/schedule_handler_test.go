package adminapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func TestScheduleHandlerReturnsSummary(t *testing.T) {
	nextRunAt := time.Date(2026, 6, 6, 3, 0, 0, 0, time.UTC)
	handler := ScheduleHandler{
		SummaryRepository: scheduleSummaryRepositoryStub{
			summary: task.ScheduleSummary{
				ActiveCount:       3,
				UnboundedCount:    1,
				IntervalCount:     2,
				EarliestNextRunAt: &nextRunAt,
				LatestNextRunAt:   &nextRunAt,
				CheckedAt:         nextRunAt,
			},
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/schedule/summary", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleSummary(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	responseBody := responseRecorder.Body.String()
	for _, expectedFragment := range []string{`"activeCount":3`, `"unboundedCount":1`, `"intervalCount":2`} {
		if !strings.Contains(responseBody, expectedFragment) {
			t.Fatalf("expected response to contain %s, got %s", expectedFragment, responseBody)
		}
	}
	for _, forbiddenFragment := range []string{"prompt", "creator", "conversation", "replyTarget"} {
		if strings.Contains(responseBody, forbiddenFragment) {
			t.Fatalf("expected summary to avoid private schedule details, got %s", responseBody)
		}
	}
}

func TestScheduleHandlerListsActiveSchedules(t *testing.T) {
	nextRunAt := time.Date(2026, 6, 6, 3, 0, 0, 0, time.UTC)
	longPrompt := strings.Repeat("scheduled message ", 40)
	repository := &scheduleListRepositoryStub{
		schedules: []task.Schedule{{
			ScheduleID:       "schedule-1",
			CreatorPersonID:  "person-1",
			Prompt:           longPrompt,
			ExecutionMode:    task.ScheduleExecutionModeAgent,
			Kind:             task.ScheduleKindCron,
			CronExpression:   "0 * * * *",
			NextRunAt:        &nextRunAt,
			CreatedAt:        nextRunAt.Add(-time.Hour),
			UpdatedAt:        nextRunAt.Add(-time.Minute),
			ConversationID:   "channel-1",
			ReplyTargetID:    "post-1",
			AgentProfileName: "default",
		}},
	}
	handler := ScheduleHandler{ListRepository: repository}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/schedule?deliveryConversationID=channel-1&unboundedOnly=true&includeExpired=true&page=2&pageSize=5", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleList(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if repository.request.ConversationID != "channel-1" || !repository.request.UnboundedOnly || !repository.request.IncludeExpired || repository.request.Page != 2 || repository.request.PageSize != 5 {
		t.Fatalf("expected query parameters to reach repository, got %+v", repository.request)
	}
	responseBody := responseRecorder.Body.String()
	for _, expectedFragment := range []string{`"taskScheduleID":"schedule-1"`, `"deliveryChannelID":"channel-1"`, `"totalCount":1`, `"page":2`, `"pageSize":5`, `"promptPreview":"`} {
		if !strings.Contains(responseBody, expectedFragment) {
			t.Fatalf("expected response to contain %s, got %s", expectedFragment, responseBody)
		}
	}
	if strings.Contains(responseBody, longPrompt) {
		t.Fatalf("expected prompt to be compacted, got %s", responseBody)
	}
}

func TestScheduleToolListUsesOnlySignedPrincipalAndExactProjection(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	fullInstruction := strings.Repeat("full instruction ", 20)
	repository := &scheduleListRepositoryStub{schedules: []task.Schedule{
		{
			ScheduleID:      "other-schedule",
			CreatorPersonID: "person-other",
			Prompt:          "must not be selected",
			Kind:            task.ScheduleKindOnce,
			NextRunAt:       &nextRunAt,
		},
		{
			ScheduleID:      "failed-schedule",
			CreatorPersonID: "person-signed",
			Prompt:          fullInstruction,
			Name:            "Daily report",
			Kind:            task.ScheduleKindCron,
			CronExpression:  "0 9 * * *",
			NextRunAt:       &nextRunAt,
			LastError:       "temporary failure",
		},
	}}
	handler := ScheduleHandler{
		ListRepository: repository,
		ReaderPersonID: func(*http.Request) string { return "person-signed" },
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/schedule/tool-list", strings.NewReader(`{"status":"failed","limit":1,"creatorPersonID":"person-other"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleToolList(responseRecorder, request)

	if responseRecorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown creator field should be refused, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/admin/api/schedule/tool-list", strings.NewReader(`{"status":"failed"}{"limit":1}`))
	responseRecorder = httptest.NewRecorder()
	handler.HandleToolList(responseRecorder, request)
	if responseRecorder.Code != http.StatusBadRequest {
		t.Fatalf("trailing JSON should be refused, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/admin/api/schedule/tool-list", strings.NewReader(`{"status":"failed","limit":1}`))
	responseRecorder = httptest.NewRecorder()
	handler.HandleToolList(responseRecorder, request)
	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if repository.request.CreatorPersonID != "person-signed" || !repository.request.IncludeExpired || repository.request.PageSize != 20 {
		t.Fatalf("repository query escaped signed principal: %+v", repository.request)
	}
	var output task.ScheduleListOutput
	if errorValue := json.NewDecoder(responseRecorder.Body).Decode(&output); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(output.Schedules) != 1 || output.Schedules[0].ScheduleID != "failed-schedule" || output.Schedules[0].TaskInstruction != fullInstruction {
		t.Fatalf("unexpected tool projection: %+v", output)
	}
}

func TestScheduleToolListFailsClosedWithoutSignedPrincipal(t *testing.T) {
	for _, handler := range []ScheduleHandler{
		{},
		{ReaderPersonID: func(*http.Request) string { return "" }},
	} {
		responseRecorder := httptest.NewRecorder()
		handler.HandleToolList(responseRecorder, httptest.NewRequest(http.MethodPost, "/admin/api/schedule/tool-list", strings.NewReader(`{}`)))
		if responseRecorder.Code != http.StatusForbidden {
			t.Fatalf("unsigned request status = %d, want %d", responseRecorder.Code, http.StatusForbidden)
		}
	}
}

func TestScheduleHandlerCancelsOwnedSchedule(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	repository := &scheduleListRepositoryStub{
		schedules: []task.Schedule{{
			ScheduleID:      "schedule-1",
			CreatorPersonID: "person-1",
			Kind:            task.ScheduleKindInterval,
			IntervalSecond:  3600,
			NextRunAt:       &nextRunAt,
		}},
	}
	handler := ScheduleHandler{ListRepository: repository}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/schedule/cancel", strings.NewReader(`{"taskScheduleID":"schedule-1","creatorPersonID":"person-1"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleCancel(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if len(repository.cancelRequest.ScheduleIDs) != 1 || repository.cancelRequest.ScheduleIDs[0] != "schedule-1" {
		t.Fatalf("expected schedule id cancel request, got %+v", repository.cancelRequest)
	}
	if repository.cancelRequest.RequesterPersonID != "person-1" {
		t.Fatalf("expected requester person, got %+v", repository.cancelRequest)
	}
}

func TestScheduleHandlerDeletesOwnedSchedule(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	repository := &scheduleListRepositoryStub{
		schedules: []task.Schedule{{
			ScheduleID:      "schedule-1",
			CreatorPersonID: "person-1",
			Kind:            task.ScheduleKindInterval,
			IntervalSecond:  3600,
			NextRunAt:       &nextRunAt,
		}},
	}
	handler := ScheduleHandler{ListRepository: repository}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/schedule/delete", strings.NewReader(`{"taskScheduleID":"schedule-1","creatorPersonID":"person-1"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleDelete(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if repository.deleteRequest.ScheduleID != "schedule-1" || repository.deleteRequest.RequesterPersonID != "person-1" {
		t.Fatalf("expected delete request, got %+v", repository.deleteRequest)
	}
	if len(repository.schedules) != 0 {
		t.Fatalf("expected schedule to be removed, got %+v", repository.schedules)
	}
}

func TestScheduleHandlerRejectsDeleteCreatorMismatch(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	repository := &scheduleListRepositoryStub{
		schedules: []task.Schedule{{
			ScheduleID:      "schedule-1",
			CreatorPersonID: "person-1",
			Kind:            task.ScheduleKindInterval,
			IntervalSecond:  3600,
			NextRunAt:       &nextRunAt,
		}},
	}
	handler := ScheduleHandler{ListRepository: repository}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/schedule/delete", strings.NewReader(`{"taskScheduleID":"schedule-1","creatorPersonID":"person-2"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleDelete(responseRecorder, request)

	if responseRecorder.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if repository.deleteRequest.RequesterPersonID != "" {
		t.Fatalf("expected no delete request, got %+v", repository.deleteRequest)
	}
}

func TestScheduleHandlerRejectsCancelCreatorMismatch(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	repository := &scheduleListRepositoryStub{
		schedules: []task.Schedule{{
			ScheduleID:      "schedule-1",
			CreatorPersonID: "person-1",
			Kind:            task.ScheduleKindInterval,
			IntervalSecond:  3600,
			NextRunAt:       &nextRunAt,
		}},
	}
	handler := ScheduleHandler{ListRepository: repository}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/schedule/cancel", strings.NewReader(`{"taskScheduleID":"schedule-1","creatorPersonID":"person-2"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleCancel(responseRecorder, request)

	if responseRecorder.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if repository.cancelRequest.RequesterPersonID != "" {
		t.Fatalf("expected no cancel request, got %+v", repository.cancelRequest)
	}
}

func TestScheduleHandlerReturnsNotFoundForMissingCancelSchedule(t *testing.T) {
	handler := ScheduleHandler{ListRepository: &scheduleListRepositoryStub{}}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/schedule/cancel", strings.NewReader(`{"taskScheduleID":"missing","creatorPersonID":"person-1"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleCancel(responseRecorder, request)

	if responseRecorder.Code != http.StatusNotFound {
		t.Fatalf("expected not found response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
}

func TestScheduleHandlerUpdatesOwnedSchedule(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	repository := &scheduleListRepositoryStub{
		schedules: []task.Schedule{{
			ScheduleID:       "schedule-1",
			CreatorPersonID:  "person-1",
			Name:             "Old name",
			ExecutionMode:    task.ScheduleExecutionModeAgent,
			AgentProfileName: "default",
			TimeZone:         "Asia/Seoul",
			Kind:             task.ScheduleKindInterval,
			IntervalSecond:   3600,
			NextRunAt:        &nextRunAt,
			CreatedAt:        nextRunAt.Add(-time.Hour),
			UpdatedAt:        nextRunAt.Add(-time.Hour),
		}},
	}
	handler := ScheduleHandler{ListRepository: repository}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/schedule/update", strings.NewReader(`{"taskScheduleID":"schedule-1","creatorPersonID":"person-1","name":"New name","intervalSecond":7200,"repeatPolicy":"unbounded"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleUpdate(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	var result task.ScheduleUpdateResult
	if errorValue := json.NewDecoder(responseRecorder.Body).Decode(&result); errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Schedule.Name != "New name" || result.Schedule.IntervalSecond != 7200 || result.Schedule.NextRunAt == nil {
		t.Fatalf("expected updated schedule, got %+v", result.Schedule)
	}
	if repository.updateRequest.RequesterPersonID != "person-1" {
		t.Fatalf("expected requester person, got %+v", repository.updateRequest)
	}
}

func TestScheduleHandlerRejectsUpdateCreatorMismatch(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	repository := &scheduleListRepositoryStub{
		schedules: []task.Schedule{{
			ScheduleID:      "schedule-1",
			CreatorPersonID: "person-1",
			Kind:            task.ScheduleKindInterval,
			IntervalSecond:  3600,
			NextRunAt:       &nextRunAt,
		}},
	}
	handler := ScheduleHandler{ListRepository: repository}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/schedule/update", strings.NewReader(`{"taskScheduleID":"schedule-1","creatorPersonID":"person-2","name":"New name"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleUpdate(responseRecorder, request)

	if responseRecorder.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if repository.updateRequest.RequesterPersonID != "" {
		t.Fatalf("expected no update request, got %+v", repository.updateRequest)
	}
}

type scheduleSummaryRepositoryStub struct {
	summary task.ScheduleSummary
}

func (repository scheduleSummaryRepositoryStub) SummarizeActiveSchedules(time.Time) (task.ScheduleSummary, error) {
	return repository.summary, nil
}

type scheduleListRepositoryStub struct {
	request       task.ScheduleListRequest
	updateRequest task.ScheduleUpdateRequest
	deleteRequest task.ScheduleDeleteRequest
	cancelRequest task.ScheduleCancelRequest
	schedules     []task.Schedule

	upsertedSchedule task.Schedule
}

func (repository *scheduleListRepositoryStub) ListSchedules(request task.ScheduleListRequest) (task.ScheduleListResult, error) {
	repository.request = request
	return task.ScheduleListResult{Schedules: repository.schedules, TotalCount: len(repository.schedules), Page: request.Page, PageSize: request.PageSize}, nil
}

func (repository *scheduleListRepositoryStub) UpsertSchedule(schedule task.Schedule) error {
	repository.upsertedSchedule = schedule
	repository.schedules = append(repository.schedules, schedule)
	return nil
}

func (repository *scheduleListRepositoryStub) UpdateSchedule(request task.ScheduleUpdateRequest) (task.ScheduleUpdateResult, error) {
	repository.updateRequest = request
	for index, schedule := range repository.schedules {
		if schedule.ScheduleID != request.ScheduleID || schedule.CreatorPersonID != request.RequesterPersonID || schedule.NextRunAt == nil {
			continue
		}
		updatedSchedule := schedule
		if request.UpdateSchedule != nil {
			var errorValue error
			updatedSchedule, errorValue = request.UpdateSchedule(schedule)
			if errorValue != nil {
				return task.ScheduleUpdateResult{}, errorValue
			}
		}
		repository.schedules[index] = updatedSchedule
		return task.ScheduleUpdateResult{Schedule: updatedSchedule, IsFound: true}, nil
	}
	return task.ScheduleUpdateResult{}, nil
}

func (repository *scheduleListRepositoryStub) DeleteSchedule(request task.ScheduleDeleteRequest) (task.ScheduleDeleteResult, error) {
	repository.deleteRequest = request
	for index, schedule := range repository.schedules {
		if schedule.ScheduleID != request.ScheduleID || schedule.CreatorPersonID != request.RequesterPersonID {
			continue
		}
		repository.schedules = append(repository.schedules[:index], repository.schedules[index+1:]...)
		return task.ScheduleDeleteResult{Schedule: schedule, IsFound: true}, nil
	}
	return task.ScheduleDeleteResult{}, nil
}

func (repository *scheduleListRepositoryStub) CancelSchedules(request task.ScheduleCancelRequest) (task.ScheduleCancelResult, error) {
	repository.cancelRequest = request
	cancelledSchedules := []task.Schedule{}
	for index, schedule := range repository.schedules {
		if schedule.CreatorPersonID != request.RequesterPersonID || !containsScheduleID(request.ScheduleIDs, schedule.ScheduleID) {
			continue
		}
		schedule.NextRunAt = nil
		schedule.ExpiresAt = &request.CancelledAt
		repository.schedules[index] = schedule
		cancelledSchedules = append(cancelledSchedules, schedule)
	}
	return task.ScheduleCancelResult{Schedules: cancelledSchedules}, nil
}

func containsScheduleID(scheduleIDs []string, scheduleID string) bool {
	for _, candidateScheduleID := range scheduleIDs {
		if candidateScheduleID == scheduleID {
			return true
		}
	}
	return false
}

func scheduleWriteHandler(repository *scheduleListRepositoryStub, signedPersonID string) ScheduleHandler {
	return ScheduleHandler{
		ListRepository: repository,
		ReaderPersonID: func(*http.Request) string { return signedPersonID },
	}
}

func postScheduleTool(handler ScheduleHandler, path string, body string) *httptest.ResponseRecorder {
	responseRecorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	switch path {
	case "/admin/api/schedule/tool-create":
		handler.HandleToolCreate(responseRecorder, request)
	case "/admin/api/schedule/tool-update":
		handler.HandleToolUpdate(responseRecorder, request)
	default:
		handler.HandleToolCancel(responseRecorder, request)
	}
	return responseRecorder
}

func ownScheduleFixture(scheduleID string, creatorPersonID string, description string) task.Schedule {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	return task.Schedule{
		ScheduleID:       scheduleID,
		CreatorPersonID:  creatorPersonID,
		Name:             description,
		Prompt:           "brief " + description,
		ExecutionMode:    task.ScheduleExecutionModeAgent,
		AgentProfileName: "default",
		Platform:         "buzz",
		ConversationID:   "channel-1",
		ReplyTargetID:    "post-1",
		TimeZone:         "Asia/Seoul",
		Kind:             task.ScheduleKindInterval,
		IntervalSecond:   3600,
		NextRunAt:        &nextRunAt,
	}
}

func TestScheduleToolWritesFailClosedWithoutSignedPrincipal(t *testing.T) {
	repository := &scheduleListRepositoryStub{}
	for _, handler := range []ScheduleHandler{
		{ListRepository: repository},
		scheduleWriteHandler(repository, ""),
	} {
		for path, body := range map[string]string{
			"/admin/api/schedule/tool-create": `{"taskInstruction":"brief me","kind":"once","platform":"buzz","conversationID":"channel-1","replyTargetID":"post-1"}`,
			"/admin/api/schedule/tool-update": `{"scheduleHint":"schedule-1","description":"새 이름"}`,
			"/admin/api/schedule/tool-cancel": `{"scheduleHints":["schedule-1"]}`,
		} {
			responseRecorder := postScheduleTool(handler, path, body)
			if responseRecorder.Code != http.StatusForbidden {
				t.Fatalf("%s unsigned status = %d, want %d", path, responseRecorder.Code, http.StatusForbidden)
			}
		}
	}
	if repository.upsertedSchedule.ScheduleID != "" || repository.cancelRequest.RequesterPersonID != "" {
		t.Fatalf("an unsigned request reached the repository: %+v", repository)
	}
}

func TestScheduleToolWritesRejectBodySuppliedCreator(t *testing.T) {
	repository := &scheduleListRepositoryStub{
		schedules: []task.Schedule{ownScheduleFixture("schedule-1", "person-이샘플", "주간 보고")},
	}
	handler := scheduleWriteHandler(repository, "person-이샘플")
	for path, body := range map[string]string{
		"/admin/api/schedule/tool-create": `{"taskInstruction":"brief me","kind":"once","runAt":"2099-01-01T00:00:00Z","platform":"buzz","conversationID":"channel-1","replyTargetID":"post-1","creatorPersonID":"person-박예시"}`,
		"/admin/api/schedule/tool-update": `{"scheduleHint":"schedule-1","description":"새 이름","creatorPersonID":"person-박예시"}`,
		"/admin/api/schedule/tool-cancel": `{"scheduleHints":["schedule-1"],"creatorPersonID":"person-박예시"}`,
	} {
		responseRecorder := postScheduleTool(handler, path, body)
		if responseRecorder.Code != http.StatusBadRequest {
			t.Fatalf("%s body creator status = %d, want %d: %s", path, responseRecorder.Code, http.StatusBadRequest, responseRecorder.Body.String())
		}
	}
	if repository.upsertedSchedule.ScheduleID != "" || repository.cancelRequest.RequesterPersonID != "" {
		t.Fatalf("a body-supplied creator reached the repository: %+v", repository)
	}
}

func TestScheduleToolCreateRefusesWithoutDeliveryBinding(t *testing.T) {
	repository := &scheduleListRepositoryStub{}
	handler := scheduleWriteHandler(repository, "person-이샘플")

	responseRecorder := postScheduleTool(handler, "/admin/api/schedule/tool-create",
		`{"taskInstruction":"brief me","kind":"once","runAt":"2099-01-01T00:00:00Z"}`)

	if responseRecorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", responseRecorder.Code, http.StatusBadRequest, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), task.ErrScheduleConversationRequired.Error()) {
		t.Fatalf("expected the delivery refusal reason, got %s", responseRecorder.Body.String())
	}
	if repository.upsertedSchedule.ScheduleID != "" {
		t.Fatalf("a schedule without delivery binding was stored: %+v", repository.upsertedSchedule)
	}
}

func TestScheduleToolCreateRefusesScheduleWithNoFutureRun(t *testing.T) {
	repository := &scheduleListRepositoryStub{}
	handler := scheduleWriteHandler(repository, "person-이샘플")

	responseRecorder := postScheduleTool(handler, "/admin/api/schedule/tool-create",
		`{"taskInstruction":"brief me","kind":"once","runAt":"2020-01-01T00:00:00Z","platform":"buzz","conversationID":"channel-1","replyTargetID":"post-1"}`)

	if responseRecorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", responseRecorder.Code, http.StatusBadRequest, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), task.ErrScheduleNoFutureRun.Error()) {
		t.Fatalf("expected the no-future-run refusal, got %s", responseRecorder.Body.String())
	}
	if repository.upsertedSchedule.ScheduleID != "" {
		t.Fatalf("a schedule with no future run was stored: %+v", repository.upsertedSchedule)
	}
}

func TestScheduleToolCreateStoresTheSignedPrincipalsSchedule(t *testing.T) {
	repository := &scheduleListRepositoryStub{}
	handler := scheduleWriteHandler(repository, "person-이샘플")

	responseRecorder := postScheduleTool(handler, "/admin/api/schedule/tool-create",
		`{"taskInstruction":"주간 보고를 정리한다","description":"주간 보고","kind":"cron","cronExpression":"0 9 * * 1","repeatPolicy":"unbounded","timeZone":"Asia/Seoul","platform":"buzz","conversationID":"channel-1","replyTargetID":"post-1"}`)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", responseRecorder.Code, http.StatusOK, responseRecorder.Body.String())
	}
	var mutation task.ScheduleMutationResult
	if errorValue := json.NewDecoder(responseRecorder.Body).Decode(&mutation); errorValue != nil {
		t.Fatal(errorValue)
	}
	if mutation.Description != "주간 보고" || mutation.TaskInstruction != "주간 보고를 정리한다" || mutation.NextRunAt == nil {
		t.Fatalf("unexpected mutation projection: %+v", mutation)
	}
	if mutation.AgentProfileName != "default" || mutation.ConversationID != "channel-1" || mutation.ReplyTargetID != "post-1" {
		t.Fatalf("unexpected delivery projection: %+v", mutation)
	}
	if repository.upsertedSchedule.CreatorPersonID != "person-이샘플" {
		t.Fatalf("the stored creator escaped the signed principal: %+v", repository.upsertedSchedule)
	}
}

func TestScheduleToolUpdateResolvesDescriptionHintAndRewritesTaskInstruction(t *testing.T) {
	repository := &scheduleListRepositoryStub{
		schedules: []task.Schedule{ownScheduleFixture("schedule-1", "person-이샘플", "주간 보고")},
	}
	handler := scheduleWriteHandler(repository, "person-이샘플")

	responseRecorder := postScheduleTool(handler, "/admin/api/schedule/tool-update",
		`{"scheduleHint":"주간","taskInstruction":"주간 보고와 지표를 함께 정리한다","intervalSecond":7200,"repeatPolicy":"unbounded"}`)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", responseRecorder.Code, http.StatusOK, responseRecorder.Body.String())
	}
	var mutation task.ScheduleMutationResult
	if errorValue := json.NewDecoder(responseRecorder.Body).Decode(&mutation); errorValue != nil {
		t.Fatal(errorValue)
	}
	if mutation.ScheduleID != "schedule-1" || mutation.TaskInstruction != "주간 보고와 지표를 함께 정리한다" || mutation.IntervalSecond != 7200 {
		t.Fatalf("unexpected mutation projection: %+v", mutation)
	}
	if repository.updateRequest.RequesterPersonID != "person-이샘플" {
		t.Fatalf("the update escaped the signed principal: %+v", repository.updateRequest)
	}
}

func TestScheduleToolUpdateAnswersAmbiguousHintWithCandidatesAndMutatesNothing(t *testing.T) {
	repository := &scheduleListRepositoryStub{
		schedules: []task.Schedule{
			ownScheduleFixture("schedule-1", "person-이샘플", "주간 보고 월요일"),
			ownScheduleFixture("schedule-2", "person-이샘플", "주간 보고 금요일"),
		},
	}
	handler := scheduleWriteHandler(repository, "person-이샘플")

	responseRecorder := postScheduleTool(handler, "/admin/api/schedule/tool-update",
		`{"scheduleHint":"주간 보고","description":"새 이름"}`)

	if responseRecorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d: %s", responseRecorder.Code, http.StatusConflict, responseRecorder.Body.String())
	}
	var conflict scheduleHintConflict
	if errorValue := json.NewDecoder(responseRecorder.Body).Decode(&conflict); errorValue != nil {
		t.Fatal(errorValue)
	}
	if conflict.Hint != "주간 보고" || len(conflict.Candidates) != 2 || conflict.Error == "" {
		t.Fatalf("unexpected conflict body: %+v", conflict)
	}
	if repository.updateRequest.RequesterPersonID != "" {
		t.Fatalf("an ambiguous hint reached the repository: %+v", repository.updateRequest)
	}
}

func TestScheduleToolCancelCancelsNothingWhenOneHintIsAmbiguous(t *testing.T) {
	repository := &scheduleListRepositoryStub{
		schedules: []task.Schedule{
			ownScheduleFixture("schedule-1", "person-이샘플", "일일 점검"),
			ownScheduleFixture("schedule-2", "person-이샘플", "주간 보고 월요일"),
			ownScheduleFixture("schedule-3", "person-이샘플", "주간 보고 금요일"),
		},
	}
	handler := scheduleWriteHandler(repository, "person-이샘플")

	responseRecorder := postScheduleTool(handler, "/admin/api/schedule/tool-cancel",
		`{"scheduleHints":["일일 점검","주간 보고"]}`)

	if responseRecorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d: %s", responseRecorder.Code, http.StatusConflict, responseRecorder.Body.String())
	}
	var conflict scheduleHintConflict
	if errorValue := json.NewDecoder(responseRecorder.Body).Decode(&conflict); errorValue != nil {
		t.Fatal(errorValue)
	}
	if conflict.Hint != "주간 보고" || len(conflict.Candidates) != 2 {
		t.Fatalf("unexpected conflict body: %+v", conflict)
	}
	if repository.cancelRequest.RequesterPersonID != "" {
		t.Fatalf("a partly unresolved cancel reached the repository: %+v", repository.cancelRequest)
	}
}

func TestScheduleToolCancelCancelsEveryResolvedHint(t *testing.T) {
	repository := &scheduleListRepositoryStub{
		schedules: []task.Schedule{
			ownScheduleFixture("schedule-1", "person-이샘플", "일일 점검"),
			ownScheduleFixture("schedule-2", "person-이샘플", "주간 보고"),
		},
	}
	handler := scheduleWriteHandler(repository, "person-이샘플")

	responseRecorder := postScheduleTool(handler, "/admin/api/schedule/tool-cancel",
		`{"scheduleHints":["일일 점검","schedule-2"]}`)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", responseRecorder.Code, http.StatusOK, responseRecorder.Body.String())
	}
	var result scheduleToolCancelResult
	if errorValue := json.NewDecoder(responseRecorder.Body).Decode(&result); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(result.Cancelled) != 2 || result.Cancelled[0].ScheduleID != "schedule-1" || result.Cancelled[1].Description != "주간 보고" {
		t.Fatalf("unexpected cancel result: %+v", result)
	}
}

func TestScheduleToolWritesNeverReachAColleaguesSchedule(t *testing.T) {
	repository := &scheduleListRepositoryStub{
		schedules: []task.Schedule{
			ownScheduleFixture("schedule-colleague", "person-박예시", "최견본 주간 보고"),
			ownScheduleFixture("schedule-own", "person-이샘플", "내 일일 점검"),
		},
	}
	handler := scheduleWriteHandler(repository, "person-이샘플")

	for path, body := range map[string]string{
		"/admin/api/schedule/tool-update": `{"scheduleHint":"schedule-colleague","description":"새 이름"}`,
		"/admin/api/schedule/tool-cancel": `{"scheduleHints":["schedule-colleague"]}`,
	} {
		responseRecorder := postScheduleTool(handler, path, body)
		if responseRecorder.Code != http.StatusConflict {
			t.Fatalf("%s status = %d, want %d: %s", path, responseRecorder.Code, http.StatusConflict, responseRecorder.Body.String())
		}
		var conflict scheduleHintConflict
		if errorValue := json.NewDecoder(responseRecorder.Body).Decode(&conflict); errorValue != nil {
			t.Fatal(errorValue)
		}
		if len(conflict.Candidates) != 0 {
			t.Fatalf("%s offered a colleague's schedule as a candidate: %+v", path, conflict.Candidates)
		}
	}

	responseRecorder := postScheduleTool(handler, "/admin/api/schedule/tool-update", `{"scheduleHint":"주간 보고","description":"새 이름"}`)
	if responseRecorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d: %s", responseRecorder.Code, http.StatusConflict, responseRecorder.Body.String())
	}
	if repository.updateRequest.RequesterPersonID != "" || repository.cancelRequest.RequesterPersonID != "" {
		t.Fatalf("a colleague's schedule reached the repository: %+v", repository)
	}
}

func TestScheduleToolResponsesMatchTheirDeclaredOutputSchemas(t *testing.T) {
	for _, responseCase := range []struct {
		path   string
		body   string
		schema json.RawMessage
	}{
		{
			path:   "/admin/api/schedule/tool-create",
			body:   `{"taskInstruction":"주간 보고를 정리한다","description":"주간 보고","kind":"cron","cronExpression":"0 9 * * 1","repeatPolicy":"unbounded","timeZone":"Asia/Seoul","platform":"buzz","conversationID":"channel-1","replyTargetID":"post-1"}`,
			schema: scheduleToolMutationOutputSchema,
		},
		{
			path:   "/admin/api/schedule/tool-update",
			body:   `{"scheduleHint":"주간 보고","intervalSecond":7200,"repeatPolicy":"unbounded"}`,
			schema: scheduleToolMutationOutputSchema,
		},
		{
			path:   "/admin/api/schedule/tool-cancel",
			body:   `{"scheduleHints":["주간 보고"]}`,
			schema: scheduleToolCancelOutputSchema,
		},
	} {
		repository := &scheduleListRepositoryStub{
			schedules: []task.Schedule{ownScheduleFixture("schedule-1", "person-이샘플", "주간 보고")},
		}
		responseRecorder := postScheduleTool(scheduleWriteHandler(repository, "person-이샘플"), responseCase.path, responseCase.body)
		if responseRecorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d: %s", responseCase.path, responseRecorder.Code, http.StatusOK, responseRecorder.Body.String())
		}
		if errorValue := validateAgainstScheduleSchema(responseCase.schema, responseRecorder.Body.Bytes()); errorValue != nil {
			t.Fatalf("%s answered a body its declared output schema rejects: %v", responseCase.path, errorValue)
		}
	}
}
