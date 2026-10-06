//go:build appliance && llmeval && !nobundledharness

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueclaw/internal/launchfailure"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract/harnesstest"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

const (
	wiredConversationID  = "wired-conversation"
	wiredThreadID        = "wired-thread"
	wiredSenderID        = "user-1"
	wiredRequesterEmail  = "sample@example.com"
	wiredSettleTimeout   = 2 * time.Minute
	wiredPollInterval    = 50 * time.Millisecond
	wiredProcessedGrace  = 100 * time.Millisecond
	wiredFinishedAgeLead = 2 * time.Second
)

func TestGatewayRoutingWiredLive(t *testing.T) {
	cases := casesTheGatewayDecides(selectedRoutingCases(t))
	evidence := evaluateGatewayRoutingCases(t, "wired", cases, func(decisionModel model.DecisionModel) routingJudge {
		return wiredRoutingJudge{decisionModel: decisionModel}
	})
	names := routingCaseNames(cases)
	deciderOnTheSameCases := evidenceRunsOfCases(t, deciderEvidencePath, names)
	deciderOnEveryCase := evidenceRunsOfCases(t, deciderEvidencePath, nil)
	baselineOnTheSameCases := evidenceRunsOfCases(t, baselineEvidencePath, names)
	for _, run := range evidence.Runs {
		if run.WrongWayCount > fewestWrongWayErrors(deciderOnEveryCase) {
			t.Errorf("run %d: %d wrong-way errors where the decider's best run has %d", run.Run, run.WrongWayCount, fewestWrongWayErrors(deciderOnEveryCase))
		}
	}
	if accuracy, deciderAccuracy := meanPrimaryAccuracy(evidence.Runs), meanPrimaryAccuracy(deciderOnEveryCase); accuracy < deciderAccuracy {
		t.Errorf("mean accuracy %.4f is below the decider's %.4f", accuracy, deciderAccuracy)
	}
	t.Logf("the evidence of #562 on these %d cases: decider %.4f, baseline %.4f; on all of its cases: decider %.4f", len(cases), meanPrimaryAccuracy(deciderOnTheSameCases), meanPrimaryAccuracy(baselineOnTheSameCases), meanPrimaryAccuracy(deciderOnEveryCase))
}

func TestTheWiredJudgeReadsTheJudgmentsOfTheWiredPath(t *testing.T) {
	routing := routingCase{
		Name:         "a running task",
		Language:     "en",
		Conversation: "D",
		Open:         string(agentcontract.TaskStatusRunning),
		Task:         routingTask{Prompt: "build the sales table", Summary: "halfway"},
		Messages:     []routingMessage{{Text: "how far along is it?", Sender: "Sample", Expect: routingExpectation{}}},
	}
	decisionModel := &routingScriptedDecisionModel{busyRoute: string(inboundengagement.BusyRouteStatus), relates: true}

	judgement, errorValue := wiredRoutingJudge{decisionModel: decisionModel}.Judge(context.Background(), routing)

	if errorValue != nil || len(judgement.Verdicts) != 1 {
		t.Fatalf("expected one verdict: %+v %v", judgement, errorValue)
	}
	if judgement.Verdicts[0].BusyRoute != string(inboundengagement.BusyRouteStatus) || !judgement.Verdicts[0].RelatesToActiveTask {
		t.Fatalf("expected the wired path to put the running task in the facts and read the decider's answers, got %+v", judgement.Verdicts[0])
	}
	if decisionModel.sawOpenTask != "build the sales table" {
		t.Fatalf("the decision request lacked the running task, got %q", decisionModel.sawOpenTask)
	}
}

func TestADirectMessageWithNothingOpenReachesNoDecisionInTheWiredPath(t *testing.T) {
	routing := routingCase{
		Name:         "a plain direct message",
		Language:     "en",
		Conversation: "D",
		Messages:     []routingMessage{{Text: "draft the agenda", Sender: "Sample"}},
	}
	decisionModel := &routingScriptedDecisionModel{}

	judgement, errorValue := wiredRoutingJudge{decisionModel: decisionModel}.Judge(context.Background(), routing)

	if errorValue != nil || len(judgement.Verdicts) != 1 || judgement.Verdicts[0].Target != string(inboundengagement.AddressingTargetBot) || !judgement.Verdicts[0].ShouldRespond {
		t.Fatalf("expected the message to go to the agent: %+v %v", judgement, errorValue)
	}
	if decisionModel.callCount() != 0 {
		t.Fatalf("expected no decision call, got %d", decisionModel.callCount())
	}
}

