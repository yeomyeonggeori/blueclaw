package connectors

import (
	"context"
	"log/slog"

	"github.com/yeomyeonggeori/blueclaw/internal/identity"
)

type DirectMessageOpener func(ctx context.Context, platform string, externalUserID string) (conversationID string, replyTargetID string, errorValue error)

type DirectMessageAccounts interface {
	ListPlatformAccount() ([]identity.PlatformAccountIdentity, error)
}

func (connectorRuntime *ConnectorRuntime) UseRequesterDirectMessages(opener DirectMessageOpener, accounts DirectMessageAccounts) {
	connectorRuntime.directMessageOpener = opener
	connectorRuntime.directMessageAccounts = accounts
}

func (connectorRuntime *ConnectorRuntime) requesterDirectMessageTurn(ctx context.Context, requesterPersonID string) (*inboundTurn, bool) {
	if connectorRuntime.directMessageOpener == nil || connectorRuntime.directMessageAccounts == nil {
		return nil, false
	}
	account, isFound := connectorRuntime.requesterAccount(requesterPersonID)
	if !isFound {
		return nil, false
	}
	adapter, errorValue := connectorRuntime.findAdapter(account.Platform)
	if errorValue != nil {
		return nil, false
	}
	conversationID, replyTargetID, errorValue := connectorRuntime.directMessageOpener(ctx, account.Platform, account.ExternalUserID)
	if errorValue != nil || conversationID == "" || replyTargetID == "" {
		connectorRuntime.logger.Warn("connector."+account.Platform+".approval.direct_message_unavailable", slog.String("personID", requesterPersonID))
		return nil, false
	}
	event := PlatformInboundEvent{
		Platform:       account.Platform,
		ConversationID: conversationID,
		ReplyTargetID:  replyTargetID,
		SenderID:       account.ExternalUserID,
	}
	return &inboundTurn{
		adapter:     adapter,
		platform:    account.Platform,
		event:       event,
		replyTarget: ReplyTarget{ConversationID: conversationID, ReplyTargetID: replyTargetID},
		sendReply:   connectorRuntime.recordingDelivery(adapter.SendReply),
	}, true
}

func (connectorRuntime *ConnectorRuntime) requesterAccount(requesterPersonID string) (identity.PlatformAccountIdentity, bool) {
	accounts, errorValue := connectorRuntime.directMessageAccounts.ListPlatformAccount()
	if errorValue != nil {
		return identity.PlatformAccountIdentity{}, false
	}
	return identity.DirectMessageAccount(requesterPersonID, accounts)
}
