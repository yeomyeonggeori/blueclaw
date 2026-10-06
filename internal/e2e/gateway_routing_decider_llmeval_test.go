//go:build appliance && llmeval

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

const baselineEvidencePath = "testdata/gateway_routing_baseline_evidence.json"

func TestGatewayRoutingDeciderLive(t *testing.T) {
	evidence := evaluateGatewayRouting(t, "decider", func(decisionModel model.DecisionModel) routingJudge {
		return deciderRoutingJudge{decider: inboundengagement.NewDecisionModelDecider(decisionModel, nil)}
	})
	baselineRuns := baselineRunsFor(t, evidence.Split)
	for _, run := range evidence.Runs {
		if run.WrongWayCount > fewestWrongWayErrors(baselineRuns) {
			t.Errorf("run %d: %d wrong-way errors where the baseline's best run has %d", run.Run, run.WrongWayCount, fewestWrongWayErrors(baselineRuns))
		}
	}
	if accuracy, baselineAccuracy := meanPrimaryAccuracy(evidence.Runs), meanPrimaryAccuracy(baselineRuns); accuracy < baselineAccuracy {
		t.Errorf("mean accuracy %.4f is below the baseline's %.4f", accuracy, baselineAccuracy)
	}
}

type deciderRoutingJudge struct {
	decider inboundengagement.Decider
}

func (judge deciderRoutingJudge) Judge(ctx context.Context, routing routingCase) (routingJudgement, error) {
	judgments, errorValue := judge.decider.Decide(ctx, deciderFactsFor(routing), nil)
	if errorValue != nil {
		return routingJudgement{}, errorValue
	}
	verdicts := []routingVerdict{}
	for _, judgment := range judgments {
		verdicts = append(verdicts, deciderVerdict(judgment))
	}
	return routingJudgement{Verdicts: verdicts}, nil
}

func deciderVerdict(judgment inboundengagement.Judgment) routingVerdict {
	duty := "none"
	if judgment.Addressing.DutyMatch {
		duty = judgment.Addressing.DutyName
	}
	return routingVerdict{
		Target:              string(judgment.Addressing.Target),
		ShouldRespond:       judgment.Addressing.ShouldRespond,
		HasRelatesToTask:    judgment.HasRelatesToActiveTask,
		RelatesToActiveTask: judgment.RelatesToActiveTask,
		BusyRoute:           string(judgment.BusyRoute),
		Duty:                duty,
		ReactionProbability: judgment.ReactionProbability,
	}
}

func deciderFactsFor(routing routingCase) inboundengagement.Facts {
	request := plannerRequestFor(routing)
	facts := inboundengagement.Facts{
		Messages:         request.Messages,
		ConversationType: request.ConversationType,
		VisibleContext:   request.VisibleContext,
		AgentIdentity:    request.AgentIdentity,
		Company:          request.Company,
		Duties:           agentcontract.StandingDuties(),
		EnvironmentNow:   request.EnvironmentNow,
	}
	taskFacts := inboundengagement.TaskFacts{Prompt: routing.Task.Prompt, Status: request.ActiveTask.Status, Summary: routing.Task.Summary, PostedQuestion: routing.Task.PostedQuestion}
	switch {
	case routing.Finished:
		facts.FinishedTask = &taskFacts
	case routing.Open != "":
		facts.OpenTask = &taskFacts
	}
	return facts
}

func baselineRunsFor(t *testing.T, split string) []routingRunSummary {
	t.Helper()
	document, errorValue := os.ReadFile(baselineEvidencePath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	baseline := routingEvidence{}
	if errorValue := json.Unmarshal(document, &baseline); errorValue != nil {
		t.Fatal(errorValue)
	}
	runs := []routingRunSummary{}
	for index, outcomes := range baseline.Outcomes {
		runs = append(runs, summarizeRoutingRun(index+1, outcomesOfSplit(outcomes, split)))
	}
	return runs
}

func outcomesOfSplit(outcomes []routingCaseOutcome, split string) []routingCaseOutcome {
	selected := []routingCaseOutcome{}
	for _, outcome := range outcomes {
		if strings.TrimSpace(split) == "" || outcome.Split == split {
			selected = append(selected, outcome)
		}
	}
	return selected
}

func fewestWrongWayErrors(runs []routingRunSummary) int {
	fewest := runs[0].WrongWayCount
	for _, run := range runs {
		fewest = min(fewest, run.WrongWayCount)
	}
	return fewest
}

func meanPrimaryAccuracy(runs []routingRunSummary) float64 {
	total := 0.0
	for _, run := range runs {
		total += run.PrimaryAccuracy
	}
	return total / float64(len(runs))
}