type routingScriptedDecisionModel struct {
	busyRoute string
	relates   bool

	mutex       sync.Mutex
	calls       int
	sawOpenTask string
}

func (decisionModel *routingScriptedDecisionModel) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	decisionModel.mutex.Lock()
	defer decisionModel.mutex.Unlock()
	decisionModel.calls++
	decisionModel.sawOpenTask = activeTaskPromptOf(request.State)
	answers := map[string]model.DecisionAnswer{}
	for questionName, question := range request.Questions {
		answers[questionName] = decisionModel.answerTo(questionName, question)
	}
	return model.DecisionResponse{Answers: answers}, nil
}

func activeTaskPromptOf(state any) string {
	document, errorValue := json.Marshal(state)
	if errorValue != nil {
		return ""
	}
	decoded := struct {
		ActiveTask struct {
			Prompt string `json:"prompt"`
		} `json:"activeTask"`
	}{}
	if errorValue := json.Unmarshal(document, &decoded); errorValue != nil {
		return ""
	}
	return decoded.ActiveTask.Prompt
}

func (decisionModel *routingScriptedDecisionModel) answerTo(questionName string, question model.DecisionQuestion) model.DecisionAnswer {
	_, shortName, _ := strings.Cut(questionName, ".")
	switch shortName {
	case inboundengagement.QuestionRelatesToActiveTask:
		return model.DecisionAnswer{Type: question.Type, Noul: boolProbability(decisionModel.relates)}
	case inboundengagement.QuestionShouldRespond:
		return model.DecisionAnswer{Type: question.Type, Noul: 1}
	case inboundengagement.QuestionBusyRoute:
		return model.DecisionAnswer{Type: question.Type, Choice: decisionModel.busyRoute}
	case inboundengagement.QuestionReaction:
		return model.DecisionAnswer{Type: question.Type, Choice: inboundengagement.ReactionOptionNone, Probabilities: map[string]float64{inboundengagement.ReactionOptionNone: 1}}
	case inboundengagement.QuestionDuty:
		return model.DecisionAnswer{Type: question.Type, Choice: inboundengagement.DutyOptionNone}
	case inboundengagement.QuestionTarget:
		return model.DecisionAnswer{Type: question.Type, Choice: string(inboundengagement.AddressingTargetBot)}
	default:
		return model.DecisionAnswer{Type: question.Type, Choice: "eyes"}
	}
}

func boolProbability(isTrue bool) float64 {
	if isTrue {
		return 1
	}
	return 0
}

func (decisionModel *routingScriptedDecisionModel) callCount() int {
	decisionModel.mutex.Lock()
	defer decisionModel.mutex.Unlock()
	return decisionModel.calls
}

type wiredRoutingJudge struct {
	decisionModel model.DecisionModel
}

func casesTheGatewayDecides(cases []routingCase) []routingCase {
	deciding := []routingCase{}
	for _, routing := range cases {
		if !isDecidedBeforeTheGateway(routing) {
			deciding = append(deciding, routing)
		}
	}
	return deciding
}

func isDecidedBeforeTheGateway(routing routingCase) bool {
	return routing.Open == string(agentcontract.TaskStatusWaitingApproval) || routing.Open == string(agentcontract.TaskStatusWaitingUserInput)
}

func routingCaseNames(cases []routingCase) map[string]bool {
	names := map[string]bool{}
	for _, routing := range cases {
		names[routing.Group+"/"+routing.Name] = true
	}
	return names
}

func evidenceRunsOfCases(t *testing.T, evidencePath string, names map[string]bool) []routingRunSummary {
	t.Helper()
	evidence := readRoutingEvidence(t, evidencePath)
	runs := []routingRunSummary{}
	for index, outcomes := range evidence.Outcomes {
		selected := []routingCaseOutcome{}
		for _, outcome := range outcomes {
			if names == nil || names[outcome.Group+"/"+outcome.Name] {
				selected = append(selected, outcome)
			}
		}
		runs = append(runs, summarizeRoutingRun(index+1, selected))
	}
	return runs
}

var errWiredPathNeverSettled = errors.New("the wired path did not settle every message before the deadline")

