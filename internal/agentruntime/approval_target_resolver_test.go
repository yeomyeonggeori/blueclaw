package agentruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
)

func targetResolverFixture(responseBody string) (approvalgate.ApprovalTargetResolver, *recordingHTTPClient) {
	httpClient := &recordingHTTPClient{responseBody: responseBody}
	return NewCapabilityApprovalTargetResolver(capability.Client{Endpoint: "http://capability", HTTPClient: httpClient}), httpClient
}

func calendarDeleteTargetRequest() approvalgate.ApprovalTargetRequest {
	return approvalgate.ApprovalTargetRequest{
		ToolName:          "event_delete",
		ToolInput:         json.RawMessage(`{"eventHint":"상하이 edatec 미팅"}`),
		RequesterPersonID: "person-1",
		RequesterEmail:    "member@example.com",
	}
}

func TestATargetIsResolvedOnTheToolsOwnResolutionEndpoint(t *testing.T) {
	resolver, httpClient := targetResolverFixture(`{"provider":"internkim","selectedBackend":"device","toolName":"event_delete","outcome":"succeeded","status":"resolved","result":{"inputField":"eventHint","id":"event-1","title":"상하이 edatec 미팅","startsAt":"2026-08-18T14:00:00+09:00"}}`)

	resolution, errorValue := resolver.ResolveApprovalTarget(context.Background(), calendarDeleteTargetRequest())
	if errorValue != nil {
		t.Fatalf("expected the resolution to answer: %v", errorValue)
	}

	if httpClient.requestPath != "/v1/tools/event_delete/target.resolve" {
		t.Fatalf("target resolution has its own endpoint so it cannot execute anything, got %s", httpClient.requestPath)
	}
	if !strings.Contains(httpClient.requestBody, "member@example.com") {
		t.Fatalf("the same requester resolves the hint that would have run the call, got %s", httpClient.requestBody)
	}
	expectedTarget := approvalgate.ApprovalTarget{InputField: "eventHint", ID: "event-1", Title: "상하이 edatec 미팅", StartsAt: "2026-08-18T14:00:00+09:00"}
	if resolution.Target != expectedTarget {
		t.Fatalf("the question and the approved call are both built from this, got %+v", resolution.Target)
	}
}

func TestAToolThatResolvesNothingAheadComesBackWithNeitherATargetNorAFailure(t *testing.T) {
	resolver, _ := targetResolverFixture(`{"provider":"internkim","selectedBackend":"device","toolName":"event_delete","outcome":"succeeded","status":"no_target","result":{}}`)

	resolution, errorValue := resolver.ResolveApprovalTarget(context.Background(), calendarDeleteTargetRequest())
	if errorValue != nil {
		t.Fatalf("expected the resolution to answer: %v", errorValue)
	}

	if resolution.Target.ID != "" || resolution.Failure.Failure != nil {
		t.Fatalf("nothing to resolve is neither a target nor a failure, got %+v", resolution)
	}
}

func TestAnUnresolvedHintComesBackAsTheFailureTheInvokePathWouldHaveGiven(t *testing.T) {
	resolver, _ := targetResolverFixture(`{"provider":"internkim","selectedBackend":"device","toolName":"event_delete","outcome":"failed","status":"error","isError":true,"message":"no calendar event matched eventHint","errorCode":"calendar_event_hint_unresolved","failureStage":"target_resolution","retryable":true,"safeRetry":true,"result":{"errorCode":"calendar_event_hint_unresolved","candidates":[{"eventID":"event-2","title":"주간 팀 회의"}]}}`)

	resolution, errorValue := resolver.ResolveApprovalTarget(context.Background(), calendarDeleteTargetRequest())
	if errorValue != nil {
		t.Fatalf("expected the resolution to answer: %v", errorValue)
	}

	if resolution.Failure.Failure == nil || resolution.Failure.Failure.Stage != "target_resolution" {
		t.Fatalf("the agent sees the capability's own failure, got %+v", resolution.Failure)
	}
	if !strings.Contains(string(resolution.Failure.Output.Data), "주간 팀 회의") {
		t.Fatalf("the candidates are what let the agent take another route, got %s", resolution.Failure.Output.Data)
	}
	if !resolution.Failure.Failure.SafeRetry {
		t.Fatalf("resolving a hint changes nothing, so retrying it is safe, got %+v", resolution.Failure.Failure)
	}
}

func TestAResolutionAnsweredForAnotherToolIsRefused(t *testing.T) {
	resolver, _ := targetResolverFixture(`{"provider":"internkim","selectedBackend":"device","toolName":"task_delete","outcome":"succeeded","status":"resolved","result":{"inputField":"taskHint","id":"task-1","title":"IR 덱"}}`)

	_, errorValue := resolver.ResolveApprovalTarget(context.Background(), calendarDeleteTargetRequest())

	if errorValue == nil {
		t.Fatal("a target resolved for another tool must never name the call the requester is asked about")
	}
}
