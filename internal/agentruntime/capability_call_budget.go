package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const capabilityAnswerMargin = 5 * time.Second

func (toolCatalogBuilder *ToolCatalogBuilder) capabilityCallBudget(descriptor CapabilityToolDescriptor) time.Duration {
	if descriptor.TimeoutSecond > 0 {
		return time.Duration(descriptor.TimeoutSecond) * time.Second
	}
	return toolCatalogBuilder.capabilityClient.CallTimeout
}

func (toolCatalogBuilder *ToolCatalogBuilder) postCapabilityInvocation(ctx context.Context, operation string, budget time.Duration, requestDocument map[string]any, responseDocument any) error {
	if budget > 0 {
		requestDocument["timeoutSecond"] = int(budget / time.Second)
	}
	return toolCatalogBuilder.capabilityClient.PostJSONWithin(ctx, capabilityCallLimit(budget), "/v1/tools/"+url.PathEscape(operation)+"/invoke", requestDocument, responseDocument)
}

func capabilityCallLimit(budget time.Duration) time.Duration {
	if budget <= 0 {
		return 0
	}
	return budget + capabilityAnswerMargin
}

func capabilityCallOutlivedItsLimit(toolContext context.Context, errorValue error) bool {
	return errors.Is(errorValue, context.DeadlineExceeded) && toolContext.Err() == nil
}

func capabilityCallExpiredFailure(operation string, limit time.Duration) toolcontract.ToolResult {
	summary := fmt.Sprintf("%s did not finish within %d seconds, and the work it started may still have taken effect", operation, int(limit/time.Second))
	result := toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.Timeout, "capability_invoke", summary)
	result.Failure.Retryable = false
	return result
}
