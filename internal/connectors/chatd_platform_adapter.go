package connectors

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
)

const DefaultChatdEndpoint = "http://127.0.0.1:18090"

type ChatdPlatformAdapter struct {
	PlatformName string
	ChatdClient  capability.Client
}

func NewChatdPlatformAdapter(platform string, chatdClient capability.Client) ChatdPlatformAdapter {
	return ChatdPlatformAdapter{
		PlatformName: strings.TrimSpace(platform),
		ChatdClient:  chatdClient,
	}
}

func (adapter ChatdPlatformAdapter) Name() string {
	return adapter.PlatformName
}

func (adapter ChatdPlatformAdapter) ParseHTTPEvent(_ context.Context, request *http.Request) (HTTPParseResult, error) {
	payload, errorValue := io.ReadAll(request.Body)
	if errorValue != nil {
		return HTTPParseResult{}, errorValue
	}

	event, hasEvent, errorValue := ParseNormalizedInboundEvent(payload, adapter.Name(), "http")
	if errorValue != nil || !hasEvent {
		return HTTPParseResult{}, errorValue
	}

	return HTTPParseResult{Event: event, HasEvent: true}, nil
}

func (adapter ChatdPlatformAdapter) ParseRealtimeEvent(_ context.Context, payload []byte, source string) (PlatformInboundEvent, bool, error) {
	return ParseNormalizedInboundEvent(payload, adapter.Name(), source)
}

func (adapter ChatdPlatformAdapter) ResolveIdentity(ctx context.Context, senderUserID string) (identity.PlatformAccountIdentity, error) {
	var response identity.PlatformAccountIdentity
	errorValue := adapter.post(ctx, "identity.resolve", capabilityIdentityRequest{SenderID: senderUserID}, &response)
	if errorValue != nil {
		return identity.PlatformAccountIdentity{}, errorValue
	}

	response.Platform = adapter.Name()
	response.ExternalUserID = senderUserID
	return response, nil
}

func (adapter ChatdPlatformAdapter) StartProgress(ctx context.Context, replyTarget ReplyTarget) error {
	return adapter.post(ctx, "progress.start", capabilityProgressRequest{ReplyTargetID: replyTarget.ReplyTargetID}, nil)
}

func (adapter ChatdPlatformAdapter) StopProgress(ctx context.Context, replyTarget ReplyTarget) error {
	return adapter.post(ctx, "progress.stop", capabilityProgressRequest{ReplyTargetID: replyTarget.ReplyTargetID}, nil)
}

func (adapter ChatdPlatformAdapter) AddReaction(ctx context.Context, target ReactionTarget) error {
	return adapter.post(ctx, "reaction.add", capabilityReactionRequest{
		ConversationID: strings.TrimSpace(target.ConversationID),
		MessageID:      strings.TrimSpace(target.MessageID),
		EmojiName:      strings.TrimSpace(target.EmojiName),
		Reason:         strings.TrimSpace(target.Reason),
	}, nil)
}

func (adapter ChatdPlatformAdapter) RemoveReaction(ctx context.Context, target ReactionTarget) error {
	return adapter.post(ctx, "reaction.remove", capabilityReactionRequest{
		ConversationID: strings.TrimSpace(target.ConversationID),
		MessageID:      strings.TrimSpace(target.MessageID),
		EmojiName:      strings.TrimSpace(target.EmojiName),
		Reason:         strings.TrimSpace(target.Reason),
	}, nil)
}

func (adapter ChatdPlatformAdapter) SendReply(ctx context.Context, replyTarget ReplyTarget, reply OutboundReply) (string, error) {
	var response capabilityReplyResponse
	errorValue := adapter.post(ctx, "reply.send", capabilityReplyRequest{
		ReplyTargetID:      replyTarget.ReplyTargetID,
		AnsweringMessageID: replyTarget.AnsweringMessageID,
		Message:            reply.Message,
		TaskRunID:          reply.TaskRunID,
		ReplyKind:          reply.ReplyKind,
		RawEventID:         reply.RawEventID,
		OutboxID:           reply.OutboxID,
		Attachments:        buildCapabilityReplyAttachments(reply.Attachments),
		RecoveryActions:    reply.RecoveryActions,
		FailureNotice:      reply.FailureNotice,
		Interaction:        reply.Interaction,
	}, &response)
	if errorValue != nil {
		return "", errorValue
	}
	return strings.TrimSpace(response.DispatchID), nil
}

func (adapter ChatdPlatformAdapter) EditReply(ctx context.Context, replyTarget ReplyTarget, messageID string, message string) error {
	return adapter.post(ctx, "message.edit", capabilityMessageEditRequest{
		ReplyTargetID: replyTarget.ReplyTargetID,
		MessageID:     messageID,
		Message:       message,
	}, nil)
}

func (adapter ChatdPlatformAdapter) DeleteReply(ctx context.Context, replyTarget ReplyTarget, messageID string) error {
	return adapter.post(ctx, "message_delete", capabilityMessageDeleteRequest{
		ReplyTargetID: replyTarget.ReplyTargetID,
		MessageID:     messageID,
	}, nil)
}

func (adapter ChatdPlatformAdapter) FetchHistory(ctx context.Context, historyCursor string, limit int) (VisibleContext, error) {
	var response VisibleContext
	errorValue := adapter.post(ctx, "history.fetch", capabilityHistoryRequest{
		HistoryCursor: historyCursor,
		Limit:         limit,
		Direction:     "before",
	}, &response)
	if errorValue != nil {
		return VisibleContext{}, errorValue
	}
	return response, nil
}

func (adapter ChatdPlatformAdapter) ImportInputAttachments(ctx context.Context, request InputAttachmentImportRequest) (InputAttachmentImportResult, error) {
	var response InputAttachmentImportResult
	errorValue := adapter.post(ctx, "attachments.import", request, &response)
	if errorValue != nil {
		return InputAttachmentImportResult{}, errorValue
	}
	return response, nil
}

func (adapter ChatdPlatformAdapter) post(ctx context.Context, capabilityName string, requestDocument any, responseDocument any) error {
	if strings.TrimSpace(adapter.Name()) == "" {
		return errors.New("chatd platform name is required")
	}
	return adapter.ChatdClient.PostJSON(ctx, adapter.endpointPath(capabilityName), requestDocument, responseDocument)
}

func (adapter ChatdPlatformAdapter) endpointPath(capabilityName string) string {
	return "/v1/platform/" + url.PathEscape(adapter.Name()) + "/" + capabilityName
}
