package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

const (
	ParkedQuestionAnsweredReason = "parked_question_answered"
	TaskEventQuestionPosted      = "approval.question_posted"
	requesterAnswerSource        = "requester_reply"
	operatorAnswerSource         = "operator_terminal"
	handoffAnswerSource          = "browser_handoff"
	movedOnCancelReason          = "the requester moved on from the question this run was parked on"
)

var (
	errRequesterCannotBeReached = errors.New("there is no conversation the requester can be asked in")
	ErrHoldOffersChoices        = errors.New("this hold asks the requester to pick an option, so only the requester can approve it; it can be rejected")
	ErrRunIsNotParked           = errors.New("this run is not parked on a question")
)

type postedQuestionRecord struct {
	HoldID         string `json:"holdID"`
	Platform       string `json:"platform"`
	ConversationID string `json:"conversationID"`
	ReplyTargetID  string `json:"replyTargetID"`
	MessageID      string `json:"messageID"`
}

type parkedRun struct {
	taskRun    task.TaskRun
	taskEvents []task.TaskEvent
	hold       holdrecord.Hold
	posted     postedQuestionRecord
}

func (parked parkedRun) question() approvalreply.Question {
	return approvalreply.Question{Text: parked.hold.Call.Confirmation, Options: approvalreply.OptionsOf(approvalgate.QuestionOffers(parked.hold.Choices))}
}

func (parked parkedRun) interaction() *AskInteraction {
	return heldCallInteraction(parked.hold.ID, parked.hold.Choices, parked.hold.Call.ToolInput)
}

func (parked parkedRun) postedQuestion() PostedQuestion {
	return PostedQuestion{ConversationID: parked.posted.ConversationID, ReplyTargetID: parked.posted.ReplyTargetID, MessageID: parked.posted.MessageID}
}

type questionPosting struct {
	mutex sync.Mutex
}

func (connectorRuntime *ConnectorRuntime) ThreadQuestionPoster() approvalgate.QuestionPoster {
	return threadQuestionPoster{connectorRuntime: connectorRuntime}
}

type threadQuestionPoster struct {
	connectorRuntime *ConnectorRuntime
}

func (poster threadQuestionPoster) PostQuestion(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question approvalgate.ParkedQuestion) error {
	turn, isReady := poster.connectorRuntime.questionTurn(ctx, approvalRequest)
	if !isReady {
		return errRequesterCannotBeReached
	}
	return poster.connectorRuntime.postParkedQuestion(ctx, turn, approvalRequest.TaskRunID, question.HoldID)
}

func (connectorRuntime *ConnectorRuntime) questionTurn(ctx context.Context, approvalRequest mcpserver.ApprovalRequest) (*inboundTurn, bool) {
	event, isFound := connectorEventFromContext(ctx)
	if !isFound {
		return connectorRuntime.requesterDirectMessageTurn(ctx, approvalRequest.RequesterPersonID)
	}
	adapter, errorValue := connectorRuntime.findAdapter(event.Platform)
	if errorValue != nil {
		return nil, false
	}
	return &inboundTurn{
		adapter:     adapter,
		platform:    event.Platform,
		event:       event,
		replyTarget: replyTargetOf(event),
		sendReply:   connectorRuntime.pendingRequestReplySender(event.DedupeKey(), connectorRuntime.recordingDelivery(adapter.SendReply), true),
	}, true
}

func (connectorRuntime *ConnectorRuntime) postParkedQuestion(ctx context.Context, turn *inboundTurn, taskRunID string, holdID string) error {
	connectorRuntime.questionPosting.mutex.Lock()
	defer connectorRuntime.questionPosting.mutex.Unlock()
	taskRun, isFound := connectorRuntime.taskRunService.FindTaskRun(taskRunID)
	if !isFound {
		return ErrRunIsNotParked
	}
	taskEvents := connectorRuntime.taskRunService.ListTaskEvent(taskRunID)
	hold, isPending := holdrecord.LatestHold(holdrecord.Holds(taskEvents), holdrecord.StatePending)
	if !isPending || hold.ID != holdID {
		return ErrRunIsNotParked
	}
	if _, isPosted := postedRecordOf(taskEvents, holdID); isPosted {
		return nil
	}
	interaction := connectorRuntime.addressedInteraction(heldCallInteraction(hold.ID, hold.Choices, hold.Call.ToolInput), taskRunID, taskRun.RequesterPersonID, turn)
	messageID, errorValue := connectorRuntime.deliverApprovalQuestion(ctx, turn, taskRunID, hold.Call.Confirmation, interaction)
	if errorValue != nil {
		return failedQuestionDelivery(errorValue)
	}
	posted := postedQuestionRecord{HoldID: holdID, Platform: turn.platform, ConversationID: turn.event.ConversationID, ReplyTargetID: turn.event.ReplyTargetID, MessageID: messageID}
	connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, TaskEventQuestionPosted, agentruntime.MarshalBody(posted))
	connectorRuntime.moveOnFromOlderQuestions(ctx, taskRun, posted)
	return nil
}

