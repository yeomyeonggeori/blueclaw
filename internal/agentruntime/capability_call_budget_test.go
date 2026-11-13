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
	requestDocument map[string]any
	deadline        time.Time
	hasDeadline     bool
	waitsForTheEnd  bool
}

func (httpClient *deadlineObservingHTTPClient) Do(request *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(request.Body)
	_ = json.Unmarshal(body, &httpClient.requestDocument)
	httpClient.deadline, httpClient.hasDeadline = request.Context().Deadline()
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

func desktopReadProvider(httpClient *deadlineObservingHTTPClient, transportBound time.Duration) capabilityToolProvider {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.capabilityClient = capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient, CallTimeout: transportBound}
	return capabilityToolProvider{toolCatalogBuilder: toolCatalogBuilder}
}

func desktopReadDeclaring(timeoutSecond int) CapabilityToolDescriptor {
	return completeTestCapabilityToolDescriptor(CapabilityToolDescriptor{Name: "desktop_read", TimeoutSecond: timeoutSecond})
}

func invokeBoundTool(ctx context.Context, boundTool toolcontract.BoundTool) (toolcontract.ToolResult, error) {
	toolSet := toolcontract.NewToolSet([]string{boundTool.Definition.Name})
	if errorValue := toolSet.RegisterBoundTool(boundTool); errorValue != nil {
		panic(errorValue)
	}
	return toolSet.Invoke(ctx, toolcontract.ToolInvocation{ToolName: boundTool.Definition.Name, Input: json.RawMessage(`{}`)})
}

func expectDeadlineNear(t *testing.T, httpClient *deadlineObservingHTTPClient, expected time.Time) {
	t.Helper()
	if !httpClient.hasDeadline || httpClient.deadline.Before(expected.Add(-time.Second)) || httpClient.deadline.After(expected.Add(time.Second)) {
		t.Errorf("the call was bounded at %v (bounded: %t), expected about %v", httpClient.deadline, httpClient.hasDeadline, expected)
	}
}

func TestADeclaredBudgetBoundsTheToolThroughTheRegistry(t *testing.T) {
	boundTool := desktopReadProvider(&deadlineObservingHTTPClient{}, 30*time.Second).boundTool(desktopReadDeclaring(120))

	if boundTool.Definition.TimeoutMS != 125000 {
		t.Fatalf("a tool declaring 120s is bounded at %dms; the registry should stop it 5s after capabilityd does", boundTool.Definition.TimeoutMS)
	}
}

func TestAToolWithNoBudgetLeavesTheRegistryUnbounded(t *testing.T) {
	boundTool := desktopReadProvider(&deadlineObservingHTTPClient{}, 30*time.Second).boundTool(desktopReadDeclaring(0))

	if boundTool.Definition.TimeoutMS != 0 {
		t.Fatalf("a tool declaring nothing is bounded at %dms", boundTool.Definition.TimeoutMS)
	}
}

func TestADeclaredBudgetTravelsWithTheCallAndOutlastsTheTransportBound(t *testing.T) {
	httpClient := &deadlineObservingHTTPClient{}
	startedAt := time.Now()

	if _, errorValue := invokeBoundTool(context.Background(), desktopReadProvider(httpClient, 30*time.Second).boundTool(desktopReadDeclaring(120))); errorValue != nil {
		t.Fatal(errorValue)
	}

	if httpClient.requestDocument["timeoutSecond"] != float64(120) {
		t.Errorf("the request carried timeoutSecond %v, and the tool declares 120", httpClient.requestDocument["timeoutSecond"])
	}
	expectDeadlineNear(t, httpClient, startedAt.Add(125*time.Second))
}

func TestAToolWithNoBudgetIsBoundedByTheTransportAsBefore(t *testing.T) {
	httpClient := &deadlineObservingHTTPClient{}
	startedAt := time.Now()

	if _, errorValue := invokeBoundTool(context.Background(), desktopReadProvider(httpClient, 30*time.Second).boundTool(desktopReadDeclaring(0))); errorValue != nil {
		t.Fatal(errorValue)
	}

	if _, isSent := httpClient.requestDocument["timeoutSecond"]; isSent {
		t.Errorf("the request carried timeoutSecond %v for a tool declaring none", httpClient.requestDocument["timeoutSecond"])
	}
	expectDeadlineNear(t, httpClient, startedAt.Add(30*time.Second))
}

func TestTheRegistryDeadlineReachesTheCapabilityCall(t *testing.T) {
	httpClient := &deadlineObservingHTTPClient{waitsForTheEnd: true}
	boundTool := desktopReadProvider(httpClient, 30*time.Second).boundTool(desktopReadDeclaring(120))
	boundTool.Definition.TimeoutMS = 50
	startedAt := time.Now()

	result, errorValue := invokeBoundTool(context.Background(), boundTool)

	if errorValue != nil || !result.Failed() || result.Failure.Code != toolcontract.FailureCodes.Timeout.String() {
		t.Fatalf("expected the registry's budget failure, got %+v, %v", result, errorValue)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("the call outlived the registry's 50ms budget by %s", elapsed)
	}
}

func TestCancellingTheTaskStillCancelsTheCapabilityCall(t *testing.T) {
	httpClient := &deadlineObservingHTTPClient{waitsForTheEnd: true}
	taskContext, cancelTask := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancelTask)

	result, errorValue := invokeBoundTool(taskContext, desktopReadProvider(httpClient, 30*time.Second).boundTool(desktopReadDeclaring(120)))

	if !errors.Is(errorValue, context.Canceled) {
		t.Fatalf("a cancelled task's call ended with %v and %+v, not the cancellation", errorValue, result)
	}
}

func TestACallOutsideTheRegistryIsBoundedByTheSameBudget(t *testing.T) {
	httpClient := &deadlineObservingHTTPClient{}
	provider := desktopReadProvider(httpClient, 30*time.Second)
	startedAt := time.Now()

	if _, errorValue := provider.toolCatalogBuilder.invokeCapabilityOperation(context.Background(), "desktop_read", desktopReadDeclaring(120), ToolCatalogRequest{}, json.RawMessage(`{}`)); errorValue != nil {
		t.Fatal(errorValue)
	}

	expectDeadlineNear(t, httpClient, startedAt.Add(125*time.Second))
}
