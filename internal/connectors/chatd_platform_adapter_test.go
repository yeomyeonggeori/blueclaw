package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestChatdPlatformAdapterParsesNormalizedHTTPEvent(t *testing.T) {
	adapter := NewChatdPlatformAdapter("mattermost", capability.Client{})
	request := httptest.NewRequest(
		http.MethodPost,
		"/connectors/mattermost/events",
		bytes.NewReader([]byte(`{"conversationID":"channel-1","messageID":"post-1","senderID":"user-1","replyTargetID":"reply-target-1","prompt":"hello","context":{"messages":[{"speaker":"admin","text":"previous"}],"hasMoreBefore":true,"historyCursor":"cursor-1"}}`)),
	)

	parseResult, errorValue := adapter.ParseHTTPEvent(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected normalized event to parse: %v", errorValue)
	}
	if !parseResult.HasEvent {
		t.Fatal("expected parsed event")
	}
	if parseResult.Event.Platform != "mattermost" {
		t.Fatalf("expected platform to be set by adapter, got %q", parseResult.Event.Platform)
	}
	if parseResult.Event.MessageID != "post-1" {
		t.Fatalf("expected message id, got %q", parseResult.Event.MessageID)
	}
	if !parseResult.Event.Context.HasMoreBefore || parseResult.Event.Context.HistoryCursor != "cursor-1" {
		t.Fatalf("expected history metadata, got %+v", parseResult.Event.Context)
	}
}

func TestChatdPlatformAdapterImportsInputAttachments(t *testing.T) {
	var receivedPath string
	var receivedBody InputAttachmentImportRequest
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		receivedPath = request.URL.Path
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedBody); errorValue != nil {
			t.Fatalf("expected request body to decode: %v", errorValue)
		}
		return jsonCapabilityResponse(http.StatusOK, `{"inputParts":[{"type":"file","file":{"path":"/workspace/private/people/person-1/inbox/mattermost/post-1/report.pdf","filename":"report.pdf","markdownPreview":"# Report"}}]}`), nil
	}}
	adapter := NewChatdPlatformAdapter("mattermost", capability.Client{
		Endpoint:   "http://chatd.test",
		HTTPClient: httpClient,
	})

	result, errorValue := adapter.ImportInputAttachments(context.Background(), InputAttachmentImportRequest{
		MessageID:           "post-1",
		TargetDirectoryPath: "/workspace/private/people/person-1/inbox/mattermost/post-1",
		InputAttachments:    []InputAttachment{{Platform: "mattermost", FileID: "file-1"}},
	})
	if errorValue != nil {
		t.Fatalf("expected attachment import to succeed: %v", errorValue)
	}
	if receivedPath != "/v1/platform/mattermost/attachments.import" || receivedBody.MessageID != "post-1" {
		t.Fatalf("unexpected import request path=%q body=%+v", receivedPath, receivedBody)
	}
	if len(result.InputParts) != 1 || result.InputParts[0].Type != agentcontract.AgentPartTypeFile {
		t.Fatalf("expected imported file part, got %+v", result.InputParts)
	}
}

func TestChatdPlatformAdapterAddsAndRemovesReaction(t *testing.T) {
	var receivedPaths []string
	var receivedBody capabilityReactionRequest
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		receivedPaths = append(receivedPaths, request.URL.Path)
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedBody); errorValue != nil {
			t.Fatalf("expected request body to decode: %v", errorValue)
		}
		return jsonCapabilityResponse(http.StatusOK, `{}`), nil
	}}
	adapter := NewChatdPlatformAdapter("mattermost", capability.Client{
		Endpoint:   "http://chatd.test",
		HTTPClient: httpClient,
	})
	target := ReactionTarget{
		ConversationID: "channel-1",
		MessageID:      "post-1",
		EmojiName:      "white_check_mark",
		Reason:         "consume",
	}

	if errorValue := adapter.AddReaction(context.Background(), target); errorValue != nil {
		t.Fatalf("expected reaction add to succeed: %v", errorValue)
	}
	if errorValue := adapter.RemoveReaction(context.Background(), target); errorValue != nil {
		t.Fatalf("expected reaction remove to succeed: %v", errorValue)
	}

	if len(receivedPaths) != 2 || receivedPaths[0] != "/v1/platform/mattermost/reaction.add" || receivedPaths[1] != "/v1/platform/mattermost/reaction.remove" {
		t.Fatalf("unexpected reaction paths %+v", receivedPaths)
	}
	if receivedBody.ConversationID != "channel-1" || receivedBody.MessageID != "post-1" || receivedBody.EmojiName != "white_check_mark" || receivedBody.Reason != "consume" {
		t.Fatalf("unexpected reaction body %+v", receivedBody)
	}
}

