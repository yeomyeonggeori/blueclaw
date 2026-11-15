package acpsession

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
)

const (
	HandoffEndedExtensionMethod  = "_kim.intern/handoffEnded"
	HandoffClosedExtensionMethod = "_kim.intern/handoffClosed"
)

type HandoffClosedNotice struct {
	HandoffID string `json:"handoffID"`
}

type HandoffEndedReport struct {
	SessionID string `json:"sessionId"`
	HandoffID string `json:"handoffID"`
	OptionKey string `json:"optionKey"`
}

var errNoRunIsParkedOnThatHandoff = errors.New("no run of this session's requester is parked on a hand-over by that id with that option")

func (agent *Agent) HandleExtensionMethod(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case HandoffEndedExtensionMethod:
		return agent.settleHandoffEnded(ctx, params)
	case DeliveredExtensionMethod:
		return agent.settleDelivered(params)
	case UndeliveredExtensionMethod:
		return agent.settleUndelivered(params)
	}
	return nil, acp.NewMethodNotFound(method)
}

func (agent *Agent) postParkedQuestion(ctx context.Context, sessionID acp.SessionId, approvalRequest mcpserver.ApprovalRequest, question approvalgate.ParkedQuestion) error {
	sessionTurn := agent.sessionTurns.OpenSessionTurn(ctx, questionEventOf(approvalRequest), approvalRequest.RequesterPersonID, func(ctx context.Context, replyTarget connectors.ReplyTarget, reply connectors.OutboundReply) (string, error) {
		return agent.deliverReply(ctx, sessionID, Delivery{DeliveryID: newRandomIdentifier(), ReplyTargetID: replyTarget.ReplyTargetID, HandoffID: question.HandoffID, IsFinal: true}, reply)
	})
	errorValue := sessionTurn.PostParkedQuestion(ctx, approvalRequest.TaskRunID, question.HoldID)
	if errorValue != nil && (ctx.Err() != nil || agent.hasLostItsClient()) {
		return errors.Join(approvalgate.ErrQuestionWillBePostedLater, errorValue)
	}
	return errorValue
}

func questionEventOf(approvalRequest mcpserver.ApprovalRequest) connectors.PlatformInboundEvent {
	return connectors.PlatformInboundEvent{
		Platform:       approvalRequest.Platform,
		ConversationID: approvalRequest.ConversationID,
		ReplyTargetID:  approvalRequest.ReplyTargetID,
	}
}

func (agent *Agent) postQuestionsNeverPosted(ctx context.Context, sessionID acp.SessionId, sessionContext SessionContext) {
	for _, unposted := range agent.sessionTurns.QuestionsNeverPosted(sessionContext.Requester.PersonID, sessionContext.Addressing.ConversationID) {
		unposted.ApprovalRequest.RequesterEmail = sessionContext.Requester.Email
		unposted.ApprovalRequest.Platform = sessionContext.Addressing.Platform
		unposted.ApprovalRequest.ReplyTargetID = firstNonEmpty(unposted.ApprovalRequest.ReplyTargetID, sessionContext.Addressing.ReplyTargetID)
		if errorValue := agent.postParkedQuestion(ctx, sessionID, unposted.ApprovalRequest, unposted.Question); errorValue != nil {
			agent.logger.Warn("acpsession.question.still_unposted", "taskRunID", unposted.ApprovalRequest.TaskRunID, "error", errorValue.Error())
		}
	}
}

func (agent *Agent) settleHandoffEnded(ctx context.Context, params json.RawMessage) (any, error) {
	report := HandoffEndedReport{}
	if errorValue := json.Unmarshal(params, &report); errorValue != nil {
		return nil, errorValue
	}
	sessionID := acp.SessionId(report.SessionID)
	session, isOpen := agent.session(sessionID)
	if !isOpen {
		return nil, errSessionIsNotOpen
	}
	answer := agent.sessionTurns.AnswerParkedHandoff(ctx, session.context.Requester.PersonID, report.HandoffID, report.OptionKey)
	if !answer.IsAnswer {
		return nil, errNoRunIsParkedOnThatHandoff
	}
	if answer.ShouldResume {
		go agent.resumeParkedRun(context.WithoutCancel(ctx), sessionID, session.context, answer, Delivery{DeliveryID: newRandomIdentifier()})
	}
	return map[string]any{}, nil
}

func (agent *Agent) resumeParkedRun(ctx context.Context, sessionID acp.SessionId, sessionContext SessionContext, answer connectors.ParkedAnswer, delivery Delivery) {
	taskRun := answer.TaskRun
	requester := sessionContext.Requester
	addressing := sessionContext.Addressing
	delivery.ReplyTargetID = firstNonEmpty(taskRun.OriginReplyTargetID, addressing.ReplyTargetID)
	launchRequest := agentruntime.TaskLaunchRequest{
		Source:                 agentruntime.TaskLaunchSourceConnector,
		SourceReference:        "acp:answered:" + taskRun.TaskRunID,
		RequesterPersonID:      requester.PersonID,
		RequesterName:          agent.requesterName(requester),
		RequesterCallingName:   requester.CallingName,
		RequesterHandle:        requester.Handle,
		RequesterEmail:         requester.Email,
		IsRuntimeRestartResume: true,
		ExistingTaskRunID:      taskRun.TaskRunID,
		SettledCalls:           answer.SettledCalls,
		ActiveGoal:             connectors.ActiveGoalOfARunResumedOnItsHeldCall(taskRun, agent.taskRunStore.ListTaskEvent(taskRun.TaskRunID)),
		OriginReplyTargetID:    delivery.ReplyTargetID,
		OriginIsThread:         taskRun.OriginIsThread || addressing.IsThread,
		ProfileName:            defaultProfileName,
		Platform:               addressing.Platform,
		ConversationID:         addressing.ConversationID,
		ConversationType:       addressing.ConversationType,
		ReplyTargetID:          delivery.ReplyTargetID,
		Prompt:                 taskRun.Prompt,
		ResponseLanguage:       addressing.ResponseLanguage,
		PersonAccess:           agent.directory.ResolvePersonAccess(requester.PersonID),
		ToolCallObserver:       agent.toolCallObserverFor(ctx, sessionID, delivery),
		CheckpointSender:       agent.checkpointSenderFor(sessionID, delivery),
	}
	launchResult, errorValue := agent.taskLauncher.Launch(ctx, launchRequest)
	if errorValue != nil {
		agent.logger.Warn("acpsession.question.answered_run_will_not_resume", "taskRunID", taskRun.TaskRunID, "error", errorValue.Error())
		return
	}
	sessionTurn := agent.sessionTurns.OpenSessionTurn(ctx, inboundEventOf(MessageContext{}, launchRequest), requester.PersonID, agent.replySenderForDelivery(sessionID, delivery.DeliveryID))
	agent.deliverTurnReply(ctx, sessionID, sessionTurn, launchResult.TurnResult)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
