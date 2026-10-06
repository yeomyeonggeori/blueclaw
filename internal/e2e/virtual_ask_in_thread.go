package e2e

import (
	"context"
	"errors"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
)

const askingTurnPollInterval = 5 * time.Millisecond
const askingTurnLimit = 30 * time.Second

type handledInboundEvent struct {
	result     connectors.ConnectorRuntimeResult
	errorValue error
}

type askingTurn struct {
	finished chan handledInboundEvent
	stop     context.CancelFunc
}

func (harness *VirtualSessionHarness) handleInboundEvent(ctx context.Context, event connectors.PlatformInboundEvent) (connectors.ConnectorRuntimeResult, error) {
	if harness.askingTurn == nil {
		return harness.startTurnThatMayAsk(ctx, event)
	}
	return harness.answerAskingTurn(ctx, event)
}

func (harness *VirtualSessionHarness) startTurnThatMayAsk(ctx context.Context, event connectors.PlatformInboundEvent) (connectors.ConnectorRuntimeResult, error) {
	turnContext, stop := context.WithCancel(ctx)
	turn := &askingTurn{finished: make(chan handledInboundEvent, 1), stop: stop}
	go func() {
		result, errorValue := harness.runtime.HandleInboundEvent(turnContext, harness.adapter, event)
		turn.finished <- handledInboundEvent{result: result, errorValue: errorValue}
	}()
	return harness.untilFinishedOrAsking(ctx, turn)
}

func (harness *VirtualSessionHarness) untilFinishedOrAsking(ctx context.Context, turn *askingTurn) (connectors.ConnectorRuntimeResult, error) {
	ticker := time.NewTicker(askingTurnPollInterval)
	defer ticker.Stop()
	for {
		select {
		case handled := <-turn.finished:
			turn.stop()
			return handled.result, handled.errorValue
		case <-ctx.Done():
			turn.stop()
			return connectors.ConnectorRuntimeResult{}, ctx.Err()
		case <-ticker.C:
			if taskRunID, dispatchID, isAsking := harness.runtime.AwaitedQuestion(); isAsking {
				harness.askingTurn = turn
				return connectors.ConnectorRuntimeResult{Handled: true, Platform: harness.adapter.Name(), TaskRunID: taskRunID, ReplyDispatchID: dispatchID}, nil
			}
		}
	}
}

func (harness *VirtualSessionHarness) answerAskingTurn(ctx context.Context, event connectors.PlatformInboundEvent) (connectors.ConnectorRuntimeResult, error) {
	turn := harness.askingTurn
	answer, errorValue := harness.runtime.HandleInboundEvent(ctx, harness.adapter, event)
	if errorValue != nil || answer.Reason != connectors.ApprovalAnsweredInThreadReason {
		return answer, errorValue
	}
	harness.askingTurn = nil
	defer turn.stop()
	select {
	case handled := <-turn.finished:
		return handled.result, handled.errorValue
	case <-ctx.Done():
		return connectors.ConnectorRuntimeResult{}, ctx.Err()
	case <-time.After(askingTurnLimit):
		return connectors.ConnectorRuntimeResult{}, errors.New("the run that asked finished nothing after its question was answered")
	}
}

func (harness *VirtualSessionHarness) stopAskingTurn() {
	if harness.askingTurn == nil {
		return
	}
	harness.askingTurn.stop()
	<-harness.askingTurn.finished
	harness.askingTurn = nil
}
