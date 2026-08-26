package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	TaskStatusWaitingApproval = "waiting_approval"
	TaskStatusRunning         = "running"
	TaskStatusPlanned         = "planned"
	TaskStatusWaitingInput    = "waiting_user_input"
	TaskStatusBlocked         = "blocked"
	TaskStatusInterrupted     = "interrupted"
	TaskStatusCompleted       = "completed"
	TaskStatusFailed          = "failed"
	TaskStatusCancelled       = "cancelled"
)

const (
	ApprovalDecisionConfirm     = "confirm"
	ApprovalDecisionConfirmTask = "confirm_task"
	ApprovalDecisionCancel      = "cancel"
)

type TaskRun struct {
	TaskRunID               string    `json:"taskRunID"`
	RequesterPersonID       string    `json:"requesterPersonID"`
	RequesterDisplayName    string    `json:"requesterDisplayName,omitempty"`
	OriginConversationID    string    `json:"originConversationID"`
	CurrentAgentProfileName string    `json:"currentAgentProfileName"`
	Status                  string    `json:"status"`
	Prompt                  string    `json:"prompt"`
	Result                  string    `json:"result"`
	FailureReason           string    `json:"failureReason"`
	CreatedAt               time.Time `json:"createdAt"`
	UpdatedAt               time.Time `json:"updatedAt"`
	LLMCostUSD              float64   `json:"llmCostUSD,omitempty"`
	LLMCallCount            int       `json:"llmCallCount,omitempty"`
}

type TaskEvent struct {
	TaskEventID string    `json:"taskEventID"`
	TaskRunID   string    `json:"taskRunID"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	CreatedAt   time.Time `json:"createdAt"`
}

type TaskRunDetail struct {
	TaskRun    TaskRun     `json:"taskRun"`
	TaskEvents []TaskEvent `json:"taskEvents"`
}

type ApprovalResult struct {
	TaskRunID string `json:"taskRunID"`
	Status    string `json:"status"`
}

type approvalRequestBody struct {
	TaskRunID string `json:"taskRunID"`
	Decision  string `json:"decision"`
}

type applicationErrorBody struct {
	Error string `json:"error"`
}

// Client is a typed HTTP client over blueclaw's admin task API. It performs
// no terminal I/O and is safe to exercise against an httptest.Server.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

func (client *Client) BaseURL() string {
	return client.baseURL
}

func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), httpClient: httpClient}
}

func (client *Client) ListTaskRuns(ctx context.Context) ([]TaskRun, error) {
	requestURL := client.baseURL + "/admin/api/run"
	var taskRuns []TaskRun
	if errorValue := client.getJSON(ctx, requestURL, &taskRuns); errorValue != nil {
		return nil, errorValue
	}
	return taskRuns, nil
}

func (client *Client) GetTaskRunDetail(ctx context.Context, taskRunID string) (TaskRunDetail, error) {
	requestURL := client.baseURL + "/admin/api/run/detail?taskRunID=" + url.QueryEscape(taskRunID)
	var detail TaskRunDetail
	if errorValue := client.getJSON(ctx, requestURL, &detail); errorValue != nil {
		return TaskRunDetail{}, errorValue
	}
	return detail, nil
}

func (client *Client) SubmitApproval(ctx context.Context, taskRunID string, decision string) (ApprovalResult, error) {
	requestBody, errorValue := json.Marshal(approvalRequestBody{TaskRunID: taskRunID, Decision: decision})
	if errorValue != nil {
		return ApprovalResult{}, fmt.Errorf("encode approval request: %w", errorValue)
	}
	requestURL := client.baseURL + "/admin/api/run/approve"
	httpRequest, errorValue := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(requestBody))
	if errorValue != nil {
		return ApprovalResult{}, fmt.Errorf("build approval request: %w", errorValue)
	}
	httpRequest.Header.Set("Content-Type", "application/json")

	httpResponse, errorValue := client.httpClient.Do(httpRequest)
	if errorValue != nil {
		return ApprovalResult{}, ConnectionError{BaseURL: client.baseURL, Cause: errorValue}
	}
	defer httpResponse.Body.Close()

	responseBody, errorValue := io.ReadAll(httpResponse.Body)
	if errorValue != nil {
		return ApprovalResult{}, fmt.Errorf("read approval response: %w", errorValue)
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return ApprovalResult{}, ApplicationError{StatusCode: httpResponse.StatusCode, Message: applicationErrorMessage(responseBody)}
	}

	var approvalResult ApprovalResult
	if errorValue := json.Unmarshal(responseBody, &approvalResult); errorValue != nil {
		return ApprovalResult{}, fmt.Errorf("decode approval response: %w", errorValue)
	}
	return approvalResult, nil
}

func (client *Client) getJSON(ctx context.Context, requestURL string, destination any) error {
	httpRequest, errorValue := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if errorValue != nil {
		return fmt.Errorf("build request: %w", errorValue)
	}

	httpResponse, errorValue := client.httpClient.Do(httpRequest)
	if errorValue != nil {
		return ConnectionError{BaseURL: client.baseURL, Cause: errorValue}
	}
	defer httpResponse.Body.Close()

	responseBody, errorValue := io.ReadAll(httpResponse.Body)
	if errorValue != nil {
		return fmt.Errorf("read response: %w", errorValue)
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return ApplicationError{StatusCode: httpResponse.StatusCode, Message: applicationErrorMessage(responseBody)}
	}
	if errorValue := json.Unmarshal(responseBody, destination); errorValue != nil {
		return fmt.Errorf("decode response: %w", errorValue)
	}
	return nil
}

func applicationErrorMessage(responseBody []byte) string {
	var errorBody applicationErrorBody
	if errorValue := json.Unmarshal(responseBody, &errorBody); errorValue == nil && errorBody.Error != "" {
		return errorBody.Error
	}
	trimmedBody := strings.TrimSpace(string(responseBody))
	if trimmedBody != "" {
		return trimmedBody
	}
	return "request failed"
}

// ConnectionError indicates the admin API could not be reached at all, as
// opposed to reaching it and receiving an application-level error.
type ConnectionError struct {
	BaseURL string
	Cause   error
}

func (connectionError ConnectionError) Error() string {
	return fmt.Sprintf("cannot reach blueclaw admin API at %s: %s", connectionError.BaseURL, connectionError.Cause)
}

func (connectionError ConnectionError) Unwrap() error {
	return connectionError.Cause
}

// ApplicationError indicates the admin API responded but rejected the
// request.
type ApplicationError struct {
	StatusCode int
	Message    string
}

func (applicationError ApplicationError) Error() string {
	return fmt.Sprintf("admin API returned %d: %s", applicationError.StatusCode, applicationError.Message)
}

type HarnessStatus struct {
	Name                    string `json:"name"`
	AgentCommandPath        string `json:"agentCommandPath,omitempty"`
	RunsAsRequesterIdentity bool   `json:"runsAsRequesterIdentity"`
	ToolCatalogURL          string `json:"toolCatalogURL,omitempty"`
}

func (client *Client) GetHarnessStatus(ctx context.Context) (HarnessStatus, error) {
	var harnessStatus HarnessStatus
	if errorValue := client.getJSON(ctx, client.baseURL+"/admin/api/harness", &harnessStatus); errorValue != nil {
		return HarnessStatus{}, errorValue
	}
	return harnessStatus, nil
}
