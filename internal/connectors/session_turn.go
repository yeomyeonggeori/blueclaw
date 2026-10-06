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
	adapter, errorValue := connectorRuntime.findAdapter(event.Platform)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector.session.adapter_missing", slog.String("platform", event.Platform), slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
	}
	replyTarget, _ := connectorRuntime.buildReplyTarget(ctx, adapter, event)
	return &SessionTurn{connectorRuntime: connectorRuntime, turn: &inboundTurn{
		adapter:        adapter,
		platform:       event.Platform,
		event:          event,
		replyTarget:    replyTarget,
		sendReply:      connectorRuntime.recordingDelivery(sendReply),
		personID:       personID,
		personAccess:   connectorRuntime.identityService.ResolvePersonAccess(personID),
		requesterEmail: connectorRuntime.requesterEmailForEvent(personID, event),
	}}
}

func (sessionTurn *SessionTurn) DecisionRequest(ctx context.Context) agentcontract.IntakeDecisionRequest {
	connectorRuntime, turn := sessionTurn.connectorRuntime, sessionTurn.turn
	request, _ := connectorRuntime.inboundDecisionRequestForTurn(withConnectorEvent(ctx, turn.event), turn)
	return request
}

func (sessionTurn *SessionTurn) ContinueOpenInteractions(ctx context.Context, launchRequest agentruntime.TaskLaunchRequest) (agentruntime.TaskLaunchRequest, bool, error) {
	connectorRuntime, turn := sessionTurn.connectorRuntime, sessionTurn.turn
	if turn.adapter == nil {
		return launchRequest, false, nil
	}
	ctx = withConnectorEvent(ctx, turn.event)
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
	notDelivered := []*FilesNotDelivered{}
	sendNoting := func(ctx context.Context, replyTarget ReplyTarget, reply OutboundReply) (string, error) {
		dispatchID, errorValue := turn.sendReply(ctx, replyTarget, reply)
		if undelivered, isUndelivered := filesNotDeliveredIn(errorValue); isUndelivered {
			notDelivered = append(notDelivered, undelivered)
		}
		return dispatchID, errorValue
	}
	_, errorValue := connectorRuntime.dispatchTaskReply(withConnectorEvent(ctx, turn.event), turn.platform, turn.adapter, turn.event, turn.replyTarget, turnResult, turn.engagedAckEmojiName, sendNoting)
	for _, undelivered := range notDelivered {
		connectorRuntime.tellOfFilesNotDelivered(ctx, turn, turnResult.TaskRun.TaskRunID, undelivered)
	}
	return errorValue
}

func (sessionTurn *SessionTurn) DeliverApprovalQuestion(ctx context.Context, taskRunID string, question string) error {
	return sessionTurn.connectorRuntime.deliverApprovalQuestion(ctx, sessionTurn.turn, taskRunID, question)
}

func (connectorRuntime *ConnectorRuntime) deliverApprovalQuestion(ctx context.Context, turn *inboundTurn, taskRunID string, question string) error {
	reply := OutboundReply{Message: question, TaskRunID: taskRunID, ReplyKind: connectorReplyKindApprovalQuestion}
	dispatchID, errorValue := turn.sendReply(withConnectorEvent(ctx, turn.event), turn.replyTarget, reply)
	if errorValue != nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, agentcontract.TaskEventConnectorReplyFailed, connectorReplyEventBody(turn.event, reply, "", "", errorValue.Error()))
		connectorRuntime.logger.Error("connector."+turn.platform+".outbound.failed", slog.String("taskRunID", taskRunID), slog.String("replyKind", reply.ReplyKind), slog.String("error", errorValue.Error()))
		return errorValue
	}
	connectorRuntime.logger.Info("connector."+turn.platform+".outbound.sent", slog.String("taskRunID", taskRunID), slog.String("replyKind", reply.ReplyKind), slog.String("replyDispatchID", dispatchID))
	return nil
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
