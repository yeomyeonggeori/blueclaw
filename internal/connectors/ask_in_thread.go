package connectors

import (
	"context"
	"errors"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalrecord"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"
)

const (
	approvalExpiry                 = 24 * time.Hour
	ApprovalAnsweredInThreadReason = "approval_answered_in_thread"
)

type askingThread struct {
	taskRunID         string
	requesterPersonID string
	platform          string
	conversationID    string
	replyTargetID     string
	question          approvalreply.Question
	operatorOptions   operatorOptions
	answers           chan string
	isClaimed         bool
	isPosted          bool
}

type operatorOptions struct {
	approveOptionID string
	rejectOptionID  string
}

type askingThreads struct {
	mutex   sync.Mutex
	expiry  time.Duration
	threads []*askingThread
}

func (threads *askingThreads) join(thread *askingThread) func() {
	threads.mutex.Lock()
	defer threads.mutex.Unlock()
	threads.threads = append(threads.threads, thread)
	return func() { threads.leave(thread) }
}

func (threads *askingThreads) leave(departing *askingThread) {
	threads.mutex.Lock()
	defer threads.mutex.Unlock()
	remaining := make([]*askingThread, 0, len(threads.threads))
	for _, thread := range threads.threads {
		if thread != departing {
			remaining = append(remaining, thread)
		}
	}
	threads.threads = remaining
}

func (threads *askingThreads) awaiting(platform string, conversationID string, personID string) []*askingThread {
	threads.mutex.Lock()
	defer threads.mutex.Unlock()
	matching := []*askingThread{}
	for _, thread := range threads.threads {
		if thread.platform == platform && thread.conversationID == conversationID && thread.requesterPersonID == personID && !thread.isClaimed {
			matching = append(matching, thread)
		}
	}
	return matching
}

func (threads *askingThreads) isAwaiting(taskRunID string) bool {
	threads.mutex.Lock()
	defer threads.mutex.Unlock()
	for _, thread := range threads.threads {
		if thread.taskRunID == taskRunID {
			return true
		}
	}
	return false
}

func (threads *askingThreads) awaitingRun(taskRunID string) (*askingThread, bool) {
	threads.mutex.Lock()
	defer threads.mutex.Unlock()
	for _, thread := range threads.threads {
		if thread.taskRunID == taskRunID && !thread.isClaimed {
			return thread, true
		}
	}
	return nil, false
}

func (threads *askingThreads) markPosted(thread *askingThread) {
	threads.mutex.Lock()
	defer threads.mutex.Unlock()
	thread.isPosted = true
}

func (threads *askingThreads) firstPosted() (*askingThread, bool) {
	threads.mutex.Lock()
	defer threads.mutex.Unlock()
	for _, thread := range threads.threads {
		if thread.isPosted && !thread.isClaimed {
			return thread, true
		}
	}
	return nil, false
}

func (threads *askingThreads) claim(thread *askingThread) bool {
	threads.mutex.Lock()
	defer threads.mutex.Unlock()
	if thread.isClaimed {
		return false
	}
	thread.isClaimed = true
	return true
}

func (connectorRuntime *ConnectorRuntime) ThreadPermissionAsker() approvalgate.PermissionAsker {
	return threadPermissionAsker{connectorRuntime: connectorRuntime}
}

func (connectorRuntime *ConnectorRuntime) AwaitedQuestion() (taskRunID string, dispatchID string, isAwaited bool) {
	thread, isAwaited := connectorRuntime.askingThreads.firstPosted()
	if !isAwaited {
		return "", "", false
	}
	return thread.taskRunID, PostedApprovalQuestionMessageID(connectorRuntime.taskRunService.ListTaskEvent(thread.taskRunID)), true
}

func (connectorRuntime *ConnectorRuntime) isAwaitedInThread(taskRunID string) bool {
	return connectorRuntime.askingThreads.isAwaiting(taskRunID)
}

type threadPermissionAsker struct {
	connectorRuntime *ConnectorRuntime
}

