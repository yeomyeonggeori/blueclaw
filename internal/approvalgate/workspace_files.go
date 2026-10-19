package approvalgate

import (
	"context"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type WorkspaceFileCarrier func(context.Context) (map[string]any, *toolcontract.ToolResult)

type workspaceFileCarrierKey struct{}

func WithWorkspaceFileCarrier(ctx context.Context, carrier WorkspaceFileCarrier) context.Context {
	return context.WithValue(ctx, workspaceFileCarrierKey{}, carrier)
}

func CarriedWorkspaceFiles(ctx context.Context) (map[string]any, *toolcontract.ToolResult) {
	carrier, isCarried := ctx.Value(workspaceFileCarrierKey{}).(WorkspaceFileCarrier)
	if !isCarried || carrier == nil {
		return nil, nil
	}
	return carrier(ctx)
}
