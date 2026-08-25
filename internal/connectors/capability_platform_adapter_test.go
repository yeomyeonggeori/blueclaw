package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestCapabilityPlatformAdapterParsesNormalizedHTTPEvent(t *testing.T) {
	adapter := NewCapabilityPlatformAdapter("mattermost", capability.Client{})
	request := httptest.NewRequest(
		http.MethodPost,
		"/connectors/mattermost/events",
		bytes.NewReader([]byte(`{"conversationID":"channel-1","messageID":"post-1","senderID":"user-1","replyTargetID":"reply-target-1","prompt":"안녕","inputParts":[{"type":"image","image":{"mimeType":"image/png","dataBase64":"aW1hZ2U=","filename":"screen.png"}}],"context":{"messages":[{"speaker":"admin","text":"이전"}],"hasMoreBefore":true,"historyCursor":"cursor-1","inputAttachments":[{"platform":"mattermost","fileID":"file-1","messageID":"post-1"}]}}`)),
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
	if parseResult.Event.Prompt != "안녕" {
		t.Fatalf("expected prompt, got %q", parseResult.Event.Prompt)
	}
	if parseResult.Event.ReplyTargetID != "reply-target-1" {
		t.Fatalf("expected reply target id, got %q", parseResult.Event.ReplyTargetID)
	}
	if !parseResult.Event.Context.HasMoreBefore || parseResult.Event.Context.HistoryCursor != "cursor-1" {
		t.Fatalf("expected history metadata, got %+v", parseResult.Event.Context)
	}
	if len(parseResult.Event.InputParts) != 1 || parseResult.Event.InputParts[0].Type != agentcontract.AgentPartTypeImage {
		t.Fatalf("expected input image part, got %+v", parseResult.Event.InputParts)
	}
	if len(parseResult.Event.Context.InputAttachments) != 1 || parseResult.Event.Context.InputAttachments[0].FileID != "file-1" {
		t.Fatalf("expected input attachment metadata, got %+v", parseResult.Event.Context.InputAttachments)
	}
}

func TestCapabilityPlatformAdapterDoesNotParseMattermostRawAskAction(t *testing.T) {
	adapter := NewCapabilityPlatformAdapter("mattermost", capability.Client{})
	request := httptest.NewRequest(
		http.MethodPost,
		"/connectors/mattermost/events",
		bytes.NewReader([]byte(`{"user_id":"user-1","post_id":"post-1","channel_id":"channel-1","context":{"action":"ask_choice","interactionID":"interaction-1","taskRunID":"task-1","conversationID":"channel-1","replyTargetID":"reply-target-1","choiceKey":"B","responseLanguage":"ko"}}`)),
	)

	parseResult, errorValue := adapter.ParseHTTPEvent(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected raw ask payload to be ignored without parser error: %v", errorValue)
	}
	if parseResult.HasEvent {
		t.Fatalf("expected raw Mattermost payload to stay outside Blueclaw core, got %+v", parseResult.Event)
	}
}

func TestCapabilityPlatformAdapterImportsInputAttachments(t *testing.T) {
	var receivedPath string
	var receivedBody InputAttachmentImportRequest
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		receivedPath = request.URL.Path
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedBody); errorValue != nil {
			t.Fatalf("expected request body to decode: %v", errorValue)
		}
		return jsonCapabilityResponse(http.StatusOK, `{"inputParts":[{"type":"file","file":{"path":"/workspace/private/people/person-1/inbox/mattermost/post-1/report.pdf","filename":"report.pdf","markdownPreview":"# Report"}}]}`), nil
	}}
	adapter := NewCapabilityPlatformAdapter("mattermost", capability.Client{
		Endpoint:   "http://capability.test",
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

func TestCapabilityPlatformAdapterAddsReaction(t *testing.T) {
	var receivedPath string
	var receivedBody capabilityReactionRequest
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		receivedPath = request.URL.Path
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedBody); errorValue != nil {
			t.Fatalf("expected request body to decode: %v", errorValue)
		}
		return jsonCapabilityResponse(http.StatusOK, `{}`), nil
	}}
	adapter := NewCapabilityPlatformAdapter("mattermost", capability.Client{
		Endpoint:   "http://capability.test",
		HTTPClient: httpClient,
	})

	errorValue := adapter.AddReaction(context.Background(), ReactionTarget{
		ConversationID: "channel-1",
		MessageID:      "post-1",
		EmojiName:      "white_check_mark",
		Reason:         "consume",
	})
	if errorValue != nil {
		t.Fatalf("expected reaction add to succeed: %v", errorValue)
	}

	if receivedPath != "/v1/platform/mattermost/reaction.add" {
		t.Fatalf("unexpected reaction path %q", receivedPath)
	}
	if receivedBody.ConversationID != "channel-1" || receivedBody.MessageID != "post-1" || receivedBody.EmojiName != "white_check_mark" || receivedBody.Reason != "consume" {
		t.Fatalf("unexpected reaction body %+v", receivedBody)
	}
}

