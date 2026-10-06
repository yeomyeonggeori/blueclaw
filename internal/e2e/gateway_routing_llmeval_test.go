//go:build appliance && llmeval

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/model/decisions"
)

const (
	routingCasesPath           = "testdata/gateway_routing_cases.json"
	routingRunCountVariable    = "BLUECLAW_ROUTING_RUNS"
	routingSplitVariable       = "BLUECLAW_ROUTING_SPLIT"
	routingEvidenceDirectory   = "testdata"
	defaultRoutingRunCount     = 3
	routingParallelism         = 4
	routingTransientRetryCount = 2
)

type routingExpectation struct {
	Target              *string `json:"target,omitempty"`
	ShouldRespond       *bool   `json:"shouldRespond,omitempty"`
	RelatesToActiveTask *bool   `json:"relatesToActiveTask,omitempty"`
	BusyRoute           *string `json:"busyRoute,omitempty"`
	Duty                *string `json:"duty,omitempty"`
	Reaction            *bool   `json:"reaction,omitempty"`
}

type routingMessage struct {
	Text      string             `json:"text"`
	Sender    string             `json:"sender"`
	Mentioned bool               `json:"mentioned"`
	Expect    routingExpectation `json:"expect"`
}

type routingTask struct {
	Prompt         string   `json:"prompt"`
	PostedQuestion string   `json:"postedQuestion"`
	Summary        string   `json:"summary"`
	Options        []string `json:"options"`
}

type routingContextMessage struct {
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
}

type routingCase struct {
	Name         string                  `json:"name"`
	Group        string                  `json:"group"`
	Split        string                  `json:"split"`
	Language     string                  `json:"language"`
	Conversation string                  `json:"conversation"`
	Open         string                  `json:"open"`
	Finished     bool                    `json:"finished"`
	Task         routingTask             `json:"task"`
	Context      []routingContextMessage `json:"context"`
	Messages     []routingMessage        `json:"messages"`
}

type routingVerdict struct {
	Target              string  `json:"target"`
	ShouldRespond       bool    `json:"shouldRespond"`
	HasRelatesToTask    bool    `json:"hasRelatesToActiveTask"`
	RelatesToActiveTask bool    `json:"relatesToActiveTask"`
	BusyRoute           string  `json:"busyRoute"`
	Duty                string  `json:"duty"`
	ReactionProbability float64 `json:"reactionProbability"`
}

type routingCall struct {
	LatencyMS    int64 `json:"latencyMs"`
	PromptTokens int64 `json:"promptTokens"`
	Attempts     int   `json:"attempts"`
}

type routingJudgement struct {
	Verdicts []routingVerdict
	Calls    []routingCall
}

type routingJudge interface {
	Judge(ctx context.Context, routing routingCase) (routingJudgement, error)
}

type routingJudgeFactory func(decisionModel model.DecisionModel) routingJudge

func TestGatewayRoutingCasesAreWellFormed(t *testing.T) {
	cases := loadRoutingCases(t)
	if len(cases) < 80 {
		t.Fatalf("the routing case set holds %d cases and should hold about 80", len(cases))
	}
	seenNames := map[string]bool{}
	for _, routing := range cases {
		if seenNames[routing.Name+routing.Group] {
			t.Errorf("%s: the name repeats within its group", routing.Name)
		}
		seenNames[routing.Name+routing.Group] = true
		if len(routing.Messages) == 0 || (routing.Split != "dev" && routing.Split != "held") {
			t.Errorf("%s: it needs a message and a dev or held split", routing.Name)
		}
		if !isKnownOpenState(routing.Open) {
			t.Errorf("%s: unknown open task state %q", routing.Name, routing.Open)
		}
	}
}

func isKnownOpenState(state string) bool {
	switch state {
	case "", string(agentcontract.TaskStatusRunning), string(agentcontract.TaskStatusWaitingApproval), string(agentcontract.TaskStatusWaitingUserInput):
		return true
	}
	return false
}