func (judge wiredRoutingJudge) Judge(ctx context.Context, routing routingCase) (routingJudgement, error) {
	recorder := &recordingGateway{inner: inboundengagement.NewDecisionModelDecider(judge.decisionModel, nil)}
	queue := &wiredQueue{}
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	seedWiredTask(taskRunService, routing)
	runtime := newWiredRuntime(taskRunService, recorder, queue)
	events := wiredEvents(routing)
	if errorValue := enqueueWiredEvents(ctx, runtime, events); errorValue != nil {
		return routingJudgement{}, errorValue
	}
	workersContext, stopWorkers := context.WithCancel(ctx)
	defer stopWorkers()
	runtime.Start(workersContext)
	if errorValue := queue.waitUntilSettled(ctx, len(events)); errorValue != nil {
		return routingJudgement{}, errorValue
	}
	return routingJudgement{Verdicts: wiredVerdicts(routing, events, recorder)}, nil
}

func newWiredRuntime(taskRunService *task.TaskRunService, recorder *recordingGateway, queue *wiredQueue) *connectors.ConnectorRuntime {
	harness := harnesstest.New(taskRunService)
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "ok"}
	identityService := identity.NewIdentityService(virtualPolicyProjection(false))
	identityService.RememberPlatformAccount(identity.PlatformAccountIdentity{Platform: "virtual", ExternalUserID: wiredSenderID, Email: wiredRequesterEmail})
	runtime := connectors.NewConnectorRuntime(identityService, harness, taskRunService, task.NewTaskEventService(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	runtime.UseAgentIdentityProvider(func() agentcontract.AgentIdentity {
		return agentcontract.AgentIdentity{Name: "김인턴", Handle: "internkim"}
	})
	runtime.UseCompanyProvider(func() agentcontract.CompanyContext {
		return agentcontract.CompanyContext{Name: "예시상사", TimeZone: "Asia/Seoul"}
	})
	runtime.UseGatewayDecider(recorder)
	runtime.UseReplyGenerator(harness)
	runtime.UseTaskRunService(taskRunService)
	runtime.UseLaunchFailureCompleter(launchfailure.NewCompleter(taskRunService, nil))
	runtime.UseEventRepository(queue)
	runtime.RegisterAdapter(&virtualAdapter{})
	return runtime
}

func seedWiredTask(taskRunService *task.TaskRunService, routing routingCase) {
	if routing.Open == "" && !routing.Finished {
		return
	}
	origin := task.TaskRunOrigin{ConversationID: wiredConversationID, ReplyTargetID: wiredThreadID, IsThread: true}
	taskRun := taskRunService.CreateTaskRunWithOrigin("person-1", origin, routing.Task.Prompt)
	advanced, _ := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "planner")
	progressName, progressBody, _ := strings.Cut(routing.Task.Summary, " ")
	taskRunService.AppendTaskEvent(advanced.TaskRunID, progressName, progressBody)
	if routing.Finished {
		_, _ = taskRunService.CompleteTaskRun(advanced.TaskRunID, routing.Task.Summary)
		return
	}
	taskRunService.RegisterTaskRunCancel(advanced.TaskRunID, func() {})
}

func wiredEvents(routing routingCase) []connectors.PlatformInboundEvent {
	receivedAt := time.Now().Add(-wiredFinishedAgeLead)
	isThread := true
	events := []connectors.PlatformInboundEvent{}
	for index, message := range routing.Messages {
		events = append(events, connectors.PlatformInboundEvent{
			Platform:       "virtual",
			Source:         "wired-eval",
			ConversationID: wiredConversationID,
			MessageID:      plannerMessageID(index),
			SenderID:       wiredSenderID,
			ReplyTargetID:  wiredThreadID,
			IsThread:       &isThread,
			Prompt:         message.Text,
			RawReceivedAt:  receivedAt.Add(time.Duration(index) * time.Second),
			Context:        wiredVisibleContext(routing, message, receivedAt),
		})
	}
	return events
}

func wiredVisibleContext(routing routingCase, message routingMessage, receivedAt time.Time) connectors.VisibleContext {
	visibleContext := connectors.VisibleContext{
		ConversationType: routing.Conversation,
		Sender:           connectors.VisibleContextSender{Platform: "virtual", SenderID: wiredSenderID, Name: message.Sender, Handle: message.Sender},
		Addressing:       connectors.AddressingMetadata{BotMentioned: message.Mentioned},
	}
	for index, contextMessage := range routing.Context {
		visibleContext.Messages = append(visibleContext.Messages, connectors.VisibleContextMessage{
			Speaker: contextMessage.Speaker,
			Text:    contextMessage.Text,
			SentAt:  receivedAt.Add(-time.Duration(len(routing.Context)-index) * time.Minute),
		})
	}
	return visibleContext
}

