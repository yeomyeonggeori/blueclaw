package approvalgate

import "context"

type refusalTargetKey struct{}

func WithRefusalTarget(ctx context.Context, target ApprovalTarget) context.Context {
	if !target.IsResolved() {
		return ctx
	}
	return context.WithValue(ctx, refusalTargetKey{}, target)
}

func refusalTarget(ctx context.Context) (ApprovalTarget, bool) {
	target, isNamed := ctx.Value(refusalTargetKey{}).(ApprovalTarget)
	return target, isNamed
}