func failedQuestionDelivery(deliveryError error) error {
	var transportError capability.TransportError
	if errors.As(deliveryError, &transportError) {
		return errors.Join(approvalgate.ErrQuestionWillBePostedLater, deliveryError)
	}
	return deliveryError
}

func postedRecordOf(taskEvents []task.TaskEvent, holdID string) (postedQuestionRecord, bool) {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		if taskEvents[index].Name != TaskEventQuestionPosted {
			continue
		}
		posted := postedQuestionRecord{}
		if json.Unmarshal([]byte(taskEvents[index].Body), &posted) == nil && posted.HoldID == holdID {
			return posted, true
		}
	}
	return postedQuestionRecord{}, false
}

func PostedApprovalQuestionMessageID(taskEvents []task.TaskEvent) string {
	hold, isPending := holdrecord.LatestHold(holdrecord.Holds(taskEvents), holdrecord.StatePending)
	if !isPending {
		return ""
	}
	posted, _ := postedRecordOf(taskEvents, hold.ID)
	return posted.MessageID
}

func (connectorRuntime *ConnectorRuntime) parkedRunsOf(personID string) []parkedRun {
	parkedRuns := []parkedRun{}
	for _, taskRun := range connectorRuntime.taskRunService.ListTaskRunByPersonID(personID) {
		if parked, isParked := connectorRuntime.parkedRunOf(taskRun); isParked {
			parkedRuns = append(parkedRuns, parked)
		}
	}
	return parkedRuns
}

func (connectorRuntime *ConnectorRuntime) parkedRunOf(taskRun task.TaskRun) (parkedRun, bool) {
	if taskRun.Status != task.TaskStatusWaitingApproval {
		return parkedRun{}, false
	}
	taskEvents := connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID)
	hold, isPending := holdrecord.LatestHold(holdrecord.Holds(taskEvents), holdrecord.StatePending)
	if !isPending {
		return parkedRun{}, false
	}
	posted, _ := postedRecordOf(taskEvents, hold.ID)
	return parkedRun{taskRun: taskRun, taskEvents: taskEvents, hold: hold, posted: posted}, true
}

func (parked parkedRun) isAnsweredFrom(platform string, placement MessagePlacement) bool {
	if parked.posted.HoldID == "" || parked.posted.Platform != platform {
		return false
	}
	return parked.postedQuestion().IsAnsweredBy(placement)
}

func (connectorRuntime *ConnectorRuntime) parkedRunAwaiting(personID string, event PlatformInboundEvent) (parkedRun, bool) {
	selected, isSelected := parkedRun{}, false
	for _, parked := range connectorRuntime.parkedRunsOf(personID) {
		if !parked.isAnsweredFrom(event.Platform, placementOf(event)) {
			continue
		}
		if isSelected && !parked.taskRun.UpdatedAt.After(selected.taskRun.UpdatedAt) {
			continue
		}
		selected, isSelected = parked, true
	}
	return selected, isSelected
}

func (connectorRuntime *ConnectorRuntime) moveOnFromOlderQuestions(ctx context.Context, asking task.TaskRun, posted postedQuestionRecord) {
	placement := MessagePlacement{ConversationID: posted.ConversationID, ReplyTargetID: posted.ReplyTargetID, IsThread: PostedQuestion{ConversationID: posted.ConversationID, ReplyTargetID: posted.ReplyTargetID}.isInThread()}
	for _, older := range connectorRuntime.parkedRunsOf(asking.RequesterPersonID) {
		if older.taskRun.TaskRunID == asking.TaskRunID || !older.isAnsweredFrom(posted.Platform, placement) {
			continue
		}
		approvalgate.MoveOnFromPendingHold(connectorRuntime.taskRunService, older.taskRun.TaskRunID)
		connectorRuntime.publishQuestionResolution(ctx, older, "")
		connectorRuntime.closeRunLeftBehind(older.taskRun)
	}
}

