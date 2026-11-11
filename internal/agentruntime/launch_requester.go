package agentruntime

import (
	"context"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

func contextAskedByTheRequester(ctx context.Context, request TaskLaunchRequest) context.Context {
	return model.ContextWithRequestContext(ctx, model.RequestContext{
		RequesterPersonID:       request.RequesterPersonID,
		RequesterEmail:          request.RequesterEmail,
		RequesterName:           request.RequesterName,
		RequesterPlatformUserID: request.RequesterPlatformUserID,
		ConversationID:          request.ConversationID,
		Platform:                request.Platform,
	})
}
