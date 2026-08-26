package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientListTaskRunsDecodesBareArray(testInstance *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/admin/api/run" {
			testInstance.Fatalf("unexpected path %s", request.URL.Path)
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		json.NewEncoder(responseWriter).Encode([]TaskRun{
			{TaskRunID: "task-1", Status: TaskStatusRunning, Prompt: "summarize report"},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	taskRuns, errorValue := client.ListTaskRuns(context.Background())
	if errorValue != nil {
		testInstance.Fatalf("unexpected error: %v", errorValue)
	}
	if len(taskRuns) != 1 || taskRuns[0].TaskRunID != "task-1" {
		testInstance.Fatalf("unexpected task runs: %+v", taskRuns)
	}
}

func TestClientListTaskRunsReturnsConnectionErrorWhenUnreachable(testInstance *testing.T) {
	client := NewClient("http://127.0.0.1:1", nil)
	_, errorValue := client.ListTaskRuns(context.Background())
	if errorValue == nil {
		testInstance.Fatal("expected a connection error")
	}
	var connectionError ConnectionError
	if !isConnectionError(errorValue, &connectionError) {
		testInstance.Fatalf("expected ConnectionError, got %T: %v", errorValue, errorValue)
	}
}

func isConnectionError(errorValue error, target *ConnectionError) bool {
	connectionError, isConnectionError := errorValue.(ConnectionError)
	if isConnectionError {
		*target = connectionError
	}
	return isConnectionError
}

func TestClientGetTaskRunDetailDecodesTaskRunAndEvents(testInstance *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("taskRunID") != "task-1" {
			testInstance.Fatalf("unexpected taskRunID query %s", request.URL.RawQuery)
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		json.NewEncoder(responseWriter).Encode(TaskRunDetail{
			TaskRun:    TaskRun{TaskRunID: "task-1", Status: TaskStatusWaitingApproval},
			TaskEvents: []TaskEvent{{TaskEventID: "event-1", TaskRunID: "task-1", Name: "approval.pending_call"}},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	detail, errorValue := client.GetTaskRunDetail(context.Background(), "task-1")
	if errorValue != nil {
		testInstance.Fatalf("unexpected error: %v", errorValue)
	}
	if detail.TaskRun.TaskRunID != "task-1" {
		testInstance.Fatalf("unexpected task run: %+v", detail.TaskRun)
	}
	if len(detail.TaskEvents) != 1 || detail.TaskEvents[0].Name != "approval.pending_call" {
		testInstance.Fatalf("unexpected task events: %+v", detail.TaskEvents)
	}
}

func TestClientGetTaskRunDetailReturnsApplicationErrorOnNotFound(testInstance *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		http.Error(responseWriter, "task run not found", http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	_, errorValue := client.GetTaskRunDetail(context.Background(), "missing")
	applicationError, isApplicationError := errorValue.(ApplicationError)
	if !isApplicationError {
		testInstance.Fatalf("expected ApplicationError, got %T: %v", errorValue, errorValue)
	}
	if applicationError.StatusCode != http.StatusNotFound {
		testInstance.Fatalf("unexpected status code: %d", applicationError.StatusCode)
	}
}

func TestClientSubmitApprovalSendsDecisionAndDecodesStatus(testInstance *testing.T) {
	var capturedRequestBody approvalRequestBody
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/admin/api/run/approve" || request.Method != http.MethodPost {
			testInstance.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		if errorValue := json.NewDecoder(request.Body).Decode(&capturedRequestBody); errorValue != nil {
			testInstance.Fatalf("decode request body: %v", errorValue)
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		json.NewEncoder(responseWriter).Encode(ApprovalResult{TaskRunID: capturedRequestBody.TaskRunID, Status: TaskStatusRunning})
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	approvalResult, errorValue := client.SubmitApproval(context.Background(), "task-1", ApprovalDecisionConfirm)
	if errorValue != nil {
		testInstance.Fatalf("unexpected error: %v", errorValue)
	}
	if capturedRequestBody.TaskRunID != "task-1" || capturedRequestBody.Decision != ApprovalDecisionConfirm {
		testInstance.Fatalf("unexpected captured request: %+v", capturedRequestBody)
	}
	if approvalResult.Status != TaskStatusRunning {
		testInstance.Fatalf("unexpected approval result: %+v", approvalResult)
	}
}

func TestClientSubmitApprovalReturnsApplicationErrorOnRefusal(testInstance *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		responseWriter.WriteHeader(http.StatusConflict)
		json.NewEncoder(responseWriter).Encode(map[string]string{"error": "task run is not waiting for approval"})
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	_, errorValue := client.SubmitApproval(context.Background(), "task-1", ApprovalDecisionCancel)
	applicationError, isApplicationError := errorValue.(ApplicationError)
	if !isApplicationError {
		testInstance.Fatalf("expected ApplicationError, got %T: %v", errorValue, errorValue)
	}
	if applicationError.Message != "task run is not waiting for approval" {
		testInstance.Fatalf("unexpected message: %s", applicationError.Message)
	}
}