func TestChatdPlatformAdapterUsesChatdEndpointsWithoutAuthorization(t *testing.T) {
	receivedAuthorizationByPath := map[string]string{}
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		receivedAuthorizationByPath[request.URL.Path] = request.Header.Get("Authorization")
		switch request.URL.Path {
		case "/v1/platform/mattermost/identity.resolve":
			var requestDocument capabilityIdentityRequest
			errorValue := json.NewDecoder(request.Body).Decode(&requestDocument)
			if errorValue != nil {
				t.Fatalf("expected identity request to decode: %v", errorValue)
			}
			if requestDocument.SenderID != "user-1" {
				t.Fatalf("expected sender id, got %q", requestDocument.SenderID)
			}
			return jsonCapabilityResponse(http.StatusOK, `{"email":"lee@example.com","displayName":"Lee"}`), nil
		case "/v1/platform/mattermost/progress.start", "/v1/platform/mattermost/progress.stop":
			var requestDocument capabilityProgressRequest
			errorValue := json.NewDecoder(request.Body).Decode(&requestDocument)
			if errorValue != nil {
				t.Fatalf("expected progress request to decode: %v", errorValue)
			}
			if requestDocument.ReplyTargetID != "reply-target-1" {
				t.Fatalf("expected progress reply target id, got %q", requestDocument.ReplyTargetID)
			}
			return jsonCapabilityResponse(http.StatusOK, `{}`), nil
		case "/v1/platform/mattermost/reply.send":
			var requestDocument capabilityReplyRequest
			errorValue := json.NewDecoder(request.Body).Decode(&requestDocument)
			if errorValue != nil {
				t.Fatalf("expected reply request to decode: %v", errorValue)
			}
			if requestDocument.Message != "hello" {
				t.Fatalf("expected reply message, got %q", requestDocument.Message)
			}
			if requestDocument.ReplyTargetID != "reply-target-1" {
				t.Fatalf("expected reply target id, got %q", requestDocument.ReplyTargetID)
			}
			return jsonCapabilityResponse(http.StatusOK, `{"dispatchID":"dispatch-1"}`), nil
		case "/v1/platform/mattermost/history.fetch":
			var requestDocument capabilityHistoryRequest
			errorValue := json.NewDecoder(request.Body).Decode(&requestDocument)
			if errorValue != nil {
				t.Fatalf("expected history request to decode: %v", errorValue)
			}
			if requestDocument.HistoryCursor != "cursor-1" {
				t.Fatalf("expected history cursor, got %q", requestDocument.HistoryCursor)
			}
			if requestDocument.Direction != "before" {
				t.Fatalf("expected history direction before, got %q", requestDocument.Direction)
			}
			return jsonCapabilityResponse(http.StatusOK, `{"messages":[{"speaker":"admin","text":"older"}],"hasMoreBefore":false}`), nil
		default:
			t.Fatalf("unexpected chatd path %q", request.URL.Path)
		}
		return nil, nil
	}}

	adapter := NewChatdPlatformAdapter("mattermost", capability.Client{
		Endpoint:   "http://127.0.0.1:18090",
		HTTPClient: httpClient,
	})
	replyTarget := ReplyTarget{ConversationID: "channel-1", ReplyTargetID: "reply-target-1", DedupeKey: "mattermost:channel-1:m1"}

	platformIdentity, errorValue := adapter.ResolveIdentity(context.Background(), "user-1")
	if errorValue != nil {
		t.Fatalf("expected identity resolution: %v", errorValue)
	}
	if errorValue := adapter.StartProgress(context.Background(), replyTarget); errorValue != nil {
		t.Fatalf("expected progress start: %v", errorValue)
	}
	if errorValue := adapter.StopProgress(context.Background(), replyTarget); errorValue != nil {
		t.Fatalf("expected progress stop: %v", errorValue)
	}
	dispatchID, errorValue := adapter.SendReply(context.Background(), replyTarget, OutboundReply{Message: "hello"})
	if errorValue != nil {
		t.Fatalf("expected reply send: %v", errorValue)
	}
	visibleContext, errorValue := adapter.FetchHistory(context.Background(), "cursor-1", 20)
	if errorValue != nil {
		t.Fatalf("expected history fetch: %v", errorValue)
	}

	if platformIdentity.Platform != "mattermost" || platformIdentity.ExternalUserID != "user-1" {
		t.Fatalf("expected adapter to stamp identity source, got %+v", platformIdentity)
	}
	if dispatchID != "dispatch-1" {
		t.Fatalf("expected dispatch id, got %q", dispatchID)
	}
	if len(visibleContext.Messages) != 1 || visibleContext.Messages[0].Text != "older" {
		t.Fatalf("expected history context, got %+v", visibleContext)
	}
	for path, authorizationHeader := range receivedAuthorizationByPath {
		if authorizationHeader != "" {
			t.Fatalf("expected no authorization header for %s, got %q", path, authorizationHeader)
		}
	}
}
