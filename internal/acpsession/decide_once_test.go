package acpsession

import (
	"context"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/agenttest"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
	"github.com/yeomyeonggeori/bluecollar/intake"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type answerPerCallDecisionModel struct {
	mutex    sync.Mutex
	outcomes []intaketest.Outcome
	requests []model.DecisionRequest
}

func (decisionModel *answerPerCallDecisionModel) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	decisionModel.mutex.Lock()
	defer decisionModel.mutex.Unlock()
	decisionModel.requests = append(decisionModel.requests, request)
	outcome := decisionModel.outcomes[max(min(countTurnDecisions(decisionModel.requests), len(decisionModel.outcomes))-1, 0)]
	return model.DecisionResponse{Answers: intaketest.Answers(request.Questions, func(string) intaketest.Outcome { return outcome })}, nil
}

func (decisionModel *answerPerCallDecisionModel) turnDecisionCount() int {
	decisionModel.mutex.Lock()
	defer decisionModel.mutex.Unlock()
	return countTurnDecisions(decisionModel.requests)
}

func countTurnDecisions(requests []model.DecisionRequest) int {
	count := 0
	for _, request := range requests {
		if _, asksRoute := request.Questions["m1."+agentcontract.IntakeQuestionRoute]; asksRoute {
			count++
		}
	}
	return count
}

func (decisionModel *answerPerCallDecisionModel) gatewayCallCount() int {
	decisionModel.mutex.Lock()
	defer decisionModel.mutex.Unlock()
	count := 0
	for _, request := range decisionModel.requests {
		if inboundengagement.AsksOnlyGatewayQuestions(request.Questions) {
			count++
		}
	}
	return count
}

func addressedWorkAtLevel(taskLevel agentcontract.TaskLevel) intaketest.Outcome {
	return intaketest.Outcome{
		Addressing: inboundengagement.AddressingDecision{Target: inboundengagement.AddressingTargetBot, ShouldRespond: true},
		TurnDecision: agentcontract.TurnDecision{
			Route:            agentcontract.TurnRouteStartTask,
			Classification:   agentcontract.IntakeClassificationBoundedTask,
			TaskShape:        agentcontract.TaskShapeResearchTask,
			TaskLevel:        taskLevel,
			ResponseLanguage: "ko",
		},
	}
}

type decidingPlane struct {
	decisionModel *answerPerCallDecisionModel
	harness       *harnesstest.Harness
	connection    *acp.ClientSideConnection
	sessionID     acp.SessionId
}

func aDecidingPlane(t *testing.T) decidingPlane {
	t.Helper()
	decisionModel := &answerPerCallDecisionModel{outcomes: []intaketest.Outcome{
		addressedWorkAtLevel(agentcontract.TaskLevelMedium),
		addressedWorkAtLevel(agentcontract.TaskLevelHigh),
	}}
	planner := intake.NewDecisionPlanner(decisionModel, nil, nil)
	wordsModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		DefaultResponsesBySchema: map[string]string{agentcontract.TurnRouterSchemaName: `{"expectedResults":[]}`},
	})
	turnRouter := intake.NewTurnRouter(wordsModel, planner, agentcontract.IntakeOptions{IsEnabled: true, DefaultTaskLevel: agentcontract.TaskLevelLow})
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	harness := harnesstest.New(taskRunService)
	taskLauncher := agentruntime.NewTaskLauncher(harness, taskRunService, nil)
	taskLauncher.UseTurnRouter(turnRouter)
	client := &recordingClient{}
	connection, _ := connectedPairWithCollaborators(t, client, Collaborators{
		TaskLauncher: taskLauncher,
		Directory:    staticDirectory{},
		ReplyReader:  scriptedReader{},
		SessionTurns: connectorRuntimeDeciding(taskRunService, inboundengagement.NewDecisionModelDecider(decisionModel, nil)),
		TaskRunStore: taskRunService,
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-room"))
	return decidingPlane{decisionModel: decisionModel, harness: harness, connection: connection, sessionID: sessionID}
}

func (plane decidingPlane) promptInTheRoom(t *testing.T, words string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, errorValue := plane.connection.Prompt(ctx, acp.PromptRequest{
		SessionId: plane.sessionID,
		Prompt:    []acp.ContentBlock{acp.TextBlock(words)},
		Meta: map[string]any{MessageMetaKey: map[string]any{
			"messageID": "message-room-1",
			"context": map[string]any{
				"conversationType": "channel",
				"addressing":       map[string]any{"botMentioned": true},
			},
		}},
	})
	if errorValue != nil {
		t.Fatalf("prompt: %v", errorValue)
	}
}

func TestAMessageInARoomIsJudgedByTheGatewayAndPlannedOnItsOwn(t *testing.T) {
	plane := aDecidingPlane(t)

	plane.promptInTheRoom(t, "인턴킴, 이번 주 정산 정리해줘")

	if count := plane.decisionModel.gatewayCallCount(); count != 1 {
		t.Fatalf("one message in a room was judged by the gateway %d times, expected once", count)
	}
	if count := plane.decisionModel.turnDecisionCount(); count != 1 {
		t.Fatalf("one message in a room was planned %d times, expected once", count)
	}
	if plane.harness.RunTurnCallCount() != 1 {
		t.Fatalf("the turn ran %d times, expected once", plane.harness.RunTurnCallCount())
	}
	planned := plane.harness.LastTurnRequest().PrecomputedTurnDecision
	if planned == nil || planned.TaskLevel != agentcontract.TaskLevelMedium {
		t.Fatalf("the turn ran on %+v, expected the %q level of the plan the launch asked for", planned, agentcontract.TaskLevelMedium)
	}
}
