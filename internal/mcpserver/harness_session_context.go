package mcpserver

import "context"

type harnessSessionContextKey struct{}

func WithHarnessSession(ctx context.Context, harnessSession HarnessSession) context.Context {
	if harnessSession == (HarnessSession{}) {
		return ctx
	}
	return context.WithValue(ctx, harnessSessionContextKey{}, harnessSession)
}

func HarnessSessionFromContext(ctx context.Context) HarnessSession {
	harnessSession, _ := ctx.Value(harnessSessionContextKey{}).(HarnessSession)
	return harnessSession
}
