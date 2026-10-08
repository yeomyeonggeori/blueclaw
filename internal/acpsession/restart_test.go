package acpsession

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract/harnesstest"
)

const interruptedMessageID = "message-7"

type harnessThatDiesMidTurn struct {
	*harnesstest.Harness
	taskRunService *task.TaskRunService
	died           chan struct{}
}

func (harness *harnessThatDiesMidTurn) RunTurn(_ context.Context, request agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	if _, errorValue := harness.taskRunService.AdvanceTaskRun(request.ExistingTaskRunID, request.ProfileName); errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	close(harness.died)
	select {}
}

type provisionerThatDies struct {
	died chan struct{}
}

func (provisioner provisionerThatDies) ProvisionRequesterWorkspace(context.Context, policy.PersonAccess, string) error {
	close(provisioner.died)
	select {}
}

type countingHarness struct {
	*harnesstest.Harness
	mutex     sync.Mutex
	turnCount int
}

func (harness *countingHarness) RunTurn(ctx context.Context, request agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	harness.mutex.Lock()
	defer harness.mutex.Unlock()
	harness.turnCount++
	return harness.Harness.RunTurn(ctx, request)
}

func (harness *countingHarness) turns() int {
	harness.mutex.Lock()
	defer harness.mutex.Unlock()
	return harness.turnCount
}

type messengerAdapter struct {
	mutex   sync.Mutex
	replies []string
}

func (adapter *messengerAdapter) Name() string { return "buzz" }

func (adapter *messengerAdapter) ParseHTTPEvent(context.Context, *http.Request) (connectors.HTTPParseResult, error) {
	return connectors.HTTPParseResult{}, nil
}

func (adapter *messengerAdapter) ResolveIdentity(context.Context, string) (identity.PlatformAccountIdentity, error) {
	return identity.PlatformAccountIdentity{}, nil
}

func (adapter *messengerAdapter) StartProgress(context.Context, connectors.ReplyTarget) error {
	return nil
}

func (adapter *messengerAdapter) StopProgress(context.Context, connectors.ReplyTarget) error {
	return nil
}

func (adapter *messengerAdapter) SendReply(_ context.Context, _ connectors.ReplyTarget, reply connectors.OutboundReply) (string, error) {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	adapter.replies = append(adapter.replies, reply.Message)
	return "posted", nil
}

func (adapter *messengerAdapter) FetchHistory(context.Context, string, int) (connectors.VisibleContext, error) {
	return connectors.VisibleContext{}, nil
}

func (adapter *messengerAdapter) sentReplies() []string {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	return append([]string{}, adapter.replies...)
}

type restartedHost struct {
	taskRunService   *task.TaskRunService
	connectorRuntime *connectors.ConnectorRuntime
	harness          *countingHarness
	messenger        *messengerAdapter
	relay            *recordingClient
	connection       *acp.ClientSideConnection
}

func TestAMessageInterruptedByARestartMidTurnIsAnsweredOnce(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	died := make(chan struct{})
	launcher := agentruntime.NewTaskLauncher(&harnessThatDiesMidTurn{Harness: harnesstest.New(taskRunService), taskRunService: taskRunService, died: died}, taskRunService, nil)

	expectTheRestartToAnswerOnce(t, taskRunService, startALaunchThatDies(t, taskRunService, launcher, died))
}

func TestAMessageInterruptedByARestartBeforeItsTurnBeganIsAnsweredOnce(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	died := make(chan struct{})
	launcher := agentruntime.NewTaskLauncher(harnesstest.New(taskRunService), taskRunService, nil)
	launcher.UseRequesterWorkspaceProvisioner(provisionerThatDies{died: died})

	expectTheRestartToAnswerOnce(t, taskRunService, startALaunchThatDies(t, taskRunService, launcher, died))
}

func expectTheRestartToAnswerOnce(t *testing.T, taskRunService *task.TaskRunService, interruptedTaskRunID string) {
	t.Helper()
	taskRunService.InterruptOrphanedRuntimeTaskRuns(agentcontract.TaskInterruptReasonRuntimeRestart)
	host := restartHost(t, taskRunService)
	reasked := host.reaskTheMessageTheRelayStillHolds(t)
	host.resumeInterruptedRun(t, interruptedTaskRunID)
	awaitReask(t, reasked)

	if turns := host.harness.turns(); turns != 1 {
		t.Fatalf("the message ran %d turns after the restart", turns)
	}
	if taskRuns := taskRunService.ListTaskRunByPersonID("person-sample"); len(taskRuns) != 1 {
		t.Fatalf("the message has %d task runs", len(taskRuns))
	}
	replies := append(host.messenger.sentReplies(), host.relay.postedMessagesForTest()...)
	if len(replies) != 1 {
		t.Fatalf("the member was answered %d times: %q", len(replies), replies)
	}
}

