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

func TestTaskScheduleHandlerReturnsSummary(t *testing.T) {
	nextRunAt := time.Date(2026, 6, 6, 3, 0, 0, 0, time.UTC)
	handler := TaskScheduleHandler{
		SummaryRepository: taskScheduleSummaryRepositoryStub{
			summary: task.TaskScheduleSummary{
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

func TestTaskScheduleHandlerListsActiveSchedules(t *testing.T) {
	nextRunAt := time.Date(2026, 6, 6, 3, 0, 0, 0, time.UTC)
	longPrompt := strings.Repeat("scheduled message ", 40)
	repository := &taskScheduleListRepositoryStub{
		taskSchedules: []task.TaskSchedule{{
			TaskScheduleID:   "schedule-1",
			CreatorPersonID:  "person-1",
			Prompt:           longPrompt,
			ExecutionMode:    task.TaskScheduleExecutionModeAgent,
			Kind:             task.TaskScheduleKindCron,
			CronExpression:   "0 * * * *",
			NextRunAt:        &nextRunAt,
			CreatedAt:        nextRunAt.Add(-time.Hour),
			UpdatedAt:        nextRunAt.Add(-time.Minute),
			ConversationID:   "channel-1",
			ReplyTargetID:    "post-1",
			AgentProfileName: "default",
		}},
	}
	handler := TaskScheduleHandler{ListRepository: repository}
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

func TestTaskScheduleToolListUsesOnlySignedPrincipalAndExactProjection(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	fullInstruction := strings.Repeat("full instruction ", 20)
	repository := &taskScheduleListRepositoryStub{taskSchedules: []task.TaskSchedule{
		{
			TaskScheduleID:  "other-schedule",
			CreatorPersonID: "person-other",
			Prompt:          "must not be selected",
			Kind:            task.TaskScheduleKindOnce,
			NextRunAt:       &nextRunAt,
		},
		{
			TaskScheduleID:  "failed-schedule",
			CreatorPersonID: "person-signed",
			Prompt:          fullInstruction,
			Name:            "Daily report",
			Kind:            task.TaskScheduleKindCron,
			CronExpression:  "0 9 * * *",
			NextRunAt:       &nextRunAt,
			LastError:       "temporary failure",
		},
	}}
	handler := TaskScheduleHandler{
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

func TestTaskScheduleToolListFailsClosedWithoutSignedPrincipal(t *testing.T) {
	for _, handler := range []TaskScheduleHandler{
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

func TestTaskScheduleHandlerCancelsOwnedSchedule(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	repository := &taskScheduleListRepositoryStub{
		taskSchedules: []task.TaskSchedule{{
			TaskScheduleID:  "schedule-1",
			CreatorPersonID: "person-1",
			Kind:            task.TaskScheduleKindInterval,
			IntervalSecond:  3600,
			NextRunAt:       &nextRunAt,
		}},
	}
	handler := TaskScheduleHandler{ListRepository: repository}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/schedule/cancel", strings.NewReader(`{"taskScheduleID":"schedule-1","creatorPersonID":"person-1"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleCancel(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if len(repository.cancelRequest.TaskScheduleIDs) != 1 || repository.cancelRequest.TaskScheduleIDs[0] != "schedule-1" {
		t.Fatalf("expected schedule id cancel request, got %+v", repository.cancelRequest)
	}
	if repository.cancelRequest.RequesterPersonID != "person-1" {
		t.Fatalf("expected requester person, got %+v", repository.cancelRequest)
	}
}

func TestTaskScheduleHandlerDeletesOwnedSchedule(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	repository := &taskScheduleListRepositoryStub{
		taskSchedules: []task.TaskSchedule{{
			TaskScheduleID:  "schedule-1",
			CreatorPersonID: "person-1",
			Kind:            task.TaskScheduleKindInterval,
			IntervalSecond:  3600,
			NextRunAt:       &nextRunAt,
		}},
	}
	handler := TaskScheduleHandler{ListRepository: repository}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/schedule/delete", strings.NewReader(`{"taskScheduleID":"schedule-1","creatorPersonID":"person-1"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleDelete(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if repository.deleteRequest.TaskScheduleID != "schedule-1" || repository.deleteRequest.RequesterPersonID != "person-1" {
		t.Fatalf("expected delete request, got %+v", repository.deleteRequest)
	}
	if len(repository.taskSchedules) != 0 {
		t.Fatalf("expected schedule to be removed, got %+v", repository.taskSchedules)
	}
}

func TestTaskScheduleHandlerRejectsDeleteCreatorMismatch(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	repository := &taskScheduleListRepositoryStub{
		taskSchedules: []task.TaskSchedule{{
			TaskScheduleID:  "schedule-1",
			CreatorPersonID: "person-1",
			Kind:            task.TaskScheduleKindInterval,
			IntervalSecond:  3600,
			NextRunAt:       &nextRunAt,
		}},
	}
	handler := TaskScheduleHandler{ListRepository: repository}
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

func TestTaskScheduleHandlerRejectsCancelCreatorMismatch(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	repository := &taskScheduleListRepositoryStub{
		taskSchedules: []task.TaskSchedule{{
			TaskScheduleID:  "schedule-1",
			CreatorPersonID: "person-1",
			Kind:            task.TaskScheduleKindInterval,
			IntervalSecond:  3600,
			NextRunAt:       &nextRunAt,
		}},
	}
	handler := TaskScheduleHandler{ListRepository: repository}
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

func TestTaskScheduleHandlerReturnsNotFoundForMissingCancelSchedule(t *testing.T) {
	handler := TaskScheduleHandler{ListRepository: &taskScheduleListRepositoryStub{}}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/schedule/cancel", strings.NewReader(`{"taskScheduleID":"missing","creatorPersonID":"person-1"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleCancel(responseRecorder, request)

	if responseRecorder.Code != http.StatusNotFound {
		t.Fatalf("expected not found response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
}

func TestTaskScheduleHandlerUpdatesOwnedSchedule(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	repository := &taskScheduleListRepositoryStub{
		taskSchedules: []task.TaskSchedule{{
			TaskScheduleID:   "schedule-1",
			CreatorPersonID:  "person-1",
			Name:             "Old name",
			ExecutionMode:    task.TaskScheduleExecutionModeAgent,
			AgentProfileName: "default",
			TimeZone:         "Asia/Seoul",
			Kind:             task.TaskScheduleKindInterval,
			IntervalSecond:   3600,
			NextRunAt:        &nextRunAt,
			CreatedAt:        nextRunAt.Add(-time.Hour),
			UpdatedAt:        nextRunAt.Add(-time.Hour),
		}},
	}
	handler := TaskScheduleHandler{ListRepository: repository}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/schedule/update", strings.NewReader(`{"taskScheduleID":"schedule-1","creatorPersonID":"person-1","name":"New name","intervalSecond":7200,"repeatPolicy":"unbounded"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleUpdate(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	var result task.TaskScheduleUpdateResult
	if errorValue := json.NewDecoder(responseRecorder.Body).Decode(&result); errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.TaskSchedule.Name != "New name" || result.TaskSchedule.IntervalSecond != 7200 || result.TaskSchedule.NextRunAt == nil {
		t.Fatalf("expected updated schedule, got %+v", result.TaskSchedule)
	}
	if repository.updateRequest.RequesterPersonID != "person-1" {
		t.Fatalf("expected requester person, got %+v", repository.updateRequest)
	}
}

func TestTaskScheduleHandlerRejectsUpdateCreatorMismatch(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	repository := &taskScheduleListRepositoryStub{
		taskSchedules: []task.TaskSchedule{{
			TaskScheduleID:  "schedule-1",
			CreatorPersonID: "person-1",
			Kind:            task.TaskScheduleKindInterval,
			IntervalSecond:  3600,
			NextRunAt:       &nextRunAt,
		}},
	}
	handler := TaskScheduleHandler{ListRepository: repository}
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

type taskScheduleSummaryRepositoryStub struct {
	summary task.TaskScheduleSummary
}

func (repository taskScheduleSummaryRepositoryStub) SummarizeActiveTaskSchedules(time.Time) (task.TaskScheduleSummary, error) {
	return repository.summary, nil
}

type taskScheduleListRepositoryStub struct {
	request       task.TaskScheduleListRequest
	updateRequest task.TaskScheduleUpdateRequest
	deleteRequest task.TaskScheduleDeleteRequest
	cancelRequest task.TaskScheduleCancelRequest
	taskSchedules []task.TaskSchedule

	upsertedTaskSchedule task.TaskSchedule
}

func (repository *taskScheduleListRepositoryStub) ListTaskSchedules(request task.TaskScheduleListRequest) (task.TaskScheduleListResult, error) {
	repository.request = request
	return task.TaskScheduleListResult{TaskSchedules: repository.taskSchedules, TotalCount: len(repository.taskSchedules), Page: request.Page, PageSize: request.PageSize}, nil
}

func (repository *taskScheduleListRepositoryStub) UpsertTaskSchedule(taskSchedule task.TaskSchedule) error {
	repository.upsertedTaskSchedule = taskSchedule
	repository.taskSchedules = append(repository.taskSchedules, taskSchedule)
	return nil
}

func (repository *taskScheduleListRepositoryStub) UpdateTaskSchedule(request task.TaskScheduleUpdateRequest) (task.TaskScheduleUpdateResult, error) {
	repository.updateRequest = request
	for index, taskSchedule := range repository.taskSchedules {
		if taskSchedule.TaskScheduleID != request.TaskScheduleID || taskSchedule.CreatorPersonID != request.RequesterPersonID || taskSchedule.NextRunAt == nil {
			continue
		}
		updatedTaskSchedule := taskSchedule
		if request.UpdateTaskSchedule != nil {
			var errorValue error
			updatedTaskSchedule, errorValue = request.UpdateTaskSchedule(taskSchedule)
			if errorValue != nil {
				return task.TaskScheduleUpdateResult{}, errorValue
			}
		}
		repository.taskSchedules[index] = updatedTaskSchedule
		return task.TaskScheduleUpdateResult{TaskSchedule: updatedTaskSchedule, IsFound: true}, nil
	}
	return task.TaskScheduleUpdateResult{}, nil
}

func (repository *taskScheduleListRepositoryStub) DeleteTaskSchedule(request task.TaskScheduleDeleteRequest) (task.TaskScheduleDeleteResult, error) {
	repository.deleteRequest = request
	for index, taskSchedule := range repository.taskSchedules {
		if taskSchedule.TaskScheduleID != request.TaskScheduleID || taskSchedule.CreatorPersonID != request.RequesterPersonID {
			continue
		}
		repository.taskSchedules = append(repository.taskSchedules[:index], repository.taskSchedules[index+1:]...)
		return task.TaskScheduleDeleteResult{TaskSchedule: taskSchedule, IsFound: true}, nil
	}
	return task.TaskScheduleDeleteResult{}, nil
}

func (repository *taskScheduleListRepositoryStub) CancelTaskSchedules(request task.TaskScheduleCancelRequest) (task.TaskScheduleCancelResult, error) {
	repository.cancelRequest = request
	cancelledTaskSchedules := []task.TaskSchedule{}
	for index, taskSchedule := range repository.taskSchedules {
		if taskSchedule.CreatorPersonID != request.RequesterPersonID || !containsTaskScheduleID(request.TaskScheduleIDs, taskSchedule.TaskScheduleID) {
			continue
		}
		taskSchedule.NextRunAt = nil
		taskSchedule.ExpiresAt = &request.CancelledAt
		repository.taskSchedules[index] = taskSchedule
		cancelledTaskSchedules = append(cancelledTaskSchedules, taskSchedule)
	}
	return task.TaskScheduleCancelResult{TaskSchedules: cancelledTaskSchedules}, nil
}

func containsTaskScheduleID(taskScheduleIDs []string, taskScheduleID string) bool {
	for _, candidateTaskScheduleID := range taskScheduleIDs {
		if candidateTaskScheduleID == taskScheduleID {
			return true
		}
	}
	return false
}

func scheduleWriteHandler(repository *taskScheduleListRepositoryStub, signedPersonID string) TaskScheduleHandler {
	return TaskScheduleHandler{
		ListRepository: repository,
		ReaderPersonID: func(*http.Request) string { return signedPersonID },
	}
}

func postScheduleTool(handler TaskScheduleHandler, path string, body string) *httptest.ResponseRecorder {
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

func ownScheduleFixture(taskScheduleID string, creatorPersonID string, description string) task.TaskSchedule {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	return task.TaskSchedule{
		TaskScheduleID:   taskScheduleID,
		CreatorPersonID:  creatorPersonID,
		Name:             description,
		Prompt:           "brief " + description,
		ExecutionMode:    task.TaskScheduleExecutionModeAgent,
		AgentProfileName: "default",
		Platform:         "buzz",
		ConversationID:   "channel-1",
		ReplyTargetID:    "post-1",
		TimeZone:         "Asia/Seoul",
		Kind:             task.TaskScheduleKindInterval,
		IntervalSecond:   3600,
		NextRunAt:        &nextRunAt,
	}
}

func TestScheduleToolWritesFailClosedWithoutSignedPrincipal(t *testing.T) {
	repository := &taskScheduleListRepositoryStub{}
	for _, handler := range []TaskScheduleHandler{
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
	if repository.upsertedTaskSchedule.TaskScheduleID != "" || repository.cancelRequest.RequesterPersonID != "" {
		t.Fatalf("an unsigned request reached the repository: %+v", repository)
	}
}

func TestScheduleToolWritesRejectBodySuppliedCreator(t *testing.T) {
	repository := &taskScheduleListRepositoryStub{
		taskSchedules: []task.TaskSchedule{ownScheduleFixture("schedule-1", "person-이샘플", "주간 보고")},
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
	if repository.upsertedTaskSchedule.TaskScheduleID != "" || repository.cancelRequest.RequesterPersonID != "" {
		t.Fatalf("a body-supplied creator reached the repository: %+v", repository)
	}
}

func TestScheduleToolCreateRefusesWithoutDeliveryBinding(t *testing.T) {
	repository := &taskScheduleListRepositoryStub{}
	handler := scheduleWriteHandler(repository, "person-이샘플")

	responseRecorder := postScheduleTool(handler, "/admin/api/schedule/tool-create",
		`{"taskInstruction":"brief me","kind":"once","runAt":"2099-01-01T00:00:00Z"}`)

	if responseRecorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", responseRecorder.Code, http.StatusBadRequest, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), task.ErrScheduleConversationRequired.Error()) {
		t.Fatalf("expected the delivery refusal reason, got %s", responseRecorder.Body.String())
	}
	if repository.upsertedTaskSchedule.TaskScheduleID != "" {
		t.Fatalf("a schedule without delivery binding was stored: %+v", repository.upsertedTaskSchedule)
	}
}

func TestScheduleToolCreateRefusesScheduleWithNoFutureRun(t *testing.T) {
	repository := &taskScheduleListRepositoryStub{}
	handler := scheduleWriteHandler(repository, "person-이샘플")

	responseRecorder := postScheduleTool(handler, "/admin/api/schedule/tool-create",
		`{"taskInstruction":"brief me","kind":"once","runAt":"2020-01-01T00:00:00Z","platform":"buzz","conversationID":"channel-1","replyTargetID":"post-1"}`)

	if responseRecorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", responseRecorder.Code, http.StatusBadRequest, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), task.ErrScheduleNoFutureRun.Error()) {
		t.Fatalf("expected the no-future-run refusal, got %s", responseRecorder.Body.String())
	}
	if repository.upsertedTaskSchedule.TaskScheduleID != "" {
		t.Fatalf("a schedule with no future run was stored: %+v", repository.upsertedTaskSchedule)
	}
}

func TestScheduleToolCreateStoresTheSignedPrincipalsSchedule(t *testing.T) {
	repository := &taskScheduleListRepositoryStub{}
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
	if repository.upsertedTaskSchedule.CreatorPersonID != "person-이샘플" {
		t.Fatalf("the stored creator escaped the signed principal: %+v", repository.upsertedTaskSchedule)
	}
}

func TestScheduleToolUpdateResolvesDescriptionHintAndRewritesTaskInstruction(t *testing.T) {
	repository := &taskScheduleListRepositoryStub{
		taskSchedules: []task.TaskSchedule{ownScheduleFixture("schedule-1", "person-이샘플", "주간 보고")},
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
	repository := &taskScheduleListRepositoryStub{
		taskSchedules: []task.TaskSchedule{
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
	repository := &taskScheduleListRepositoryStub{
		taskSchedules: []task.TaskSchedule{
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
	repository := &taskScheduleListRepositoryStub{
		taskSchedules: []task.TaskSchedule{
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
	repository := &taskScheduleListRepositoryStub{
		taskSchedules: []task.TaskSchedule{
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
		repository := &taskScheduleListRepositoryStub{
			taskSchedules: []task.TaskSchedule{ownScheduleFixture("schedule-1", "person-이샘플", "주간 보고")},
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

type taskRunReaderStub struct {
	taskRuns []task.TaskRun
}

func (reader taskRunReaderStub) FindTaskRun(taskRunID string) (task.TaskRun, bool) {
	for _, taskRun := range reader.taskRuns {
		if taskRun.TaskRunID == taskRunID {
			return taskRun, true
		}
	}
	return task.TaskRun{}, false
}

func scheduleWriteHandlerReadingRuns(repository *taskScheduleListRepositoryStub, signedPersonID string, taskRuns ...task.TaskRun) TaskScheduleHandler {
	handler := scheduleWriteHandler(repository, signedPersonID)
	handler.TaskRunReader = taskRunReaderStub{taskRuns: taskRuns}
	return handler
}

func scheduleCreateBodyForRun(taskRunID string) string {
	return `{"taskRunID":"` + taskRunID + `","taskInstruction":"주간 보고를 정리한다","kind":"once","runAt":"2099-01-01T00:00:00Z","platform":"buzz","conversationID":"channel-1","replyTargetID":"post-1"}`
}

func TestScheduleToolCreateRefusesARunAScheduleStarted(t *testing.T) {
	repository := &taskScheduleListRepositoryStub{}
	handler := scheduleWriteHandlerReadingRuns(repository, "person-이샘플", task.TaskRun{
		TaskRunID:            "run-scheduled",
		RequesterPersonID:    "person-이샘플",
		OriginConversationID: task.ScheduleOriginConversationID("schedule-1"),
	})

	responseRecorder := postScheduleTool(handler, "/admin/api/schedule/tool-create", scheduleCreateBodyForRun("run-scheduled"))

	if responseRecorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d: %s", responseRecorder.Code, http.StatusForbidden, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), errScheduleCreateFromScheduleRun.Error()) {
		t.Fatalf("expected the scheduled-run refusal, got %s", responseRecorder.Body.String())
	}
	if repository.upsertedTaskSchedule.TaskScheduleID != "" {
		t.Fatalf("a scheduled run minted a schedule: %+v", repository.upsertedTaskSchedule)
	}
}

func TestScheduleToolCreateRefusesARunTheRequesterDoesNotOwn(t *testing.T) {
	for _, refusalCase := range []struct {
		name      string
		taskRunID string
		taskRuns  []task.TaskRun
	}{
		{
			name:      "unknown run",
			taskRunID: "run-missing",
			taskRuns:  nil,
		},
		{
			name:      "a colleague's run",
			taskRunID: "run-colleague",
			taskRuns: []task.TaskRun{{
				TaskRunID:            "run-colleague",
				RequesterPersonID:    "person-박예시",
				OriginConversationID: "channel-1",
			}},
		},
	} {
		repository := &taskScheduleListRepositoryStub{}
		handler := scheduleWriteHandlerReadingRuns(repository, "person-이샘플", refusalCase.taskRuns...)

		responseRecorder := postScheduleTool(handler, "/admin/api/schedule/tool-create", scheduleCreateBodyForRun(refusalCase.taskRunID))

		if responseRecorder.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want %d: %s", refusalCase.name, responseRecorder.Code, http.StatusForbidden, responseRecorder.Body.String())
		}
		if !strings.Contains(responseRecorder.Body.String(), errScheduleWriteRunUnknown.Error()) {
			t.Fatalf("%s expected the unknown-run refusal, got %s", refusalCase.name, responseRecorder.Body.String())
		}
		if repository.upsertedTaskSchedule.TaskScheduleID != "" {
			t.Fatalf("%s stored a schedule: %+v", refusalCase.name, repository.upsertedTaskSchedule)
		}
	}
}

func TestScheduleToolCreateAllowsARunNoScheduleStarted(t *testing.T) {
	repository := &taskScheduleListRepositoryStub{}
	handler := scheduleWriteHandlerReadingRuns(repository, "person-이샘플", task.TaskRun{
		TaskRunID:            "run-conversation",
		RequesterPersonID:    "person-이샘플",
		OriginConversationID: "channel-1",
	})

	responseRecorder := postScheduleTool(handler, "/admin/api/schedule/tool-create", scheduleCreateBodyForRun("run-conversation"))

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", responseRecorder.Code, http.StatusOK, responseRecorder.Body.String())
	}
	if repository.upsertedTaskSchedule.CreatorPersonID != "person-이샘플" {
		t.Fatalf("an ordinary run did not store its schedule: %+v", repository.upsertedTaskSchedule)
	}
}

func TestScheduleToolCreateWithoutATaskRunIDStoresTheSchedule(t *testing.T) {
	repository := &taskScheduleListRepositoryStub{}
	handler := scheduleWriteHandlerReadingRuns(repository, "person-이샘플")

	responseRecorder := postScheduleTool(handler, "/admin/api/schedule/tool-create",
		`{"taskInstruction":"주간 보고를 정리한다","kind":"once","runAt":"2099-01-01T00:00:00Z","platform":"buzz","conversationID":"channel-1","replyTargetID":"post-1"}`)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", responseRecorder.Code, http.StatusOK, responseRecorder.Body.String())
	}
	if repository.upsertedTaskSchedule.CreatorPersonID != "person-이샘플" {
		t.Fatalf("a client outside any task did not store its schedule: %+v", repository.upsertedTaskSchedule)
	}
}

func TestScheduleToolCreateSchemaTakesTheRunIDAndRefusesAnAssertedOrigin(t *testing.T) {
	if errorValue := validateAgainstScheduleSchema(scheduleToolCreateInputSchema, []byte(scheduleCreateBodyForRun("run-conversation"))); errorValue != nil {
		t.Fatalf("the declared input schema rejects a real body carrying taskRunID: %v", errorValue)
	}
	assertedOrigin := `{"taskInstruction":"주간 보고를 정리한다","kind":"once","runAt":"2099-01-01T00:00:00Z","platform":"buzz","conversationID":"channel-1","replyTargetID":"post-1","isScheduledRun":false}`
	if validateAgainstScheduleSchema(scheduleToolCreateInputSchema, []byte(assertedOrigin)) == nil {
		t.Fatal("the declared input schema accepts a caller-asserted run origin")
	}
}

const rewrittenTaskInstruction = "주간 보고와 지표를 함께 정리한다"

func scheduleUpdateBodyForRun(taskRunID string) string {
	return `{"taskRunID":"` + taskRunID + `","scheduleHint":"주간","taskInstruction":"` + rewrittenTaskInstruction + `","repeatPolicy":"unbounded"}`
}

func repositoryHoldingOneOwnSchedule() *taskScheduleListRepositoryStub {
	return &taskScheduleListRepositoryStub{
		taskSchedules: []task.TaskSchedule{ownScheduleFixture("schedule-1", "person-이샘플", "주간 보고")},
	}
}

func storedTaskInstruction(repository *taskScheduleListRepositoryStub) string {
	return repository.taskSchedules[0].Prompt
}

func TestScheduleToolUpdateRefusesARunAScheduleStarted(t *testing.T) {
	repository := repositoryHoldingOneOwnSchedule()
	untouchedTaskInstruction := storedTaskInstruction(repository)
	handler := scheduleWriteHandlerReadingRuns(repository, "person-이샘플", task.TaskRun{
		TaskRunID:            "run-scheduled",
		RequesterPersonID:    "person-이샘플",
		OriginConversationID: task.ScheduleOriginConversationID("schedule-2"),
	})

	responseRecorder := postScheduleTool(handler, "/admin/api/schedule/tool-update", scheduleUpdateBodyForRun("run-scheduled"))

	if responseRecorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d: %s", responseRecorder.Code, http.StatusForbidden, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), errScheduleUpdateFromScheduleRun.Error()) {
		t.Fatalf("expected the scheduled-run refusal, got %s", responseRecorder.Body.String())
	}
	if repository.updateRequest.RequesterPersonID != "" {
		t.Fatalf("a scheduled run reached the repository: %+v", repository.updateRequest)
	}
	if storedTaskInstruction(repository) != untouchedTaskInstruction {
		t.Fatalf("a scheduled run changed a schedule: %q", storedTaskInstruction(repository))
	}
}

func TestScheduleToolUpdateRefusesARunTheRequesterDoesNotOwn(t *testing.T) {
	for _, refusalCase := range []struct {
		name      string
		taskRunID string
		taskRuns  []task.TaskRun
	}{
		{
			name:      "unknown run",
			taskRunID: "run-missing",
			taskRuns:  nil,
		},
		{
			name:      "a colleague's run",
			taskRunID: "run-colleague",
			taskRuns: []task.TaskRun{{
				TaskRunID:            "run-colleague",
				RequesterPersonID:    "person-박예시",
				OriginConversationID: "channel-1",
			}},
		},
	} {
		repository := repositoryHoldingOneOwnSchedule()
		untouchedTaskInstruction := storedTaskInstruction(repository)
		handler := scheduleWriteHandlerReadingRuns(repository, "person-이샘플", refusalCase.taskRuns...)

		responseRecorder := postScheduleTool(handler, "/admin/api/schedule/tool-update", scheduleUpdateBodyForRun(refusalCase.taskRunID))

		if responseRecorder.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want %d: %s", refusalCase.name, responseRecorder.Code, http.StatusForbidden, responseRecorder.Body.String())
		}
		if !strings.Contains(responseRecorder.Body.String(), errScheduleWriteRunUnknown.Error()) {
			t.Fatalf("%s expected the unknown-run refusal, got %s", refusalCase.name, responseRecorder.Body.String())
		}
		if storedTaskInstruction(repository) != untouchedTaskInstruction {
			t.Fatalf("%s changed a schedule: %q", refusalCase.name, storedTaskInstruction(repository))
		}
	}
}

func TestScheduleToolUpdateAllowsARunNoScheduleStarted(t *testing.T) {
	repository := repositoryHoldingOneOwnSchedule()
	handler := scheduleWriteHandlerReadingRuns(repository, "person-이샘플", task.TaskRun{
		TaskRunID:            "run-conversation",
		RequesterPersonID:    "person-이샘플",
		OriginConversationID: "channel-1",
	})

	responseRecorder := postScheduleTool(handler, "/admin/api/schedule/tool-update", scheduleUpdateBodyForRun("run-conversation"))

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", responseRecorder.Code, http.StatusOK, responseRecorder.Body.String())
	}
	if storedTaskInstruction(repository) != rewrittenTaskInstruction {
		t.Fatalf("an ordinary run did not change its schedule: %q", storedTaskInstruction(repository))
	}
}

func TestScheduleToolUpdateWithoutATaskRunIDChangesTheSchedule(t *testing.T) {
	repository := repositoryHoldingOneOwnSchedule()
	handler := scheduleWriteHandlerReadingRuns(repository, "person-이샘플")

	responseRecorder := postScheduleTool(handler, "/admin/api/schedule/tool-update",
		`{"scheduleHint":"주간","taskInstruction":"`+rewrittenTaskInstruction+`","repeatPolicy":"unbounded"}`)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", responseRecorder.Code, http.StatusOK, responseRecorder.Body.String())
	}
	if storedTaskInstruction(repository) != rewrittenTaskInstruction {
		t.Fatalf("a client outside any task did not change its schedule: %q", storedTaskInstruction(repository))
	}
}

func TestScheduleToolUpdateSchemaTakesTheRunIDAndRefusesAnAssertedOrigin(t *testing.T) {
	if errorValue := validateAgainstScheduleSchema(scheduleToolUpdateInputSchema, []byte(scheduleUpdateBodyForRun("run-conversation"))); errorValue != nil {
		t.Fatalf("the declared input schema rejects a real body carrying taskRunID: %v", errorValue)
	}
	assertedOrigin := `{"scheduleHint":"주간","taskInstruction":"` + rewrittenTaskInstruction + `","repeatPolicy":"unbounded","isScheduledRun":false}`
	if validateAgainstScheduleSchema(scheduleToolUpdateInputSchema, []byte(assertedOrigin)) == nil {
		t.Fatal("the declared input schema accepts a caller-asserted run origin")
	}
}

func TestScheduleToolCancelSchemaTakesNoTaskRun(t *testing.T) {
	if validateAgainstScheduleSchema(scheduleToolCancelInputSchema, []byte(`{"scheduleHints":["주간"],"taskRunID":"run-conversation"}`)) == nil {
		t.Fatal("the declared cancel schema accepts a task run it never reads")
	}
}