func (connectorRuntime *ConnectorRuntime) closeRunLeftBehind(taskRun task.TaskRun) {
	if _, errorValue := connectorRuntime.taskRunService.CancelTaskRunWithReason(taskRun.TaskRunID, taskRun.RequesterPersonID, movedOnCancelReason); errorValue != nil {
		connectorRuntime.logger.Warn("connector.parked_question.run_left_behind_not_closed", slog.String("taskRunID", taskRun.TaskRunID), slog.String("error", errorValue.Error()))
	}
}

type ParkedAnswer struct {
	IsAnswer     bool
	ShouldResume bool
	TaskRun      task.TaskRun
	SettledCalls []agentcontract.CarriedOutCall
}

func (connectorRuntime *ConnectorRuntime) answerParkedQuestion(ctx context.Context, turn *inboundTurn) ParkedAnswer {
	if turn.event.TaskRetry != nil {
		return ParkedAnswer{}
	}
	parked, isParked := connectorRuntime.parkedRunAwaiting(turn.personID, turn.event)
	if !isParked {
		return ParkedAnswer{}
	}
	optionKey, isPressed := pressedOptionKey(turn.event.AnsweredChoice, parked.interaction())
	if !isPressed {
		optionKey = connectorRuntime.optionReadFromReply(ctx, parked, turn.event.Prompt)
	}
	if optionKey == "" {
		connectorRuntime.moveOnFromQuestion(ctx, turn, parked)
		return ParkedAnswer{}
	}
	if carriesOwnWords(optionKey, isPressed) {
		approvalgate.RecordOwnWordsAnswer(connectorRuntime.taskRunService, parked.taskRun.TaskRunID, turn.event.Prompt)
	}
	connectorRuntime.recordConfirmationReplyClassified(parked.taskRun.TaskRunID, turn.event, optionKey)
	return connectorRuntime.settleParkedRun(ctx, parked, optionKey, requesterAnswerSource)
}

func (connectorRuntime *ConnectorRuntime) settleParkedRun(ctx context.Context, parked parkedRun, optionKey string, source string) ParkedAnswer {
	settlement, errorValue := connectorRuntime.approvalGate.SettleAnswer(ctx, connectorRuntime.approvalRequestOf(parked), optionKey, source)
	if errors.Is(errorValue, approvalgate.ErrAnswerSettledElsewhere) {
		return ParkedAnswer{IsAnswer: true, TaskRun: parked.taskRun}
	}
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector.parked_question.answer_not_settled", slog.String("taskRunID", parked.taskRun.TaskRunID), slog.String("error", errorValue.Error()))
		return ParkedAnswer{IsAnswer: true, TaskRun: parked.taskRun}
	}
	connectorRuntime.publishQuestionResolution(ctx, parked, optionKey)
	return ParkedAnswer{IsAnswer: true, ShouldResume: true, TaskRun: parked.taskRun, SettledCalls: settlement.SettledCalls()}
}

func (connectorRuntime *ConnectorRuntime) moveOnFromQuestion(ctx context.Context, turn *inboundTurn, parked parkedRun) {
	movedOnCall, isMovedOn := approvalgate.MoveOnFromPendingHold(connectorRuntime.taskRunService, parked.taskRun.TaskRunID)
	if !isMovedOn {
		return
	}
	connectorRuntime.publishQuestionResolution(ctx, parked, "")
	turn.movedOnRun = &parked.taskRun
	turn.settledCalls = append(turn.settledCalls, movedOnCall)
}

func (connectorRuntime *ConnectorRuntime) optionReadFromReply(ctx context.Context, parked parkedRun, reply string) string {
	reading, errorValue := connectorRuntime.readReplyToQuestion(ctx, parked.taskRun.TaskRunID, parked.question(), reply)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector.parked_question.reply_unread", slog.String("taskRunID", parked.taskRun.TaskRunID), slog.String("error", errorValue.Error()))
		return ""
	}
	return reading.OptionID
}

func pressedOptionKey(answered *AnsweredChoice, interaction *AskInteraction) (string, bool) {
	if answered == nil || interaction == nil || strings.TrimSpace(answered.InteractionID) != interaction.InteractionID {
		return "", false
	}
	return offeredChoiceKey(interaction, answered.Key)
}

func carriesOwnWords(optionKey string, isPressed bool) bool {
	if isPressed {
		return false
	}
	return optionKey == approvalreply.OwnWordsOptionID || approvalgate.IsDecliningOption(optionKey)
}

