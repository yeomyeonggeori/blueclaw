package acpharness

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

type SteerRequest struct {
	Instruction string
	Reason      string
	MessageID   string
}

type steerExtension struct {
	method string
	params func(sessionID acp.SessionId, steer SteerRequest) any
}

func (harness *Harness) UseSteerExtension(method string, params func(sessionID acp.SessionId, steer SteerRequest) any) {
	harness.steerExtension = &steerExtension{method: method, params: params}
}

type turnControl struct {
	harness     *Harness
	connection  *acp.ClientSideConnection
	sessionID   acp.SessionId
	request     agentcontract.AgentTurnRequest
	detach      func()
	isMirroring *mirroringFlag

	mutex           sync.Mutex
	pendingSteers   []SteerRequest
	hasParkedTheRun bool
	hasCancelled    bool
	cancelContext   context.Context
}

type mirroringFlag struct {
	mutex sync.Mutex
	count int
}

func (flag *mirroringFlag) enter() {
	flag.mutex.Lock()
	flag.count++
	flag.mutex.Unlock()
}

func (flag *mirroringFlag) leave() {
	flag.mutex.Lock()
	flag.count--
	flag.mutex.Unlock()
}

func (flag *mirroringFlag) isActive() bool {
	flag.mutex.Lock()
	defer flag.mutex.Unlock()
	return flag.count > 0
}

func (harness *Harness) newTurnControl(ctx context.Context, connection *acp.ClientSideConnection, sessionID acp.SessionId, request agentcontract.AgentTurnRequest, isMirroring *mirroringFlag) *turnControl {
	control := &turnControl{harness: harness, connection: connection, sessionID: sessionID, request: request, isMirroring: isMirroring, detach: func() {}}
	if harness.taskRunStore != nil && strings.TrimSpace(request.ExistingTaskRunID) != "" {
		control.detach = harness.taskRunStore.RegisterTaskRunObserver(request.ExistingTaskRunID, func(rawTurnEvent taskstate.RawTurnEvent) { control.observe(ctx, rawTurnEvent) })
	}
	return control
}

func (control *turnControl) stop() {
	control.detach()
}

func (control *turnControl) observe(ctx context.Context, rawTurnEvent taskstate.RawTurnEvent) {
	switch rawTurnEvent.Name {
	case agentcontract.TaskEventAskRequested, agentcontract.TaskEventAgentInputRequested:
		control.noteParking(ctx)
	case agentcontract.TaskEventTaskSteerRequested:
		control.steer(ctx, rawTurnEvent.Body)
	}
}

func (control *turnControl) noteParking(ctx context.Context) {
	if control.isMirroring.isActive() || !control.isRunParked() {
		return
	}
	control.mutex.Lock()
	control.hasParkedTheRun = true
	control.cancelContext = context.WithoutCancel(ctx)
	control.mutex.Unlock()
}

func (control *turnControl) toolCallEnded() {
	stillParked := control.isRunParked()
	control.mutex.Lock()
	shouldCancel := control.hasParkedTheRun && stillParked && !control.hasCancelled
	control.hasCancelled = control.hasCancelled || shouldCancel
	cancelContext := control.cancelContext
	control.mutex.Unlock()
	if shouldCancel {
		_ = control.connection.Cancel(cancelContext, acp.CancelNotification{SessionId: control.sessionID})
	}
}

func (control *turnControl) isRunParked() bool {
	if control.harness.taskRunStore == nil || strings.TrimSpace(control.request.ExistingTaskRunID) == "" {
		return false
	}
	return isParked(control.harness.taskRunStore, control.request.ExistingTaskRunID)
}

func isParked(taskRunStore taskstate.TaskRunStore, taskRunID string) bool {
	taskRun, isFound := taskRunStore.FindTaskRun(taskRunID)
	return isFound && (taskRun.Status == agentcontract.TaskStatusWaitingApproval || taskRun.Status == agentcontract.TaskStatusWaitingUserInput)
}

func (control *turnControl) steer(ctx context.Context, eventBody string) {
	steer := steerRequestOf(eventBody)
	if control.harness.steerExtension != nil {
		_, _ = control.connection.CallExtension(context.WithoutCancel(ctx), control.harness.steerExtension.method, control.harness.steerExtension.params(control.sessionID, steer))
		return
	}
	control.mutex.Lock()
	defer control.mutex.Unlock()
	control.pendingSteers = append(control.pendingSteers, steer)
	_ = control.connection.Cancel(context.WithoutCancel(ctx), acp.CancelNotification{SessionId: control.sessionID})
}

func steerRequestOf(eventBody string) SteerRequest {
	document := struct {
		Instruction string `json:"instruction"`
		Reason      string `json:"reason"`
		MessageID   string `json:"messageID"`
	}{}
	_ = json.Unmarshal([]byte(eventBody), &document)
	return SteerRequest{Instruction: document.Instruction, Reason: document.Reason, MessageID: document.MessageID}
}

func (control *turnControl) nextSteer() (SteerRequest, bool) {
	control.mutex.Lock()
	defer control.mutex.Unlock()
	if control.hasParkedTheRun || len(control.pendingSteers) == 0 {
		return SteerRequest{}, false
	}
	steer := control.pendingSteers[0]
	control.pendingSteers = control.pendingSteers[1:]
	return steer, true
}

func (control *turnControl) converse(ctx context.Context, promptRequest acp.PromptRequest) (acp.PromptResponse, error) {
	for {
		promptResponse, errorValue := control.connection.Prompt(ctx, promptRequest)
		if errorValue != nil {
			return promptResponse, errorValue
		}
		steer, isSteered := control.nextSteer()
		if !isSteered {
			return promptResponse, nil
		}
		promptRequest = acp.PromptRequest{SessionId: control.sessionID, Prompt: []acp.ContentBlock{acp.TextBlock(steerPrompt(steer))}}
	}
}

func steerPrompt(steer SteerRequest) string {
	return "The person who asked sent this while you were working. Take it into account and carry on with their request:\n" + strings.TrimSpace(steer.Instruction)
}
