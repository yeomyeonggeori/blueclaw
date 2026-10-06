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

func contextCarryingValuesOf(turnContext context.Context, callContext context.Context) context.Context {
	if turnContext == nil {
		return callContext
	}
	return valuesFromTurnContext{Context: callContext, turnContext: turnContext}
}