func loadRoutingCases(t *testing.T) []routingCase {
	t.Helper()
	document, errorValue := os.ReadFile(routingCasesPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	cases := []routingCase{}
	if errorValue := json.Unmarshal(document, &cases); errorValue != nil {
		t.Fatal(errorValue)
	}
	return cases
}

func selectedRoutingCases(t *testing.T) []routingCase {
	t.Helper()
	split := strings.TrimSpace(os.Getenv(routingSplitVariable))
	selected := []routingCase{}
	for _, routing := range loadRoutingCases(t) {
		if split == "" || routing.Split == split {
			selected = append(selected, routing)
		}
	}
	return selected
}

func routingRunCount(t *testing.T) int {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(routingRunCountVariable))
	if value == "" {
		return defaultRoutingRunCount
	}
	count, errorValue := strconv.Atoi(value)
	if errorValue != nil || count < 1 {
		t.Fatalf("%s must be a positive number, got %q", routingRunCountVariable, value)
	}
	return count
}

func routingDecisionModel(t *testing.T) (model.DecisionModel, decisions.Endpoint) {
	t.Helper()
	requireLiveEvaluationConsent(t)
	endpoint := decisions.Endpoint{
		URL:        requireEvaluationInput(t, "BLUECLAW_DECISION_ENDPOINT", languageModelHint),
		ModelName:  requireEvaluationInput(t, "BLUECLAW_DECISION_MODEL", languageModelHint),
		APIKey:     requireEvaluationInput(t, "OPENROUTER_API_KEY", languageModelHint),
		HTTPClient: &http.Client{Timeout: time.Minute},
	}
	return endpoint.DecisionModel(), endpoint
}

type meteredDecisionModel struct {
	inner model.DecisionModel
	mutex sync.Mutex
	calls []routingCall
}

func (metered *meteredDecisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	startedAt := time.Now()
	var response model.DecisionResponse
	var errorValue error
	attempts := 0
	for attempts <= routingTransientRetryCount {
		attempts++
		response, errorValue = metered.inner.Decide(ctx, request)
		if errorValue == nil || ctx.Err() != nil {
			break
		}
	}
	metered.mutex.Lock()
	defer metered.mutex.Unlock()
	metered.calls = append(metered.calls, routingCall{LatencyMS: time.Since(startedAt).Milliseconds(), PromptTokens: response.Usage.PromptTokens, Attempts: attempts})
	return response, errorValue
}

type routingCheck struct {
	Field     string `json:"field"`
	Expected  string `json:"expected"`
	Actual    string `json:"actual"`
	IsCorrect bool   `json:"isCorrect"`
}

type routingCaseOutcome struct {
	Name     string         `json:"name"`
	Group    string         `json:"group"`
	Split    string         `json:"split"`
	IsRight  bool           `json:"isRight"`
	Checks   []routingCheck `json:"checks"`
	WrongWay []string       `json:"wrongWay,omitempty"`
	Calls    []routingCall  `json:"calls"`
	Error    string         `json:"error,omitempty"`
}

type routingTally struct {
	Correct int `json:"correct"`
	Total   int `json:"total"`
}

func (tally routingTally) accuracy() float64 {
	if tally.Total == 0 {
		return 0
	}
	return float64(tally.Correct) / float64(tally.Total)
}

type routingRunSummary struct {
	Run                int                     `json:"run"`
	Primary            routingTally            `json:"primary"`
	PrimaryAccuracy    float64                 `json:"primaryAccuracy"`
	Dev                routingTally            `json:"dev"`
	Held               routingTally            `json:"held"`
	Secondary          routingTally            `json:"secondary"`
	CasesRight         int                     `json:"casesRight"`
	CaseCount          int                     `json:"caseCount"`
	WrongWayCount      int                     `json:"wrongWayCount"`
	ByGroup            map[string]routingTally `json:"byGroup"`
	CallCount          int                     `json:"callCount"`
	MeanLatencyMS      float64                 `json:"meanLatencyMs"`
	MeanPromptTokens   float64                 `json:"meanPromptTokens"`
	RetriedCallCount   int                     `json:"retriedCallCount"`
	FailedCaseCount    int                     `json:"failedCaseCount"`
	MeanLatencyPerCase float64                 `json:"meanLatencyPerCaseMs"`
	MeanTokensPerCase  float64                 `json:"meanPromptTokensPerCase"`
}

type routingEvidence struct {
	Judge       string                 `json:"judge"`
	DecisionURL string                 `json:"decisionEndpoint"`
	ModelName   string                 `json:"decisionModel"`
	GeneratedAt string                 `json:"generatedAt"`
	Split       string                 `json:"split"`
	Runs        []routingRunSummary    `json:"runs"`
	Outcomes    [][]routingCaseOutcome `json:"outcomes"`
}