func TestCapabilityPlatformAdapterUsesCapabilityEndpointsWithoutAuthorization(t *testing.T) {
	receivedAuthorizationByPath := map[string]string{}
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		receivedAuthorizationByPath[request.URL.Path] = request.Header.Get("Authorization")
		switch request.URL.Path {
		case "/v1/platform/slack/identity.resolve":
			var requestDocument capabilityIdentityRequest
			errorValue := json.NewDecoder(request.Body).Decode(&requestDocument)
			if errorValue != nil {
				t.Fatalf("expected identity request to decode: %v", errorValue)
			}
			if requestDocument.SenderID != "user-1" {
				t.Fatalf("expected sender id, got %q", requestDocument.SenderID)
			}
			return jsonCapabilityResponse(http.StatusOK, `{"email":"lee@example.com","displayName":"Lee"}`), nil
		case "/v1/platform/slack/progress.start", "/v1/platform/slack/progress.stop":
			var requestDocument capabilityProgressRequest
			errorValue := json.NewDecoder(request.Body).Decode(&requestDocument)
			if errorValue != nil {
				t.Fatalf("expected progress request to decode: %v", errorValue)
			}
			if requestDocument.ReplyTargetID != "reply-target-1" {
				t.Fatalf("expected progress reply target id, got %q", requestDocument.ReplyTargetID)
			}
			return jsonCapabilityResponse(http.StatusOK, `{}`), nil
		case "/v1/platform/slack/reply.send":
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
			if len(requestDocument.Attachments) != 1 || requestDocument.Attachments[0].DevicePath != "/tmp/internkim-companion-files/screen.png" {
				t.Fatalf("expected reply attachment, got %+v", requestDocument.Attachments)
			}
			return jsonCapabilityResponse(http.StatusOK, `{"dispatchID":"dispatch-1"}`), nil
		case "/v1/platform/slack/history.fetch":
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
			t.Fatalf("unexpected capability path %q", request.URL.Path)
		}
		return nil, nil
	}}

	adapter := NewCapabilityPlatformAdapter("slack", capability.Client{
		Endpoint:   "http://internkim-capability",
		HTTPClient: httpClient,
	})
	replyTarget := ReplyTarget{ConversationID: "D1", ReplyTargetID: "reply-target-1", DedupeKey: "slack:D1:m1"}

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
	dispatchID, errorValue := adapter.SendReply(context.Background(), replyTarget, OutboundReply{
		Message: "hello",
		Attachments: []toolcontract.FileAttachment{{
			DevicePath: "/tmp/internkim-companion-files/screen.png",
			Filename:   "screen.png",
		}},
	})
	if errorValue != nil {
		t.Fatalf("expected reply send: %v", errorValue)
	}
	visibleContext, errorValue := adapter.FetchHistory(context.Background(), "cursor-1", 20)
	if errorValue != nil {
		t.Fatalf("expected history fetch: %v", errorValue)
	}

	if platformIdentity.Platform != "slack" || platformIdentity.ExternalUserID != "user-1" {
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

type fakeCapabilityHTTPClient struct {
	handler func(*http.Request) (*http.Response, error)
}

func (client fakeCapabilityHTTPClient) Do(request *http.Request) (*http.Response, error) {
	return client.handler(request)
}

func jsonCapabilityResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}
