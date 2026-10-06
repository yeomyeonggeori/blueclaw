package acpsession

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement/gatewaytest"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract/harnesstest"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type gatewayOnlyDecisionModel struct {
	mutex    sync.Mutex
	requests []model.DecisionRequest
}

func (decisionModel *gatewayOnlyDecisionModel) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	decisionModel.mutex.Lock()
	defer decisionModel.mutex.Unlock()
	decisionModel.requests = append(decisionModel.requests, request)
	if !inboundengagement.AsksOnlyGatewayQuestions(request.Questions) {
		return model.DecisionResponse{}, errors.New("blueclaw asked a question only the agent plans")
	}
	addressing := inboundengagement.AddressingDecision{Target: inboundengagement.AddressingTargetBot, ShouldRespond: true}
	return model.DecisionResponse{Answers: gatewaytest.Answers(request.Questions, gatewaytest.Outcome{Addressing: addressing})}, nil
}

func (decisionModel *gatewayOnlyDecisionModel) callCount() int {
	decisionModel.mutex.Lock()
	defer decisionModel.mutex.Unlock()
	return len(decisionModel.requests)
}

type decidingPlane struct {
	decisionModel *gatewayOnlyDecisionModel
	harness       *harnesstest.Harness
	connection    *acp.ClientSideConnection
	sessionID     acp.SessionId
}

func aDecidingPlane(t *testing.T) decidingPlane {
	t.Helper()
	decisionModel := &gatewayOnlyDecisionModel{}
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	harness := harnesstest.New(taskRunService)
	taskLauncher := agentruntime.NewTaskLauncher(harness, taskRunService, nil)
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

func TestAMessageInARoomIsJudgedByTheGatewayAndLeftToTheAgentToPlan(t *testing.T) {
	plane := aDecidingPlane(t)

	plane.promptInTheRoom(t, "인턴킴, 이번 주 정산 정리해줘")

	if count := plane.decisionModel.callCount(); count != 1 {
		t.Fatalf("one message in a room made %d decision calls here, expected the one gateway judgment", count)
	}
	if plane.harness.RunTurnCallCount() != 1 {
		t.Fatalf("the turn ran %d times, expected once", plane.harness.RunTurnCallCount())
	}
}