func evaluateGatewayRouting(t *testing.T, judgeName string, newJudge routingJudgeFactory) routingEvidence {
	t.Helper()
	return evaluateGatewayRoutingCases(t, judgeName, selectedRoutingCases(t), newJudge)
}

func evaluateGatewayRoutingCases(t *testing.T, judgeName string, cases []routingCase, newJudge routingJudgeFactory) routingEvidence {
	t.Helper()
	decisionModel, endpoint := routingDecisionModel(t)
	evidence := routingEvidence{Judge: judgeName, DecisionURL: endpoint.URL, ModelName: endpoint.ModelName, GeneratedAt: time.Now().UTC().Format(time.RFC3339), Split: strings.TrimSpace(os.Getenv(routingSplitVariable))}
	for run := 1; run <= routingRunCount(t); run++ {
		outcomes := judgeEveryCase(cases, decisionModel, newJudge)
		summary := summarizeRoutingRun(run, outcomes)
		evidence.Runs = append(evidence.Runs, summary)
		evidence.Outcomes = append(evidence.Outcomes, outcomes)
		t.Logf("%s run %d: %s", judgeName, run, describeRoutingRun(summary))
	}
	writeRoutingEvidence(t, judgeName, evidence)
	return evidence
}

func judgeEveryCase(cases []routingCase, decisionModel model.DecisionModel, newJudge routingJudgeFactory) []routingCaseOutcome {
	outcomes := make([]routingCaseOutcome, len(cases))
	slots := make(chan struct{}, routingParallelism)
	waitGroup := sync.WaitGroup{}
	for index, routing := range cases {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			outcomes[index] = judgeOneCase(routing, decisionModel, newJudge)
		}()
	}
	waitGroup.Wait()
	return outcomes
}

func judgeOneCase(routing routingCase, decisionModel model.DecisionModel, newJudge routingJudgeFactory) routingCaseOutcome {
	metered := &meteredDecisionModel{inner: decisionModel}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	judgement, errorValue := newJudge(metered).Judge(ctx, routing)
	outcome := routingCaseOutcome{Name: routing.Name, Group: routing.Group, Split: routing.Split, Calls: metered.calls}
	if errorValue != nil {
		outcome.Error = errorValue.Error()
		outcome.Checks = failedChecks(routing)
		return outcome
	}
	outcome.Checks, outcome.WrongWay = checkRoutingVerdicts(routing, judgement.Verdicts)
	outcome.IsRight = allChecksCorrect(outcome.Checks)
	return outcome
}

func failedChecks(routing routingCase) []routingCheck {
	checks := []routingCheck{}
	for _, message := range routing.Messages {
		for _, check := range expectedChecks(message.Expect) {
			check.Actual = "no answer"
			checks = append(checks, check)
		}
	}
	return checks
}

func allChecksCorrect(checks []routingCheck) bool {
	for _, check := range checks {
		if !check.IsCorrect {
			return false
		}
	}
	return true
}

func expectedChecks(expectation routingExpectation) []routingCheck {
	checks := []routingCheck{}
	if expectation.Target != nil {
		checks = append(checks, routingCheck{Field: "target", Expected: *expectation.Target})
	}
	if expectation.ShouldRespond != nil {
		checks = append(checks, routingCheck{Field: "shouldRespond", Expected: strconv.FormatBool(*expectation.ShouldRespond)})
	}
	if expectation.RelatesToActiveTask != nil {
		checks = append(checks, routingCheck{Field: "relatesToActiveTask", Expected: strconv.FormatBool(*expectation.RelatesToActiveTask)})
	}
	if expectation.BusyRoute != nil {
		checks = append(checks, routingCheck{Field: "busyRoute", Expected: *expectation.BusyRoute})
	}
	if expectation.Duty != nil {
		checks = append(checks, routingCheck{Field: "duty", Expected: *expectation.Duty})
	}
	if expectation.Reaction != nil {
		checks = append(checks, routingCheck{Field: "reaction", Expected: strconv.FormatBool(*expectation.Reaction)})
	}
	return checks
}

