//go:build appliance && llmeval

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

// intakeDecisionCase is one message the runtime has to read correctly, with the
// answers a person would give. The corpus replaces the golden turn-router
// fixtures, which graded a chat model's prose against a schema that no longer
// exists.
type intakeDecisionCase struct {
	name                string
	request             agentcontract.IntakeDecisionRequest
	expectedRoute       agentcontract.TurnRoute
	expectedTarget      agentcontract.AddressingTarget
	expectedRespond     bool
	expectedApproval    agentcontract.ApprovalSignal
	expectedChoiceKeys  []string
	expectedRelatesTask bool
}

func TestIntakeDecisionCorpusLive(t *testing.T) {
	if !truthyEnvironmentValue(os.Getenv("BLUECLAW_E2E_LIVE")) {
		t.Skip("set BLUECLAW_E2E_LIVE=1 to run the costed intake decision evaluation")
	}
	endpoint := strings.TrimSpace(os.Getenv("BLUECLAW_DECISION_ENDPOINT"))
	socketPath := strings.TrimSpace(os.Getenv("BLUECLAW_DECISION_UNIX_SOCKET"))
	if endpoint == "" && socketPath == "" {
		t.Skip("set BLUECLAW_DECISION_ENDPOINT or BLUECLAW_DECISION_UNIX_SOCKET to run the intake decision evaluation")
	}
	decisionModel := llm.CapabilityDecisionClient{CapabilityLLMClient: llm.CapabilityLLMClient{
		CapabilityClient: capability.NewClient(capability.Configuration{Endpoint: endpoint, UnixSocketPath: socketPath, Timeout: time.Minute}),
		ModelName:        strings.TrimSpace(os.Getenv("BLUECLAW_DECISION_MODEL")),
	}}
	planner := intake.NewDecisionPlanner(decisionModel, nil, nil)
	outcomes := []intakeDecisionOutcome{}
	for _, decisionCase := range intakeDecisionCorpus() {
		outcomes = append(outcomes, runIntakeDecisionCase(t, planner, decisionCase))
	}
	writeIntakeDecisionEvidence(t, outcomes)
	for _, outcome := range outcomes {
		if len(outcome.Disagreements) > 0 {
			t.Errorf("%s: %s", outcome.Name, strings.Join(outcome.Disagreements, "; "))
		}
	}
}

type intakeDecisionOutcome struct {
	Name          string                              `json:"name"`
	Decision      agentcontract.IntakeMessageDecision `json:"decision"`
	CallRecords   []agentcontract.LLMCallRecord       `json:"callRecords"`
	Disagreements []string                            `json:"disagreements"`
	Error         string                              `json:"error,omitempty"`
}

func runIntakeDecisionCase(t *testing.T, planner intake.DecisionPlanner, decisionCase intakeDecisionCase) intakeDecisionOutcome {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	callLedger := &agentcontract.IntakeCallLedger{}
	decisions, errorValue := planner.Decide(ctx, decisionCase.request, callLedger)
	if errorValue != nil {
		return intakeDecisionOutcome{Name: decisionCase.name, CallRecords: callLedger.Records, Error: errorValue.Error(), Disagreements: []string{"the decision call failed: " + errorValue.Error()}}
	}
	decision, isDecided := decisions.ForMessage(decisionCase.request.Messages[0].MessageID)
	if !isDecided {
		return intakeDecisionOutcome{Name: decisionCase.name, CallRecords: callLedger.Records, Disagreements: []string{"the decision answered about no message"}}
	}
	return intakeDecisionOutcome{
		Name:          decisionCase.name,
		Decision:      decision,
		CallRecords:   callLedger.Records,
		Disagreements: intakeDecisionDisagreements(decisionCase, decision),
	}
}

