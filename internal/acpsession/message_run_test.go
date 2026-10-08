package acpsession

import (
	"context"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

type ledgerLauncher struct {
	taskRunService *task.TaskRunService
	mutex          sync.Mutex
	launchCount    int
}

func (launcher *ledgerLauncher) Launch(_ context.Context, request agentruntime.TaskLaunchRequest) (agentruntime.TaskLaunchResult, error) {
	launcher.mutex.Lock()
	launcher.launchCount++
	launcher.mutex.Unlock()
	taskRun := launchedRunForTest(launcher.taskRunService, request.ConversationID, request.SourceReference)
	completed, errorValue := launcher.taskRunService.CompleteTaskRun(taskRun.TaskRunID, "done")
	if errorValue != nil {
		return agentruntime.TaskLaunchResult{}, errorValue
	}
	return agentruntime.TaskLaunchResult{TurnResult: agentcontract.AgentTurnResult{TaskRun: completed}}, nil
}

func (launcher *ledgerLauncher) launches() int {
	launcher.mutex.Lock()
	defer launcher.mutex.Unlock()
	return launcher.launchCount
}

func launchedRunForTest(taskRunService *task.TaskRunService, conversationID string, sourceReference string) agentcontract.TaskRun {
	taskRun := taskRunService.CreateTaskRun("person-sample", conversationID, "박예시한테 DM 보내줘")
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentTaskLaunched, `{"sourceReference":"`+sourceReference+`"}`)
	return taskRun
}

func promptMessageForTest(connection *acp.ClientSideConnection, sessionID acp.SessionId, messageID string) (acp.PromptResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return connection.Prompt(ctx, acp.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acp.ContentBlock{acp.TextBlock("박예시한테 DM 보내줘")},
		Meta:      map[string]any{MessageMetaKey: map[string]any{"messageID": messageID}},
	})
}

func connectedPairOverLedger(t *testing.T, launcher TaskLauncher, taskRunService *task.TaskRunService) *acp.ClientSideConnection {
	t.Helper()
	return reconnectedPair(t, launcher, &recordingClient{}, taskRunService)
}

func TestTheSameMessagePromptedTwiceRunsOnce(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	launcher := &ledgerLauncher{taskRunService: taskRunService}
	connection := connectedPairOverLedger(t, launcher, taskRunService)
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	for range 2 {
		if _, errorValue := promptMessageForTest(connection, sessionID, "message-7"); errorValue != nil {
			t.Fatalf("prompt: %v", errorValue)
		}
	}

	if launches := launcher.launches(); launches != 1 {
		t.Fatalf("one message launched %d task runs", launches)
	}
}

type gatedLauncher struct {
	ledgerLauncher
	entered chan struct{}
	gate    chan struct{}
}

func (launcher *gatedLauncher) Launch(ctx context.Context, request agentruntime.TaskLaunchRequest) (agentruntime.TaskLaunchResult, error) {
	launcher.entered <- struct{}{}
	<-launcher.gate
	return launcher.ledgerLauncher.Launch(ctx, request)
}

func TestTheSameMessageArrivingTwiceAtOnceRunsOnce(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	launcher := &gatedLauncher{
		ledgerLauncher: ledgerLauncher{taskRunService: taskRunService},
		entered:        make(chan struct{}, 2),
		gate:           make(chan struct{}),
	}
	connection := connectedPairOverLedger(t, launcher, taskRunService)
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, errorValue := promptMessageForTest(connection, sessionID, "message-7"); errorValue != nil {
				t.Errorf("prompt: %v", errorValue)
			}
		}()
	}
	<-launcher.entered
	close(launcher.gate)
	group.Wait()

	if launches := launcher.launches(); launches != 1 {
		t.Fatalf("one message arriving twice at once launched %d task runs", launches)
	}
}

func TestAMessageFlightHoldsASecondClaimUntilTheFirstIsReleased(t *testing.T) {
	flights := newMessageFlights()
	key := messageFlightKey{personID: "person-sample", sourceReference: "buzz:conversation-1:message-7"}
	release, errorValue := flights.claim(context.Background(), key)
	if errorValue != nil {
		t.Fatalf("first claim: %v", errorValue)
	}
	claimed := make(chan struct{})
	go func() {
		releaseSecond, _ := flights.claim(context.Background(), key)
		releaseSecond()
		close(claimed)
	}()
	select {
	case <-claimed:
		t.Fatal("a second claim succeeded while the first was held")
	default:
	}

	release()

	<-claimed
}

