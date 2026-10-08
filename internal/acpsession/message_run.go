package acpsession

import (
	"context"
	"sync"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

type TaskRunTransitionSource interface {
	RegisterTaskRunTransitionObserver(observer func(agentcontract.TaskRun)) func()
}

func taskRunTransitionSourceOf(taskRunStore taskstate.TaskRunStore) TaskRunTransitionSource {
	transitionSource, _ := taskRunStore.(TaskRunTransitionSource)
	return transitionSource
}

type messageFlightKey struct {
	personID        string
	sourceReference string
}

type messageFlights struct {
	mutex    sync.Mutex
	inFlight map[messageFlightKey]chan struct{}
}

func newMessageFlights() *messageFlights {
	return &messageFlights{inFlight: map[messageFlightKey]chan struct{}{}}
}

func (flights *messageFlights) claim(ctx context.Context, key messageFlightKey) (func(), error) {
	for {
		flights.mutex.Lock()
		finished, isFlying := flights.inFlight[key]
		if !isFlying {
			finished = make(chan struct{})
			flights.inFlight[key] = finished
			flights.mutex.Unlock()
			return func() { flights.release(key, finished) }, nil
		}
		flights.mutex.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-finished:
		}
	}
}

func (flights *messageFlights) release(key messageFlightKey, finished chan struct{}) {
	flights.mutex.Lock()
	delete(flights.inFlight, key)
	flights.mutex.Unlock()
	close(finished)
}

func (agent *Agent) claimMessage(ctx context.Context, launchRequest agentruntime.TaskLaunchRequest, messageContext MessageContext) (func(), error) {
	if messageContext.MessageID == "" {
		return func() {}, nil
	}
	return agent.messageFlights.claim(ctx, messageFlightKey{personID: launchRequest.RequesterPersonID, sourceReference: launchRequest.SourceReference})
}

func sourceReferenceFor(sessionID acp.SessionId, addressing Addressing, messageContext MessageContext) string {
	if messageContext.MessageID == "" {
		return "acp:" + string(sessionID)
	}
	return connectors.PlatformInboundEvent{
		Platform:       addressing.Platform,
		ConversationID: addressing.ConversationID,
		MessageID:      messageContext.MessageID,
	}.DedupeKey()
}

func (agent *Agent) runAlreadyLaunchedForMessage(personID string, sourceReference string, messageContext MessageContext) (agentcontract.TaskRun, bool) {
	if agent.taskRunStore == nil || messageContext.MessageID == "" {
		return agentcontract.TaskRun{}, false
	}
	return connectors.FindTaskRunBySourceReference(agent.taskRunStore, personID, sourceReference)
}

func (agent *Agent) answerFromRunOfTheSameMessage(ctx context.Context, sessionID acp.SessionId, taskRun agentcontract.TaskRun) (acp.PromptResponse, error) {
	agent.logger.Info("acpsession.prompt.message_already_has_a_run", "sessionID", string(sessionID), "taskRunID", taskRun.TaskRunID)
	settled, errorValue := agent.awaitSettledRun(ctx, taskRun)
	if errorValue != nil {
		return acp.PromptResponse{}, errorValue
	}
	return acp.PromptResponse{StopReason: stopReasonForTaskStatus(settled.Status)}, nil
}

func (agent *Agent) awaitSettledRun(ctx context.Context, taskRun agentcontract.TaskRun) (agentcontract.TaskRun, error) {
	if agent.taskRunTransitions == nil {
		return taskRun, nil
	}
	transitioned := make(chan struct{}, 1)
	stopObserving := agent.taskRunTransitions.RegisterTaskRunTransitionObserver(func(transitionedRun agentcontract.TaskRun) {
		if transitionedRun.TaskRunID == taskRun.TaskRunID {
			signalTransition(transitioned)
		}
	})
	defer stopObserving()
	for {
		current, isFound := agent.taskRunStore.FindTaskRun(taskRun.TaskRunID)
		if !isFound {
			return taskRun, nil
		}
		if !isStillWorking(current) {
			return current, nil
		}
		select {
		case <-ctx.Done():
			return agentcontract.TaskRun{}, ctx.Err()
		case <-transitioned:
		}
	}
}

func signalTransition(transitioned chan<- struct{}) {
	select {
	case transitioned <- struct{}{}:
	default:
	}
}

func isStillWorking(taskRun agentcontract.TaskRun) bool {
	switch taskRun.Status {
	case agentcontract.TaskStatusPlanned, agentcontract.TaskStatusRunning:
		return true
	}
	return agentcontract.TaskRunWasInterruptedByRuntimeRestart(taskRun)
}
