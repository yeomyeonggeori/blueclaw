package api

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
)

type IdentityResolver interface {
	ResolvePersonIDByEmail(email string) (string, bool)
	ResolvePersonDisplayName(personID string) string
}

type Adapter struct {
	IdentityService IdentityResolver
	ReplyStore      *ReplyStore
}

func NewAdapter(identityService IdentityResolver, replyStore *ReplyStore) Adapter {
	return Adapter{IdentityService: identityService, ReplyStore: replyStore}
}

func (adapter Adapter) Name() string {
	return "api"
}

func (adapter Adapter) ParseHTTPEvent(_ context.Context, request *http.Request) (connectors.HTTPParseResult, error) {
	payload, errorValue := io.ReadAll(request.Body)
	if errorValue != nil {
		return connectors.HTTPParseResult{}, errorValue
	}
	event, hasEvent, errorValue := connectors.ParseNormalizedInboundEvent(payload, adapter.Name(), "http")
	if errorValue != nil || !hasEvent {
		return connectors.HTTPParseResult{}, errorValue
	}
	return connectors.HTTPParseResult{Event: event, HasEvent: true}, nil
}

func (adapter Adapter) ResolveIdentity(_ context.Context, senderID string) (identity.PlatformAccountIdentity, error) {
	email := strings.ToLower(strings.TrimSpace(senderID))
	personID, isFound := adapter.IdentityService.ResolvePersonIDByEmail(email)
	if !isFound {
		return identity.PlatformAccountIdentity{}, nil
	}
	return identity.PlatformAccountIdentity{
		Platform:       adapter.Name(),
		ExternalUserID: senderID,
		Email:          email,
		PersonID:       personID,
		DisplayName:    adapter.IdentityService.ResolvePersonDisplayName(personID),
	}, nil
}

func (adapter Adapter) StartProgress(context.Context, connectors.ReplyTarget) error {
	return nil
}

func (adapter Adapter) StopProgress(context.Context, connectors.ReplyTarget) error {
	return nil
}

func (adapter Adapter) SendReply(_ context.Context, replyTarget connectors.ReplyTarget, reply connectors.OutboundReply) (string, error) {
	if adapter.ReplyStore != nil {
		adapter.ReplyStore.Append(replyTarget.ConversationID, replyTarget.ReplyTargetID, reply)
	}
	return firstNonEmpty(reply.TaskRunID, replyTarget.DedupeKey), nil
}

func (adapter Adapter) FetchHistory(context.Context, string, int) (connectors.VisibleContext, error) {
	return connectors.VisibleContext{}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}
