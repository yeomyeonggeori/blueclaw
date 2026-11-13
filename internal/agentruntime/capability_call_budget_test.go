package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const answeredDesktopRead = `{"provider":"internkim","selectedBackend":"device","toolName":"desktop_read","outcome":"succeeded","status":"ok","result":{}}`

type deadlineObservingHTTPClient struct {
	requestDocument         map[string]any
	deadline                time.Time
	hasDeadline             bool
	waitsForTheEnd          bool
	answersDeadlineExceeded bool
}

func (httpClient *deadlineObservingHTTPClient) Do(request *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(request.Body)
	_ = json.Unmarshal(body, &httpClient.requestDocument)
	httpClient.deadline, httpClient.hasDeadline = request.Context().Deadline()
	if httpClient.answersDeadlineExceeded {
		return nil, context.DeadlineExceeded
	}
	if httpClient.waitsForTheEnd {
		<-request.Context().Done()
		return nil, request.Context().Err()
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(answeredDesktopRead)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

func invokeDesktopReadWithin(ctx context.Context, httpClient *deadlineObservingHTTPClient, defaultLimit time.Duration, declaredSecond int) (toolcontract.ToolResult, error) {
	descriptor := completeTestCapabilityToolDescriptor(CapabilityToolDescriptor{Name: "desktop_read", TimeoutSecond: declaredSecond})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.capabilityClient = capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient, CallTimeout: defaultLimit}
	return toolCatalogBuilder.invokeCapabilityOperation(ctx, "desktop_read", descriptor, ToolCatalogRequest{}, json.RawMessage(`{}`))
}

func TestACapabilityCallCarriesTheBudgetItsToolDeclares(t *testing.T) {
	httpClient := &deadlineObservingHTTPClient{}
	startedAt := time.Now()

	if _, errorValue := invokeDesktopReadWithin(context.Background(), httpClient, time.Second, 120); errorValue != nil {
		t.Fatal(errorValue)
	}

	if httpClient.requestDocument["timeoutSecond"] != float64(120) {
		t.Errorf("the request carried timeoutSecond %v, and the tool declares 120", httpClient.requestDocument["timeoutSecond"])
	}
	expectedDeadline := startedAt.Add(120*time.Second + capabilityAnswerMargin)
	if !httpClient.hasDeadline || httpClient.deadline.Before(expectedDeadline) || httpClient.deadline.After(expectedDeadline.Add(time.Second)) {
		t.Errorf("the call was bounded at %v (bounded: %t), and the declared budget plus the margin ends at %v", httpClient.deadline, httpClient.hasDeadline, expectedDeadline)
	}
}

func TestACapabilityCallWhoseToolDeclaresNothingCarriesTheDefault(t *testing.T) {
	httpClient := &deadlineObservingHTTPClient{}
	startedAt := time.Now()

	if _, errorValue := invokeDesktopReadWithin(context.Background(), httpClient, 300*time.Second, 0); errorValue != nil {
		t.Fatal(errorValue)
	}

	if httpClient.requestDocument["timeoutSecond"] != float64(300) {
		t.Errorf("the request carried timeoutSecond %v, and the default is 300", httpClient.requestDocument["timeoutSecond"])
	}
	expectedDeadline := startedAt.Add(300*time.Second + capabilityAnswerMargin)
	if !httpClient.hasDeadline || httpClient.deadline.Before(expectedDeadline) || httpClient.deadline.After(expectedDeadline.Add(time.Second)) {
		t.Errorf("the call was bounded at %v (bounded: %t), and the default plus the margin ends at %v", httpClient.deadline, httpClient.hasDeadline, expectedDeadline)
	}
}

func TestACapabilityCallWithNoLimitAnywhereSendsNone(t *testing.T) {
	httpClient := &deadlineObservingHTTPClient{}

	if _, errorValue := invokeDesktopReadWithin(context.Background(), httpClient, 0, 0); errorValue != nil {
		t.Fatal(errorValue)
	}

	if _, isSent := httpClient.requestDocument["timeoutSecond"]; isSent {
		t.Errorf("the request carried timeoutSecond %v with no limit configured", httpClient.requestDocument["timeoutSecond"])
	}
	if httpClient.hasDeadline {
		t.Errorf("the call was bounded at %v with no limit configured", httpClient.deadline)
	}
}

func TestACapabilityCallThatOutlivesItsLimitAnswersTheFact(t *testing.T) {
	httpClient := &deadlineObservingHTTPClient{answersDeadlineExceeded: true}

	result, errorValue := invokeDesktopReadWithin(context.Background(), httpClient, time.Second, 120)

	if errorValue != nil {
		t.Fatalf("a call that outlived its limit surfaced as an error instead of a tool result: %v", errorValue)
	}
	if !result.Failed() || result.Failure.Code != toolcontract.FailureCodes.Timeout.String() {
		t.Fatalf("expected a timeout failure, got %+v", result)
	}
	if summary := result.Failure.UserSafeSummary; !strings.Contains(summary, "125 seconds") || !strings.Contains(summary, "may still have taken effect") {
		t.Errorf("the failure says %q", summary)
	}
	if result.Failure.Retryable {
		t.Error("a call whose work may have taken effect was marked retryable")
	}
}

func TestCancellingTheTaskStillCancelsTheCapabilityCall(t *testing.T) {
	httpClient := &deadlineObservingHTTPClient{waitsForTheEnd: true}
	taskContext, cancelTask := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancelTask)

	result, errorValue := invokeDesktopReadWithin(taskContext, httpClient, 300*time.Second, 120)

	if !errors.Is(errorValue, context.Canceled) {
		t.Fatalf("a cancelled task's call ended with %v and %+v, not the cancellation", errorValue, result)
	}
}