func (asker threadPermissionAsker) AskPermission(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question approvalgate.PermissionQuestion) (approvalgate.ApprovalAnswer, approvalgate.AskStatus) {
	optionID, status := asker.connectorRuntime.askWhereTheyAre(ctx, approvalRequest, question.Confirmation, approvalQuestionFor(question.Confirmation, question.Choices), operatorOptionsOfApproval(question.Choices))
	if status != approvalgate.AskAnswered {
		return approvalgate.ApprovalAnswer{}, status
	}
	return approvalAnswerOfOption(optionID), approvalgate.AskAnswered
}

func (asker threadPermissionAsker) AskHarnessPermission(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question approvalgate.HarnessPermissionQuestion) (acp.RequestPermissionOutcome, approvalgate.AskStatus) {
	readerQuestion := approvalreply.Question{Text: question.Text, Options: approvalgate.ReplyOptionsOf(question.Options)}
	optionID, status := asker.connectorRuntime.askWhereTheyAre(ctx, approvalRequest, question.Text, readerQuestion, operatorOptionsOfHarness(question.Options))
	if status != approvalgate.AskAnswered {
		return acp.RequestPermissionOutcome{}, status
	}
	return acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{Outcome: "selected", OptionId: acp.PermissionOptionId(optionID)}}, approvalgate.AskAnswered
}

func approvalAnswerOfOption(optionID string) approvalgate.ApprovalAnswer {
	decision := answeredDecision(optionID)
	if decision.Approval != nil {
		return approvalgate.ApprovalAnswer{Signal: *decision.Approval}
	}
	if optionID == approvalrecord.CancelChoiceKey {
		return approvalgate.ApprovalAnswer{Signal: agentcontract.ApprovalSignalReject}
	}
	return approvalgate.ApprovalAnswer{Signal: agentcontract.ApprovalSignalApprove, ChoiceKey: optionID}
}

func (connectorRuntime *ConnectorRuntime) askWhereTheyAre(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, confirmation string, question approvalreply.Question, operator operatorOptions) (string, approvalgate.AskStatus) {
	turn, isReady := connectorRuntime.questionTurn(ctx, approvalRequest)
	if !isReady {
		return "", approvalgate.AskUnreachable
	}
	thread := &askingThread{
		taskRunID:         approvalRequest.TaskRunID,
		requesterPersonID: approvalRequest.RequesterPersonID,
		platform:          turn.platform,
		conversationID:    turn.event.ConversationID,
		replyTargetID:     turn.event.ReplyTargetID,
		question:          question,
		operatorOptions:   operator,
		answers:           make(chan string, 1),
	}
	defer connectorRuntime.askingThreads.join(thread)()
	if connectorRuntime.deliverApprovalQuestion(ctx, turn, approvalRequest.TaskRunID, confirmation) != nil {
		return "", approvalgate.AskUnreachable
	}
	connectorRuntime.askingThreads.markPosted(thread)
	defer waitHandoffFrom(ctx).begin()()
	return connectorRuntime.awaitAnswer(ctx, thread, connectorRuntime.askingThreads.expiry)
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
	replyTarget, _ := connectorRuntime.buildReplyTarget(ctx, adapter, event)
	return &inboundTurn{
		adapter:     adapter,
		platform:    event.Platform,
		event:       event,
		replyTarget: replyTarget,
		sendReply:   connectorRuntime.pendingRequestReplySender(event.DedupeKey(), connectorRuntime.recordingDelivery(adapter.SendReply), true),
	}, true
}

func (connectorRuntime *ConnectorRuntime) awaitAnswer(ctx context.Context, thread *askingThread, patience time.Duration) (string, approvalgate.AskStatus) {
	expiry := time.NewTimer(patience)
	defer expiry.Stop()
	select {
	case optionID := <-thread.answers:
		return optionID, approvalgate.AskAnswered
	case <-ctx.Done():
		return "", approvalgate.AskInterrupted
	case <-expiry.C:
		return "", approvalgate.AskExpired
	}
}

