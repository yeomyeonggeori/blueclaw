package adminapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/sessionquery"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type searchLedgerStub struct{}

func (searchLedgerStub) ListTaskRunByPersonID(personID string) []task.TaskRun {
	if personID != "person-1" {
		return nil
	}
	return []task.TaskRun{{TaskRunID: "run-1", Status: task.TaskStatusCompleted, Prompt: "회의록 보내줘"}}
}

func (searchLedgerStub) ListTaskEvent(string) []task.TaskEvent { return nil }

func TestSearchingWithoutAPersonIsABadRequestRatherThanEveryonesLedger(t *testing.T) {
	handler := TaskSearchHandler{SessionQuery: sessionquery.New(searchLedgerStub{})}
	recorder := httptest.NewRecorder()

	handler.HandleSearchTaskRuns(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/run/search?q=회의록", nil))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("an operator who forgets the person must be told, never quietly served everything: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestSearchingReturnsTheBoundedResultAsJSON(t *testing.T) {
	handler := TaskSearchHandler{SessionQuery: sessionquery.New(searchLedgerStub{})}
	recorder := httptest.NewRecorder()

	handler.HandleSearchTaskRuns(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/run/search?personID=person-1&q=회의록", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected the search to answer: %d %s", recorder.Code, recorder.Body.String())
	}
	result := sessionquery.Result{}
	if errorValue := json.Unmarshal(recorder.Body.Bytes(), &result); errorValue != nil {
		t.Fatalf("the answer has to be readable: %v", errorValue)
	}
	if len(result.Matches) != 1 || result.Matches[0].TaskRunID != "run-1" || result.IsTruncated {
		t.Fatalf("expected one untruncated match, got %+v", result)
	}
}
