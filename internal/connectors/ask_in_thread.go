package connectors

import (
	"context"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalrecord"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
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
	answers           chan string
	isClaimed         bool
	isPosted          bool
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

func (connectorRuntime *ConnectorRuntime) UseAskInThread(isEnabled bool) {
	if !isEnabled {
		connectorRuntime.askingThreads = nil
		return
	}
	connectorRuntime.askingThreads = &askingThreads{expiry: approvalExpiry}
}

func (connectorRuntime *ConnectorRuntime) ThreadPermissionAsker() approvalgate.PermissionAsker {
	if connectorRuntime.askingThreads == nil {
		return nil
	}
	return threadPermissionAsker{connectorRuntime: connectorRuntime}
}

func (connectorRuntime *ConnectorRuntime) AwaitedQuestion() (taskRunID string, dispatchID string, isAwaited bool) {
	if connectorRuntime.askingThreads == nil {
		return "", "", false
	}
	thread, isAwaited := connectorRuntime.askingThreads.firstPosted()
	if !isAwaited {
		return "", "", false
	}
	return thread.taskRunID, PostedApprovalQuestionMessageID(connectorRuntime.taskRunService.ListTaskEvent(thread.taskRunID)), true
}

func (connectorRuntime *ConnectorRuntime) isAwaitedInThread(taskRunID string) bool {
	return connectorRuntime.askingThreads != nil && connectorRuntime.askingThreads.isAwaiting(taskRunID)
}

type threadPermissionAsker struct {
	connectorRuntime *ConnectorRuntime
}

func (asker threadPermissionAsker) AskPermission(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question approvalgate.PermissionQuestion) (approvalgate.ApprovalAnswer, bool) {
	optionID, isAnswered := asker.connectorRuntime.askInThread(ctx, approvalRequest, question.Confirmation, approvalQuestionFor(question.Confirmation, question.Choices))
	if !isAnswered {
		return approvalgate.ApprovalAnswer{}, false
	}
	return approvalAnswerOfOption(optionID), true
}

func (asker threadPermissionAsker) AskHarnessPermission(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question approvalgate.HarnessPermissionQuestion) (acp.RequestPermissionOutcome, bool) {
	readerQuestion := approvalreply.Question{Text: question.Text, Options: approvalgate.ReplyOptionsOf(question.Options)}
	optionID, isAnswered := asker.connectorRuntime.askInThread(ctx, approvalRequest, question.Text, readerQuestion)
	if !isAnswered {
		return acp.RequestPermissionOutcome{}, false
	}
	return acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{Outcome: "selected", OptionId: acp.PermissionOptionId(optionID)}}, true
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

func (connectorRuntime *ConnectorRuntime) askInThread(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, confirmation string, question approvalreply.Question) (string, bool) {
	turn, isReady := connectorRuntime.questionTurn(ctx, approvalRequest)
	if !isReady {
		return "", false
	}
	thread := &askingThread{
		taskRunID:         approvalRequest.TaskRunID,
		requesterPersonID: approvalRequest.RequesterPersonID,
		platform:          turn.platform,
		conversationID:    turn.event.ConversationID,
		replyTargetID:     turn.event.ReplyTargetID,
		question:          question,
		answers:           make(chan string, 1),
	}
	defer connectorRuntime.askingThreads.join(thread)()
	if connectorRuntime.deliverApprovalQuestion(ctx, turn, approvalRequest.TaskRunID, confirmation) != nil {
		return "", false
	}
	connectorRuntime.askingThreads.markPosted(thread)
	defer waitHandoffFrom(ctx).begin()()
	return connectorRuntime.awaitAnswer(ctx, thread)
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

func (connectorRuntime *ConnectorRuntime) awaitAnswer(ctx context.Context, thread *askingThread) (string, bool) {
	expiry := time.NewTimer(connectorRuntime.askingThreads.expiry)
	defer expiry.Stop()
	select {
	case optionID := <-thread.answers:
		return optionID, true
	case <-ctx.Done():
		return "", false
	case <-expiry.C:
		return "", false
	}
}

func (connectorRuntime *ConnectorRuntime) answerAskingThread(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (ConnectorRuntimeResult, bool, error) {
	if connectorRuntime.askingThreads == nil || event.TaskRetry != nil || exactTaskControlIntent(event.Prompt) != agentcontract.TaskControlIntentNone {
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
