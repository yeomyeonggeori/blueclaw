package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func rawFailureMarkSentWith(t *testing.T, failureNotice agentcontract.FailureNotice) bool {
	t.Helper()
	var sent capabilityReplyRequest
	adapter := NewChatdPlatformAdapter("buzz", capability.Client{
		Endpoint: "http://127.0.0.1:18090",
		HTTPClient: fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
			if errorValue := json.NewDecoder(request.Body).Decode(&sent); errorValue != nil {
				t.Fatalf("expected the reply request to decode: %v", errorValue)
			}
			return jsonCapabilityResponse(http.StatusOK, `{"dispatchID":"dispatch-1"}`), nil
		}},
	})
	if _, errorValue := adapter.SendReply(context.Background(), ReplyTarget{ReplyTargetID: "reply-target-1"}, OutboundReply{Message: failureNotice.Message, FailureNotice: failureNotice}); errorValue != nil {
		t.Fatalf("expected the reply to send: %v", errorValue)
	}
	return sent.IsRawFailure
}

func TestAFailureNoModelCouldWordIsSentMarkedAsTheRawReason(t *testing.T) {
	rawNotice := agentcontract.BuildRawErrorFailureNotice(agentcontract.FailureReport{RawError: "turn router: decisions endpoint answered 502"})
	wordedNotice := agentcontract.BuildFailureNotice("일정을 옮기지 못했어요.", "generated", agentcontract.FailureReport{})

	if !rawFailureMarkSentWith(t, rawNotice) {
		t.Fatal("a raw reason must reach the messenger marked as one, so the screen can frame it")
	}
	if rawFailureMarkSentWith(t, wordedNotice) {
		t.Fatal("a notice a model wrote for the person is not a raw reason")
	}
}
