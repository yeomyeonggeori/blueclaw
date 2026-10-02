package connectors

import (
	"context"
	"log/slog"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type ReplySender func(context.Context, ReplyTarget, OutboundReply) (string, error)

type SessionTurn struct {
	connectorRuntime *ConnectorRuntime
	turn             *inboundTurn
}

func (connectorRuntime *ConnectorRuntime) OpenSessionTurn(ctx context.Context, event PlatformInboundEvent, personID string, sendReply ReplySender) *SessionTurn {
	event = withInboundDecision(event)
	event.isApprovalAskedElsewhere = true
	adapter, errorValue := connectorRuntime.findAdapter(event.Platform)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector.session.adapter_missing", slog.String("platform", event.Platform), slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
	}
	replyTarget, _ := connectorRuntime.buildReplyTarget(ctx, adapter, event)
	turn := &inboundTurn{
		adapter:     adapter,
		platform:    event.Platform,
		event:       event,
		replyTarget: replyTarget,
		sendReply:   sendReply,
		personID:    personID,
	}
	if adapter != nil && shouldStartProgressBeforeAddressing(event) {
		connectorRuntime.showTurnProgress(ctx, turn)
	}
	return &SessionTurn{connectorRuntime: connectorRuntime, turn: turn}
}

func (sessionTurn *SessionTurn) PrepareLaunch(ctx context.Context, launchRequest agentruntime.TaskLaunchRequest) (agentruntime.TaskLaunchRequest, bool, error) {
	connectorRuntime, turn := sessionTurn.connectorRuntime, sessionTurn.turn
	if turn.adapter == nil {
		return launchRequest, false, nil
	}
	if _, isAnswered, errorValue := connectorRuntime.resolveOpenInteractions(ctx, turn); isAnswered {
		connectorRuntime.recordUnclaimedIntakeCalls(turn.event)
		return launchRequest, true, errorValue
	}
	connectorRuntime.resolveTurnActiveGoal(ctx, turn)
	connectorRuntime.prepareTurnForLaunch(ctx, turn)
	precomputedTurnDecision := precomputedTurnDecisionForLaunch(turn.turnDecision, turn.hasTurnDecision)
	conversationTurn := connectorRuntime.conversationTurnFor(turn, precomputedTurnDecision)
	return withTurnContinuation(connectorRuntime.withTurnMessage(launchRequest, conversationTurn), conversationTurn), false, nil
}

func (sessionTurn *SessionTurn) DeliverReply(ctx context.Context, turnResult agentcontract.AgentTurnResult) error {
	connectorRuntime, turn := sessionTurn.connectorRuntime, sessionTurn.turn
	connectorRuntime.recordHeldIntakeCalls(turnResult.TaskRun.TaskRunID, turn.event)
	_, errorValue := connectorRuntime.dispatchTaskReply(ctx, turn.platform, turn.adapter, turn.event, turn.replyTarget, turnResult, turn.engagedAckEmojiName, turn.sendReply)
	return errorValue
}

func (sessionTurn *SessionTurn) End() {
	sessionTurn.turn.endProgress()
}
