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
	return &SessionTurn{connectorRuntime: connectorRuntime, turn: &inboundTurn{
		adapter:     adapter,
		platform:    event.Platform,
		event:       event,
		replyTarget: replyTarget,
		sendReply:   sendReply,
		personID:    personID,
	}}
}

func (sessionTurn *SessionTurn) ContinueOpenInteractions(ctx context.Context, launchRequest agentruntime.TaskLaunchRequest) (agentruntime.TaskLaunchRequest, bool, error) {
	connectorRuntime, turn := sessionTurn.connectorRuntime, sessionTurn.turn
	if turn.adapter == nil {
		return launchRequest, false, nil
	}
	if _, isAnswered, errorValue := connectorRuntime.resolveOpenInteractions(ctx, turn); isAnswered {
		connectorRuntime.recordUnclaimedIntakeCalls(turn.event)
		return launchRequest, true, errorValue
	}
	connectorRuntime.resolveTurnActiveGoal(ctx, turn)
	connectorRuntime.resolveTurnPriorTask(turn)
	precomputedTurnDecision := precomputedTurnDecisionForLaunch(turn.turnDecision, turn.hasTurnDecision)
	return withTurnContinuation(launchRequest, connectorRuntime.conversationTurnFor(turn, precomputedTurnDecision)), false, nil
}

func (sessionTurn *SessionTurn) DeliverReply(ctx context.Context, turnResult agentcontract.AgentTurnResult) error {
	connectorRuntime, turn := sessionTurn.connectorRuntime, sessionTurn.turn
	connectorRuntime.recordHeldIntakeCalls(turnResult.TaskRun.TaskRunID, turn.event)
	_, errorValue := connectorRuntime.dispatchTaskReply(ctx, turn.platform, turn.adapter, turn.event, turn.replyTarget, turnResult, turn.engagedAckEmojiName, turn.sendReply)
	return errorValue
}

func (sessionTurn *SessionTurn) ShowProgressBeforeAddressing(ctx context.Context) {
	if !shouldStartProgressBeforeAddressing(sessionTurn.turn.event) {
		return
	}
	sessionTurn.ShowProgress(ctx)
}

func (sessionTurn *SessionTurn) ShowProgress(ctx context.Context) {
	if sessionTurn.turn.adapter == nil {
		return
	}
	sessionTurn.connectorRuntime.showTurnProgress(ctx, sessionTurn.turn)
}

func (sessionTurn *SessionTurn) EndProgress() {
	sessionTurn.turn.endProgress()
}