func startALaunchThatDies(t *testing.T, taskRunService *task.TaskRunService, launcher *agentruntime.TaskLauncher, died <-chan struct{}) string {
	t.Helper()
	connection := reconnectedPair(t, launcher, &recordingClient{}, taskRunService)
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))
	go promptWithReplyTarget(context.Background(), connection, sessionID)
	select {
	case <-died:
	case <-time.After(5 * time.Second):
		t.Fatal("the launch before the restart never reached the point it dies at")
	}
	taskRuns := taskRunService.ListTaskRunByPersonID("person-sample")
	if len(taskRuns) != 1 {
		t.Fatalf("the launch before the restart opened %d task runs", len(taskRuns))
	}
	return taskRuns[0].TaskRunID
}

func restartHost(t *testing.T, taskRunService *task.TaskRunService) *restartedHost {
	t.Helper()
	harness := &countingHarness{Harness: harnesstest.New(taskRunService)}
	harness.TurnResult.FinishMessage = "박예시에게 보냈어요."
	connectorRuntime := connectors.NewConnectorRuntime(identity.NewIdentityService(policy.PolicyProjection{}), harness, taskRunService, task.NewTaskEventService(), silentLogger())
	connectorRuntime.UseLaunchFailureCompleter(harness)
	messenger := &messengerAdapter{}
	connectorRuntime.RegisterAdapter(messenger)
	relay := &recordingClient{}
	connection, _ := connectedPairWithCollaborators(t, relay, Collaborators{
		TaskLauncher:  agentruntime.NewTaskLauncher(harness, taskRunService, nil),
		Directory:     staticDirectory{},
		ReplyReader:   scriptedReader{},
		TaskRunStore:  taskRunService,
		AnswerSettler: approvalgate.New(taskRunService),
		SessionTurns:  connectorRuntime,
	})
	return &restartedHost{
		taskRunService:   taskRunService,
		connectorRuntime: connectorRuntime,
		harness:          harness,
		messenger:        messenger,
		relay:            relay,
		connection:       connection,
	}
}

func (host *restartedHost) reaskTheMessageTheRelayStillHolds(t *testing.T) <-chan error {
	t.Helper()
	sessionID := openSessionForTest(t, host.connection, sessionMeta("sample@example.test", "conversation-1"))
	reasked := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, errorValue := promptWithReplyTarget(ctx, host.connection, sessionID)
		reasked <- errorValue
	}()
	select {
	case errorValue := <-reasked:
		reasked <- errorValue
	case <-time.After(300 * time.Millisecond):
	}
	return reasked
}

func (host *restartedHost) resumeInterruptedRun(t *testing.T, taskRunID string) {
	t.Helper()
	interrupted, _ := host.taskRunService.FindTaskRun(taskRunID)
	if !host.connectorRuntime.CanResumeInterruptedTaskRun(interrupted) {
		host.connectorRuntime.FailUnresumedInterruptedTaskRun(context.Background(), interrupted, "the task was interrupted by a runtime restart and could not be resumed")
		return
	}
	if !host.taskRunService.ClaimInterruptedTaskRunAutoResume(taskRunID, "runtime_restart") {
		t.Fatal("auto-resume could not claim the interrupted run")
	}
	if _, errorValue := host.connectorRuntime.ResumeInterruptedTaskRun(context.Background(), interrupted); errorValue != nil {
		t.Fatalf("resume: %v", errorValue)
	}
}

func awaitReask(t *testing.T, reasked <-chan error) {
	t.Helper()
	select {
	case errorValue := <-reasked:
		if errorValue != nil {
			t.Fatalf("the re-ask failed: %v", errorValue)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the re-ask never got the outcome of the resumed run")
	}
}

func (client *recordingClient) postedMessagesForTest() []string {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return append([]string{}, client.messages...)
}

func promptWithReplyTarget(ctx context.Context, connection *acp.ClientSideConnection, sessionID acp.SessionId) (acp.PromptResponse, error) {
	return connection.Prompt(ctx, acp.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acp.ContentBlock{acp.TextBlock("박예시한테 DM 보내줘")},
		Meta: map[string]any{MessageMetaKey: map[string]any{
			"messageID":     interruptedMessageID,
			"replyTargetID": "thread-1",
		}},
	})
}