func enqueueWiredEvents(ctx context.Context, runtime *connectors.ConnectorRuntime, events []connectors.PlatformInboundEvent) error {
	adapter := &virtualAdapter{}
	for _, event := range events {
		if _, errorValue := runtime.HandleInboundEvent(ctx, adapter, event); errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func wiredVerdicts(routing routingCase, events []connectors.PlatformInboundEvent, recorder *recordingGateway) []routingVerdict {
	verdicts := []routingVerdict{}
	for _, event := range events {
		judgment, isJudged := recorder.judgmentFor(event.MessageID)
		if !isJudged {
			verdicts = append(verdicts, routingVerdict{Target: string(inboundengagement.AddressingTargetBot), ShouldRespond: true})
			continue
		}
		verdicts = append(verdicts, deciderVerdict(judgment))
	}
	return verdicts
}

type recordingGateway struct {
	inner inboundengagement.Decider

	mutex     sync.Mutex
	judgments map[string]inboundengagement.Judgment
}

func (gateway *recordingGateway) Decide(ctx context.Context, facts inboundengagement.Facts, observe agentcontract.LLMCallObserver) ([]inboundengagement.Judgment, error) {
	judgments, errorValue := gateway.inner.Decide(ctx, facts, observe)
	gateway.mutex.Lock()
	defer gateway.mutex.Unlock()
	if gateway.judgments == nil {
		gateway.judgments = map[string]inboundengagement.Judgment{}
	}
	for _, judgment := range judgments {
		if _, isKnown := gateway.judgments[judgment.MessageID]; !isKnown {
			gateway.judgments[judgment.MessageID] = judgment
		}
	}
	return judgments, errorValue
}

func (gateway *recordingGateway) FitsBurstBudget(facts inboundengagement.Facts) bool {
	return gateway.inner.FitsBurstBudget(facts)
}

func (gateway *recordingGateway) judgmentFor(messageID string) (inboundengagement.Judgment, bool) {
	gateway.mutex.Lock()
	defer gateway.mutex.Unlock()
	judgment, isJudged := gateway.judgments[messageID]
	return judgment, isJudged
}

type wiredQueue struct {
	mutex     sync.Mutex
	pending   []connectors.QueuedConnectorEvent
	processed int
}

func (queue *wiredQueue) TryInsertConnectorEvent(connectors.PlatformInboundEvent) (bool, connectors.ConnectorRuntimeResult, error) {
	return false, connectors.ConnectorRuntimeResult{}, nil
}

func (queue *wiredQueue) SaveConnectorResult(connectors.PlatformInboundEvent, connectors.ConnectorRuntimeResult) error {
	return nil
}

func (queue *wiredQueue) TryEnqueueConnectorEvent(event connectors.PlatformInboundEvent) (bool, connectors.ConnectorRuntimeResult, error) {
	queue.mutex.Lock()
	defer queue.mutex.Unlock()
	queue.pending = append(queue.pending, connectors.QueuedConnectorEvent{Event: event})
	return false, connectors.ConnectorRuntimeResult{}, nil
}

func (queue *wiredQueue) ClaimPendingConnectorEvents(limit int, _ time.Duration) ([]connectors.QueuedConnectorEvent, error) {
	queue.mutex.Lock()
	defer queue.mutex.Unlock()
	claimed := queue.pending[:min(limit, len(queue.pending))]
	queue.pending = queue.pending[len(claimed):]
	return append([]connectors.QueuedConnectorEvent{}, claimed...), nil
}

func (queue *wiredQueue) MarkConnectorEventSucceeded(connectors.PlatformInboundEvent, connectors.ConnectorRuntimeResult) error {
	queue.markProcessed()
	return nil
}

func (queue *wiredQueue) MarkConnectorEventFailed(connectors.QueuedConnectorEvent, error, time.Time) error {
	queue.markProcessed()
	return nil
}

func (queue *wiredQueue) ReleaseConnectorEventClaim(queued connectors.QueuedConnectorEvent, _ time.Time) error {
	queue.mutex.Lock()
	defer queue.mutex.Unlock()
	queue.pending = append(queue.pending, queued)
	return nil
}

func (queue *wiredQueue) markProcessed() {
	queue.mutex.Lock()
	defer queue.mutex.Unlock()
	queue.processed++
}

func (queue *wiredQueue) processedCount() int {
	queue.mutex.Lock()
	defer queue.mutex.Unlock()
	return queue.processed
}

func (queue *wiredQueue) waitUntilSettled(ctx context.Context, eventCount int) error {
	deadline := time.NewTimer(wiredSettleTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(wiredPollInterval)
	defer ticker.Stop()
	for queue.processedCount() < eventCount {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errWiredPathNeverSettled
		case <-ticker.C:
		}
	}
	return nil
}