func intakeDecisionDisagreements(decisionCase intakeDecisionCase, decision agentcontract.IntakeMessageDecision) []string {
	disagreements := []string{}
	if decisionCase.expectedTarget != "" && decision.Addressing.Target != decisionCase.expectedTarget {
		disagreements = append(disagreements, "expected target "+string(decisionCase.expectedTarget)+", got "+string(decision.Addressing.Target))
	}
	if decision.Addressing.ShouldRespond != decisionCase.expectedRespond {
		disagreements = append(disagreements, "expected shouldRespond to be the other way round")
	}
	if decisionCase.expectedRoute != "" && decision.TurnFields.Route != decisionCase.expectedRoute {
		disagreements = append(disagreements, "expected route "+string(decisionCase.expectedRoute)+", got "+string(decision.TurnFields.Route))
	}
	if decisionCase.expectedApproval != "" && (decision.TurnFields.Approval == nil || *decision.TurnFields.Approval != decisionCase.expectedApproval) {
		disagreements = append(disagreements, "expected approval "+string(decisionCase.expectedApproval))
	}
	if len(decisionCase.expectedChoiceKeys) > 0 && strings.Join(decision.TurnFields.Choices, ",") != strings.Join(decisionCase.expectedChoiceKeys, ",") {
		disagreements = append(disagreements, "expected choices "+strings.Join(decisionCase.expectedChoiceKeys, ",")+", got "+strings.Join(decision.TurnFields.Choices, ","))
	}
	if decisionCase.expectedRelatesTask && !decision.RelatesToActiveTask {
		disagreements = append(disagreements, "expected the message to be read as part of the running task")
	}
	return disagreements
}

