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
}

func (repository *taskScheduleListRepositoryStub) ListTaskSchedules(request task.TaskScheduleListRequest) (task.TaskScheduleListResult, error) {
	repository.request = request
	return task.TaskScheduleListResult{TaskSchedules: repository.taskSchedules, TotalCount: len(repository.taskSchedules), Page: request.Page, PageSize: request.PageSize}, nil
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