func actualRoutingValue(field string, verdict routingVerdict) string {
	switch field {
	case "target":
		return verdict.Target
	case "shouldRespond":
		return strconv.FormatBool(verdict.ShouldRespond)
	case "relatesToActiveTask":
		return strconv.FormatBool(verdict.HasRelatesToTask && verdict.RelatesToActiveTask)
	case "busyRoute":
		return verdict.BusyRoute
	case "duty":
		return verdict.Duty
	default:
		return strconv.FormatBool(verdict.ReactionProbability >= 0.5)
	}
}

func checkRoutingVerdicts(routing routingCase, verdicts []routingVerdict) ([]routingCheck, []string) {
	checks := []routingCheck{}
	wrongWay := []string{}
	for index, message := range routing.Messages {
		if index >= len(verdicts) {
			for _, check := range expectedChecks(message.Expect) {
				check.Actual = "no answer"
				checks = append(checks, check)
			}
			continue
		}
		for _, check := range expectedChecks(message.Expect) {
			check.Actual = actualRoutingValue(check.Field, verdicts[index])
			check.IsCorrect = check.Actual == check.Expected
			checks = append(checks, check)
		}
		wrongWay = append(wrongWay, wrongWayErrors(message, verdicts[index])...)
	}
	return checks, wrongWay
}

func wrongWayErrors(message routingMessage, verdict routingVerdict) []string {
	errors := []string{}
	expectation := message.Expect
	if expectation.BusyRoute != nil && isGentleBusyRoute(*expectation.BusyRoute) && isDestructiveBusyRoute(verdict.BusyRoute) {
		errors = append(errors, fmt.Sprintf("%q: %s where the truth is %s", message.Text, verdict.BusyRoute, *expectation.BusyRoute))
	}
	if expectation.Target != nil && *expectation.Target == "human" && verdict.ShouldRespond {
		errors = append(errors, fmt.Sprintf("%q: responds to a human-to-human message", message.Text))
	}
	if expectation.Target != nil && *expectation.Target == "bot" && expectation.ShouldRespond != nil && *expectation.ShouldRespond && !verdict.ShouldRespond {
		errors = append(errors, fmt.Sprintf("%q: ignores a message addressed to the agent", message.Text))
	}
	return errors
}

func isGentleBusyRoute(route string) bool {
	return route == string(inboundengagement.BusyRouteSteer) || route == string(inboundengagement.BusyRouteStatus)
}

func isDestructiveBusyRoute(route string) bool {
	switch route {
	case string(inboundengagement.BusyRouteCancel), string(inboundengagement.BusyRouteReplace), string(inboundengagement.BusyRouteNewTask):
		return true
	}
	return false
}

func isPrimaryRoutingField(field string) bool {
	return field != "duty" && field != "reaction"
}

func summarizeRoutingRun(run int, outcomes []routingCaseOutcome) routingRunSummary {
	summary := routingRunSummary{Run: run, CaseCount: len(outcomes), ByGroup: map[string]routingTally{}}
	var latencyTotal, tokenTotal int64
	for _, outcome := range outcomes {
		if outcome.IsRight {
			summary.CasesRight++
		}
		if outcome.Error != "" {
			summary.FailedCaseCount++
		}
		summary.WrongWayCount += len(outcome.WrongWay)
		tallyOutcomeChecks(&summary, outcome)
		for _, call := range outcome.Calls {
			summary.CallCount++
			latencyTotal += call.LatencyMS
			tokenTotal += call.PromptTokens
			if call.Attempts > 1 {
				summary.RetriedCallCount++
			}
		}
	}
	summary.PrimaryAccuracy = summary.Primary.accuracy()
	if summary.CallCount > 0 {
		summary.MeanLatencyMS = float64(latencyTotal) / float64(summary.CallCount)
		summary.MeanPromptTokens = float64(tokenTotal) / float64(summary.CallCount)
	}
	if summary.CaseCount > 0 {
		summary.MeanLatencyPerCase = float64(latencyTotal) / float64(summary.CaseCount)
		summary.MeanTokensPerCase = float64(tokenTotal) / float64(summary.CaseCount)
	}
	return summary
}

func tallyOutcomeChecks(summary *routingRunSummary, outcome routingCaseOutcome) {
	for _, check := range outcome.Checks {
		if !isPrimaryRoutingField(check.Field) {
			summary.Secondary = addToTally(summary.Secondary, check.IsCorrect)
			continue
		}
		summary.Primary = addToTally(summary.Primary, check.IsCorrect)
		summary.ByGroup[outcome.Group] = addToTally(summary.ByGroup[outcome.Group], check.IsCorrect)
		if outcome.Split == "held" {
			summary.Held = addToTally(summary.Held, check.IsCorrect)
		} else {
			summary.Dev = addToTally(summary.Dev, check.IsCorrect)
		}
	}
}