func (connectorRuntime *ConnectorRuntime) approvalRequestOf(parked parkedRun) mcpserver.ApprovalRequest {
	return mcpserver.ApprovalRequest{
		RequesterPersonID: parked.taskRun.RequesterPersonID,
		TaskRunID:         parked.taskRun.TaskRunID,
		ToolName:          parked.hold.Call.ToolName,
		ToolInput:         parked.hold.Call.ToolInput,
		Prompt:            parked.taskRun.Prompt,
		Platform:          parked.posted.Platform,
		ConversationID:    firstNonEmptyString(parked.posted.ConversationID, parked.taskRun.OriginConversationID),
		ReplyTargetID:     firstNonEmptyString(parked.taskRun.OriginReplyTargetID, parked.posted.ReplyTargetID),
	}
}

func (connectorRuntime *ConnectorRuntime) AnswerParkedQuestionAsOperator(ctx context.Context, taskRunID string, signal agentcontract.ApprovalSignal) (approvalgate.AnswerSettlement, error) {
	taskRun, _ := connectorRuntime.taskRunService.FindTaskRun(taskRunID)
	parked, isParked := connectorRuntime.parkedRunOf(taskRun)
	if !isParked {
		return approvalgate.AnswerSettlement{}, ErrRunIsNotParked
	}
	optionKey, errorValue := operatorOptionKey(parked.hold, signal)
	if errorValue != nil {
		return approvalgate.AnswerSettlement{}, errorValue
	}
	settlement, errorValue := connectorRuntime.approvalGate.SettleAnswer(ctx, connectorRuntime.approvalRequestOf(parked), optionKey, operatorAnswerSource)
	if errorValue != nil {
		return approvalgate.AnswerSettlement{}, errorValue
	}
	connectorRuntime.recordOperatorAnswer(taskRunID, signal)
	connectorRuntime.publishQuestionResolution(ctx, parked, optionKey)
	return settlement, nil
}

func operatorOptionKey(hold holdrecord.Hold, signal agentcontract.ApprovalSignal) (string, error) {
	if len(hold.Choices) == 0 && signal == agentcontract.ApprovalSignalApprove {
		return approvalgate.ApproveOptionKey, nil
	}
	if len(hold.Choices) == 0 {
		return approvalgate.RejectOptionKey, nil
	}
	if signal == agentcontract.ApprovalSignalApprove {
		return "", ErrHoldOffersChoices
	}
	return approvalgate.CancelChoiceKey, nil
}

func (connectorRuntime *ConnectorRuntime) recordOperatorAnswer(taskRunID string, signal agentcontract.ApprovalSignal) {
	connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventConfirmationReplyClassified, agentruntime.MarshalBody(map[string]any{
		"approval": signal,
		"source":   operatorAnswerSource,
	}))
}

func (connectorRuntime *ConnectorRuntime) AnswerParkedHandoff(ctx context.Context, personID string, handoffID string, optionKey string) ParkedAnswer {
	for _, parked := range connectorRuntime.parkedRunsOf(personID) {
		if strings.TrimSpace(handoffID) == "" || parked.hold.Call.HandoffID != strings.TrimSpace(handoffID) {
			continue
		}
		if !isOfferedOption(parked.question(), optionKey) {
			return ParkedAnswer{}
		}
		return connectorRuntime.settleParkedRun(ctx, parked, optionKey, handoffAnswerSource)
	}
	return ParkedAnswer{}
}

func isOfferedOption(question approvalreply.Question, optionKey string) bool {
	for _, option := range question.Options {
		if option.ID == strings.TrimSpace(optionKey) {
			return true
		}
	}
	return false
}

type UnpostedQuestion struct {
	ApprovalRequest mcpserver.ApprovalRequest
	Question        approvalgate.ParkedQuestion
}

func (connectorRuntime *ConnectorRuntime) QuestionsNeverPosted(personID string, conversationID string) []UnpostedQuestion {
	unposted := []UnpostedQuestion{}
	for _, parked := range connectorRuntime.parkedRunsOf(personID) {
		if parked.posted.HoldID != "" || parked.taskRun.OriginConversationID != conversationID {
			continue
		}
		approvalRequest := connectorRuntime.approvalRequestOf(parked)
		approvalRequest.ConversationID = conversationID
		unposted = append(unposted, UnpostedQuestion{
			ApprovalRequest: approvalRequest,
			Question:        approvalgate.ParkedQuestion{HoldID: parked.hold.ID, Confirmation: parked.hold.Call.Confirmation, Choices: parked.hold.Choices, HandoffID: parked.hold.Call.HandoffID},
		})
	}
	return unposted
}

