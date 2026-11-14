package mcpserver

import "context"

type valuesFromTurnContext struct {
	context.Context
	turnContext context.Context
}

func (carrying valuesFromTurnContext) Value(key any) any {
	if value := carrying.Context.Value(key); value != nil {
		return value
	}
	return carrying.turnContext.Value(key)
}

func callContextWithinTheTurn(turnContext context.Context, callContext context.Context) (context.Context, context.CancelFunc) {
	if turnContext == nil {
		return context.WithCancel(callContext)
	}
	callWithinTheTurn, cancelCall := context.WithCancel(valuesFromTurnContext{Context: callContext, turnContext: turnContext})
	stopFollowingTheTurn := context.AfterFunc(turnContext, cancelCall)
	return callWithinTheTurn, func() {
		stopFollowingTheTurn()
		cancelCall()
	}
}
