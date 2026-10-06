package acpharness

import (
	"context"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type turnRequestContextKey struct{}

func WithTurnRequest(ctx context.Context, request agentcontract.AgentTurnRequest) context.Context {
	return context.WithValue(ctx, turnRequestContextKey{}, request)
}

func TurnRequestFrom(ctx context.Context) (agentcontract.AgentTurnRequest, bool) {
	request, isPresent := ctx.Value(turnRequestContextKey{}).(agentcontract.AgentTurnRequest)
	return request, isPresent
}