func addToTally(tally routingTally, isCorrect bool) routingTally {
	tally.Total++
	if isCorrect {
		tally.Correct++
	}
	return tally
}

func describeRoutingRun(summary routingRunSummary) string {
	groups := []string{}
	for group, tally := range summary.ByGroup {
		groups = append(groups, fmt.Sprintf("%s %d/%d", group, tally.Correct, tally.Total))
	}
	sort.Strings(groups)
	return fmt.Sprintf("primary %d/%d (dev %d/%d, held %d/%d), secondary %d/%d, cases right %d/%d, wrong-way %d, calls %d, %.0f ms and %.0f prompt tokens per call, failed %d, retried %d; %s",
		summary.Primary.Correct, summary.Primary.Total, summary.Dev.Correct, summary.Dev.Total, summary.Held.Correct, summary.Held.Total,
		summary.Secondary.Correct, summary.Secondary.Total, summary.CasesRight, summary.CaseCount, summary.WrongWayCount, summary.CallCount,
		summary.MeanLatencyMS, summary.MeanPromptTokens, summary.FailedCaseCount, summary.RetriedCallCount, strings.Join(groups, ", "))
}

func writeRoutingEvidence(t *testing.T, judgeName string, evidence routingEvidence) {
	t.Helper()
	directory := firstNonEmptyTestString(os.Getenv("BLUECLAW_E2E_ARTIFACT_DIR"), filepath.Join("..", "..", ".artifacts", "gateway-routing-live"))
	if errorValue := os.MkdirAll(directory, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	document, errorValue := json.MarshalIndent(evidence, "", " ")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	evidencePath := filepath.Join(directory, "gateway-routing-"+judgeName+".json")
	if errorValue := os.WriteFile(evidencePath, document, 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Logf("gateway routing evidence: %s", evidencePath)
}

func plannerMessageID(index int) string {
	return "message-" + strconv.Itoa(index+1)
}

func deciderFactsFor(routing routingCase) inboundengagement.Facts {
	now := time.Now()
	facts := inboundengagement.Facts{
		Messages:         gatewayMessages(routing, now),
		ConversationType: routing.Conversation,
		VisibleContext:   visibleContextFor(routing, now),
		AgentIdentity:    agentcontract.AgentIdentity{Name: "김인턴", Handle: "internkim"},
		Company:          agentcontract.CompanyContext{Name: "예시상사", TimeZone: "Asia/Seoul"},
		Duties:           inboundengagement.StandingDuties(),
		EnvironmentNow:   now,
	}
	taskFacts := inboundengagement.TaskFacts{Prompt: routing.Task.Prompt, Status: openTaskStatus(routing), Summary: routing.Task.Summary, PostedQuestion: routing.Task.PostedQuestion}
	switch {
	case routing.Finished:
		facts.FinishedTask = &taskFacts
	case routing.Open != "":
		facts.OpenTask = &taskFacts
	}
	return facts
}

func openTaskStatus(routing routingCase) string {
	if routing.Finished {
		return "completed"
	}
	return routing.Open
}

func gatewayMessages(routing routingCase, now time.Time) []inboundengagement.Message {
	messages := []inboundengagement.Message{}
	for index, message := range routing.Messages {
		messages = append(messages, inboundengagement.Message{
			MessageID:    plannerMessageID(index),
			Prompt:       message.Text,
			SenderName:   message.Sender,
			SenderHandle: message.Sender,
			BotMentioned: message.Mentioned,
			SentAt:       now,
		})
	}
	return messages
}

func visibleContextFor(routing routingCase, now time.Time) agentcontract.VisibleContext {
	visibleContext := agentcontract.VisibleContext{}
	for index, message := range routing.Context {
		visibleContext.Messages = append(visibleContext.Messages, agentcontract.VisibleContextMessage{
			Speaker: message.Speaker,
			Text:    message.Text,
			SentAt:  now.Add(-time.Duration(len(routing.Context)-index) * time.Minute),
		})
	}
	return visibleContext
}
