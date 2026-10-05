package toolcallprogress

import (
	"context"

	acp "github.com/coder/acp-go-sdk"
)

type Observer func(acp.SessionUpdate)

type contextKey struct{}

func WithObserver(ctx context.Context, observer Observer) context.Context {
	return context.WithValue(ctx, contextKey{}, observer)
}

func ObserverFrom(ctx context.Context) Observer {
	observer, _ := ctx.Value(contextKey{}).(Observer)
	return observer
}