func writeIntakeDecisionEvidence(t *testing.T, outcomes []intakeDecisionOutcome) {
	t.Helper()
	artifactRoot := firstNonEmptyTestString(os.Getenv("BLUECLAW_E2E_ARTIFACT_DIR"), filepath.Join("..", "..", ".artifacts", "intake-decision-live"))
	if errorValue := os.MkdirAll(artifactRoot, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	document, errorValue := json.MarshalIndent(outcomes, "", "  ")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	evidencePath := filepath.Join(artifactRoot, "intake-decisions.json")
	if errorValue := os.WriteFile(evidencePath, document, 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Logf("intake decision evidence: %s", evidencePath)
}

func intakeDecisionCorpus() []intakeDecisionCase {
	return []intakeDecisionCase{
		{
			name:            "a direct request starts work",
			request:         intakeDecisionRequestFor("message-1", "내일 오후 3시에 팀 회의 잡아줘", "D", false),
			expectedRoute:   agentcontract.TurnRouteStartTask,
			expectedTarget:  agentcontract.AddressingTargetBot,
			expectedRespond: true,
		},
		{
			name:            "a question is answered in words",
			request:         intakeDecisionRequestFor("message-2", "연차는 며칠까지 쓸 수 있어?", "D", false),
			expectedRoute:   agentcontract.TurnRouteAnswerQuestion,
			expectedTarget:  agentcontract.AddressingTargetBot,
			expectedRespond: true,
		},
		{
			name:            "a question about the agent itself is a meta answer",
			request:         intakeDecisionRequestFor("message-3", "너는 무슨 일을 할 수 있어?", "D", false),
			expectedRoute:   agentcontract.TurnRouteAnswerMeta,
			expectedTarget:  agentcontract.AddressingTargetBot,
			expectedRespond: true,
		},
		{
			name:            "two colleagues talking in a channel are not addressing the agent",
			request:         intakeDecisionRequestFor("message-4", "박예시 님, 어제 보내주신 자료 잘 받았습니다", "O", false),
			expectedTarget:  agentcontract.AddressingTargetHuman,
			expectedRespond: false,
		},
		{
			name:            "a mention in a channel is addressed to the agent",
			request:         intakeDecisionRequestFor("message-5", "@internkim 이번 주 회의록 정리해줘", "O", true),
			expectedRoute:   agentcontract.TurnRouteStartTask,
			expectedTarget:  agentcontract.AddressingTargetBot,
			expectedRespond: true,
		},
		{
			name:            "an impossible request is refused rather than started",
			request:         intakeDecisionRequestFor("message-6", "어제로 돌아가서 그 메일을 보내지 않은 걸로 해줘", "D", false),
			expectedRoute:   agentcontract.TurnRouteGiveUp,
			expectedTarget:  agentcontract.AddressingTargetBot,
			expectedRespond: true,
		},
		{
			name:             "a bare yes approves the pending action",
			request:          intakeDecisionRequestWithPendingConfirmation("message-7", "응 해줘"),
			expectedTarget:   agentcontract.AddressingTargetBot,
			expectedRespond:  true,
			expectedApproval: agentcontract.ApprovalSignalApprove,
		},
		{
			name:             "a refusal in ordinary words rejects the pending action",
			request:          intakeDecisionRequestWithPendingConfirmation("message-8", "아니 이번에는 하지 마"),
			expectedTarget:   agentcontract.AddressingTargetBot,
			expectedRespond:  true,
			expectedApproval: agentcontract.ApprovalSignalReject,
		},
		{
			name:               "a natural-language reply selects the pending option",
			request:            intakeDecisionRequestWithPendingChoice("message-9", "발표자료로 만들어 주세요"),
			expectedTarget:     agentcontract.AddressingTargetBot,
			expectedRespond:    true,
			expectedChoiceKeys: []string{"B"},
		},
		{
			name:                "an addition while work runs belongs to the running task",
			request:             intakeDecisionRequestWithActiveTask("message-10", "아 그리고 작년 수치도 같이 넣어줘"),
			expectedTarget:      agentcontract.AddressingTargetBot,
			expectedRespond:     true,
			expectedRelatesTask: true,
		},
	}
}

func intakeDecisionRequestFor(messageID string, prompt string, conversationType string, isBotMentioned bool) agentcontract.IntakeDecisionRequest {
	return agentcontract.IntakeDecisionRequest{
		Messages: []agentcontract.IntakeDecisionMessage{{
			MessageID:    messageID,
			Prompt:       prompt,
			SenderName:   "이샘플",
			SenderHandle: "sample",
			BotMentioned: isBotMentioned,
			SentAt:       time.Now(),
		}},
		ConversationType: conversationType,
		AgentIdentity:    agentcontract.AgentIdentity{Name: "김인턴", Handle: "internkim"},
		Company:          agentcontract.CompanyContext{Name: "여명거리"},
		ToolSet:          toolcontract.NewToolSet([]string{"event_add", "event_list", "task_add", "memory_search", "web_search"}),
		ResponseLanguage: "ko",
		AllowGiveUp:      true,
		EnvironmentNow:   time.Now(),
	}
}

func intakeDecisionRequestWithPendingConfirmation(messageID string, prompt string) agentcontract.IntakeDecisionRequest {
	request := intakeDecisionRequestFor(messageID, prompt, "D", false)
	request.PendingConfirmation = agentcontract.PendingConfirmationContext{
		TaskRunID: "task-pending",
		Prompt:    "내일 휴가 일정을 캘린더에서 삭제해줘",
		Question:  "내일 휴가 일정을 삭제할까요?",
		AskedAt:   time.Now().Add(-time.Minute),
	}
	return request
}

func intakeDecisionRequestWithPendingChoice(messageID string, prompt string) agentcontract.IntakeDecisionRequest {
	request := intakeDecisionRequestFor(messageID, prompt, "D", false)
	request.PendingChoice = agentcontract.PendingChoiceContext{
		TaskRunID: "task-asking",
		Question:  "어떤 형식으로 만들까요?",
		Options: []agentcontract.ChoiceReplyOption{
			{Key: "A", Label: "문서"},
			{Key: "B", Label: "발표자료"},
		},
		AskedAt: time.Now().Add(-time.Minute),
	}
	return request
}

func intakeDecisionRequestWithActiveTask(messageID string, prompt string) agentcontract.IntakeDecisionRequest {
	request := intakeDecisionRequestFor(messageID, prompt, "D", false)
	request.ActiveTask = agentcontract.ActiveTaskContext{
		TaskRunID: "task-running",
		Prompt:    "작년과 올해 매출 비교표를 만들어줘",
		Status:    "running",
	}
	return request
}