func (connectorRuntime *ConnectorRuntime) answerAskingThread(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (ConnectorRuntimeResult, bool, error) {
	if event.TaskRetry != nil || exactTaskControlIntent(event.Prompt) != agentcontract.TaskControlIntentNone {
		return ConnectorRuntimeResult{}, false, nil
	}
	personID, isFound := connectorRuntime.identityService.ResolvePersonIDByPlatformAccount(adapter.Name(), event.SenderID)
	if !isFound {
		return ConnectorRuntimeResult{}, false, nil
	}
	for _, thread := range connectorRuntime.askingThreads.awaiting(adapter.Name(), event.ConversationID, personID) {
		result, isAnswer, errorValue := connectorRuntime.offerReplyToThread(ctx, thread, event)
		if isAnswer || errorValue != nil {
			return result, isAnswer, errorValue
		}
	}
	return ConnectorRuntimeResult{}, false, nil
}

func (connectorRuntime *ConnectorRuntime) offerReplyToThread(ctx context.Context, thread *askingThread, event PlatformInboundEvent) (ConnectorRuntimeResult, bool, error) {
	if !connectorRuntime.postedQuestionOf(thread).IsAnsweredBy(placementOf(event)) {
		return ConnectorRuntimeResult{}, false, nil
	}
	optionID, isAnswer, errorValue := connectorRuntime.readReplyToQuestion(ctx, thread.taskRunID, thread.question, event.Prompt)
	if errorValue != nil || !isAnswer || !connectorRuntime.askingThreads.claim(thread) {
		return ConnectorRuntimeResult{}, false, errorValue
	}
	connectorRuntime.recordConfirmationReplyClassified(thread.taskRunID, event, answeredDecision(optionID))
	thread.answers <- optionID
	return ConnectorRuntimeResult{Handled: true, Platform: thread.platform, TaskRunID: thread.taskRunID, Reason: ApprovalAnsweredInThreadReason}, true, nil
}

func (connectorRuntime *ConnectorRuntime) postedQuestionOf(thread *askingThread) PostedQuestion {
	return PostedQuestion{
		ConversationID: thread.conversationID,
		ReplyTargetID:  thread.replyTargetID,
		MessageID:      PostedApprovalQuestionMessageID(connectorRuntime.taskRunService.ListTaskEvent(thread.taskRunID)),
	}
}

func operatorOptionsOfApproval(choices []holdrecord.Choice) operatorOptions {
	if len(choices) > 0 {
		return operatorOptions{rejectOptionID: approvalrecord.CancelChoiceKey}
	}
	return operatorOptions{approveOptionID: ApproveOptionID, rejectOptionID: RejectOptionID}
}

func operatorOptionsOfHarness(permissionOptions []acp.PermissionOption) operatorOptions {
	options := operatorOptions{}
	for _, permissionOption := range permissionOptions {
		switch permissionOption.Kind {
		case acp.PermissionOptionKindAllowOnce:
			if options.approveOptionID == "" {
				options.approveOptionID = string(permissionOption.OptionId)
			}
		case acp.PermissionOptionKindRejectOnce:
			if options.rejectOptionID == "" {
				options.rejectOptionID = string(permissionOption.OptionId)
			}
		}
	}
	return options
}

var ErrHoldOffersChoices = errors.New("this hold asks the requester to pick an option, so only the requester can approve it; it can be rejected")

func (connectorRuntime *ConnectorRuntime) AnswerAwaitedHold(taskRunID string, signal agentcontract.ApprovalSignal) (bool, error) {
	thread, isAwaited := connectorRuntime.askingThreads.awaitingRun(taskRunID)
	if !isAwaited {
		return false, nil
	}
	optionID := thread.operatorOptions.rejectOptionID
	if signal == agentcontract.ApprovalSignalApprove {
		optionID = thread.operatorOptions.approveOptionID
	}
	if optionID == "" {
		return false, ErrHoldOffersChoices
	}
	if !connectorRuntime.askingThreads.claim(thread) {
		return true, nil
	}
	connectorRuntime.recordOperatorAnswer(taskRunID, signal)
	thread.answers <- optionID
	return true, nil
}

func (connectorRuntime *ConnectorRuntime) recordOperatorAnswer(taskRunID string, signal agentcontract.ApprovalSignal) {
	connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventConfirmationReplyClassified, agentruntime.MarshalBody(map[string]any{
		"approval": signal,
		"source":   "operator_terminal",
	}))
}