func TestAMessageFlightLetsDifferentMessagesFlyTogether(t *testing.T) {
	flights := newMessageFlights()
	releaseFirst, _ := flights.claim(context.Background(), messageFlightKey{personID: "person-sample", sourceReference: "buzz:conversation-1:message-7"})
	defer releaseFirst()

	releaseSecond, errorValue := flights.claim(context.Background(), messageFlightKey{personID: "person-sample", sourceReference: "buzz:conversation-1:message-8"})

	if errorValue != nil {
		t.Fatalf("second claim: %v", errorValue)
	}
	releaseSecond()
}

func TestAReaskedMessageIsReleasedByTheTransitionOfItsRun(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	launcher := &ledgerLauncher{taskRunService: taskRunService}
	connection := connectedPairOverLedger(t, launcher, taskRunService)
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))
	running := launchedRunForTest(taskRunService, "conversation-1", "buzz:conversation-1:message-7")
	if _, errorValue := taskRunService.AdvanceTaskRun(running.TaskRunID, "assistant"); errorValue != nil {
		t.Fatalf("advance: %v", errorValue)
	}
	answered := make(chan acp.PromptResponse, 1)
	go func() {
		response, _ := promptMessageForTest(connection, sessionID, "message-7")
		answered <- response
	}()

	if _, errorValue := taskRunService.CancelTaskRun(running.TaskRunID, "person-sample"); errorValue != nil {
		t.Fatalf("cancel: %v", errorValue)
	}

	response := <-answered
	if response.StopReason != acp.StopReasonCancelled {
		t.Fatalf("the re-ask was answered %q", response.StopReason)
	}
	if launches := launcher.launches(); launches != 0 {
		t.Fatalf("the re-ask launched %d task runs", launches)
	}
}

func TestADifferentMessageInTheSameConversationRunsAgain(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	launcher := &ledgerLauncher{taskRunService: taskRunService}
	connection := connectedPairOverLedger(t, launcher, taskRunService)
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	for _, messageID := range []string{"message-7", "message-8"} {
		if _, errorValue := promptMessageForTest(connection, sessionID, messageID); errorValue != nil {
			t.Fatalf("prompt: %v", errorValue)
		}
	}

	if launches := launcher.launches(); launches != 2 {
		t.Fatalf("two messages launched %d task runs", launches)
	}
}

func TestAReaskedMessageWaitsForTheRunTheRestartResumed(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	launcher := &ledgerLauncher{taskRunService: taskRunService}
	connection := connectedPairOverLedger(t, launcher, taskRunService)
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))
	interrupted := launchedRunForTest(taskRunService, "conversation-1", "buzz:conversation-1:message-7")
	if _, isInterrupted := taskRunService.InterruptInactiveTaskRun(interrupted.TaskRunID, agentcontract.TaskInterruptReasonRuntimeRestart); !isInterrupted {
		t.Fatal("the run was not interrupted")
	}

	answered := make(chan acp.PromptResponse, 1)
	go func() {
		response, _ := promptMessageForTest(connection, sessionID, "message-7")
		answered <- response
	}()
	select {
	case <-answered:
		t.Fatal("the re-ask was answered while the run waited to be resumed")
	case <-time.After(300 * time.Millisecond):
	}
	if _, errorValue := taskRunService.ResumeTaskRun(interrupted.TaskRunID); errorValue != nil {
		t.Fatalf("resume: %v", errorValue)
	}
	if _, errorValue := taskRunService.CompleteTaskRun(interrupted.TaskRunID, "done"); errorValue != nil {
		t.Fatalf("complete: %v", errorValue)
	}

	select {
	case response := <-answered:
		if response.StopReason != acp.StopReasonEndTurn {
			t.Fatalf("the re-ask was answered %q", response.StopReason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the re-ask never got the outcome of the resumed run")
	}
	if launches := launcher.launches(); launches != 0 {
		t.Fatalf("the re-ask launched %d more task runs", launches)
	}
}