func (connectorRuntime *ConnectorRuntime) postUnpostedQuestions(ctx context.Context) {
	for _, taskRun := range connectorRuntime.taskRunService.ListTaskRun() {
		connectorRuntime.postQuestionNeverPosted(ctx, taskRun)
	}
}

func (connectorRuntime *ConnectorRuntime) postQuestionNeverPosted(ctx context.Context, taskRun task.TaskRun) {
	parked, isParked := connectorRuntime.parkedRunOf(taskRun)
	if !isParked || parked.posted.HoldID != "" {
		return
	}
	turn, isReady := connectorRuntime.questionTurnOfParkedRun(ctx, parked)
	if !isReady {
		connectorRuntime.logger.Warn("connector.parked_question.nowhere_to_post", slog.String("taskRunID", taskRun.TaskRunID))
		return
	}
	if errorValue := connectorRuntime.postParkedQuestion(withConnectorEvent(ctx, turn.event), turn, taskRun.TaskRunID, parked.hold.ID); errorValue != nil {
		connectorRuntime.logger.Warn("connector."+turn.platform+".parked_question.still_unposted", slog.String("taskRunID", taskRun.TaskRunID), slog.String("error", errorValue.Error()))
	}
}

func (connectorRuntime *ConnectorRuntime) questionTurnOfParkedRun(ctx context.Context, parked parkedRun) (*inboundTurn, bool) {
	launchContext, isFound := interruptedTaskLaunchContextFromEvents(parked.taskRun, parked.taskEvents)
	if !isFound {
		return connectorRuntime.requesterDirectMessageTurn(ctx, parked.taskRun.RequesterPersonID)
	}
	adapter, errorValue := connectorRuntime.findAdapter(launchContext.Platform)
	if errorValue != nil {
		return nil, false
	}
	event := interruptedTaskResumeEvent(parked.taskRun, launchContext)
	return &inboundTurn{
		adapter:     adapter,
		platform:    launchContext.Platform,
		event:       event,
		replyTarget: ReplyTarget{ConversationID: event.ConversationID, ReplyTargetID: event.ReplyTargetID, DedupeKey: event.DedupeKey()},
		sendReply:   connectorRuntime.recordingDelivery(adapter.SendReply),
	}, true
}

func (connectorRuntime *ConnectorRuntime) resumeParkedRun(ctx context.Context, turn *inboundTurn, answer ParkedAnswer) (ConnectorRuntimeResult, error) {
	taskEvents := connectorRuntime.taskRunService.ListTaskEvent(answer.TaskRun.TaskRunID)
	launchContext := resumeLaunchContextOf(answer.TaskRun, taskEvents, turn)
	adapter, errorValue := connectorRuntime.findAdapter(launchContext.Platform)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}
	result, errorValue := connectorRuntime.resumeTaskRun(ctx, answer.TaskRun, taskEvents, launchContext, adapter, answeredHoldTaskProfile(answer.TaskRun.TaskRunID), answer.SettledCalls)
	result.Reason = firstNonEmptyString(result.Reason, ParkedQuestionAnsweredReason)
	return result, errorValue
}

func resumeLaunchContextOf(taskRun task.TaskRun, taskEvents []task.TaskEvent, turn *inboundTurn) interruptedTaskLaunchContext {
	if launchContext, isFound := interruptedTaskLaunchContextFromEvents(taskRun, taskEvents); isFound {
		return launchContext
	}
	return interruptedTaskLaunchContext{
		ProfileName:      "default",
		ConversationID:   turn.event.ConversationID,
		ReplyTargetID:    turn.event.ReplyTargetID,
		ConversationType: turn.event.Context.ConversationType,
		Platform:         turn.platform,
	}
}

func (connectorRuntime *ConnectorRuntime) isReplyToParkedQuestion(event PlatformInboundEvent) bool {
	personID, isFound := connectorRuntime.identityService.ResolvePersonIDByPlatformAccount(event.Platform, event.SenderID)
	if !isFound {
		return false
	}
	_, isParked := connectorRuntime.parkedRunAwaiting(personID, event)
	return isParked
}

func (connectorRuntime *ConnectorRuntime) hasPendingHold(taskRunID string) bool {
	_, isPending := approvalgate.PendingHeldCall(connectorRuntime.taskRunService.ListTaskEvent(taskRunID))
	return isPending
}
