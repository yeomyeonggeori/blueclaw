package agentruntime

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/skill"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const (
	deliveryCheckTimeoutSeconds = 300
	deliveryCheckFailureLimit   = 400
)

func (toolCatalogBuilder *ToolCatalogBuilder) runDeliveryChecks(ctx context.Context, handlerContext toolHandlerContext, concretePath string) []string {
	bundles, _ := skill.NewSkillRegistry().DiscoverSkill(toolCatalogBuilder.bundledSkillRootPath())
	failures := []string{}
	for _, bundle := range bundles {
		if bundle.DeliveryCheck == "" {
			continue
		}
		result, _ := toolCatalogBuilder.runTerminalTool(ctx, security.CommandRequest{
			Command:       deliveryCheckCommand(bundle, concretePath),
			TimeoutSecond: deliveryCheckTimeoutSeconds,
		}, handlerContext)
		if result.Failed() {
			failures = append(failures, fmt.Sprintf("%s: %s's delivery check failed, so the file went out unchecked: %s", filepath.Base(concretePath), bundle.Name, boundedText(result.ContentText())))
		}
	}
	return failures
}

type checkedDelivery struct {
	failures []string
	metadata deliveredFileMetadata
}

func (toolCatalogBuilder *ToolCatalogBuilder) checkDeliveries(ctx context.Context, handlerContext toolHandlerContext, attachmentInputs []fileAttachFileInput) []checkedDelivery {
	checked := make([]checkedDelivery, 0, len(attachmentInputs))
	for _, attachmentInput := range attachmentInputs {
		concretePath := toolCatalogBuilder.nativeRequesterPath(handlerContext.request, strings.TrimSpace(attachmentInput.Path))
		checked = append(checked, checkedDelivery{
			failures: toolCatalogBuilder.runDeliveryChecks(ctx, handlerContext, concretePath),
			metadata: toolCatalogBuilder.deliveredMetadata(ctx, handlerContext.request, concretePath),
		})
	}
	return checked
}

func deliveryRefusals(checked []checkedDelivery) []string {
	refusals := []string{}
	for _, delivery := range checked {
		if delivery.metadata.Refusal != "" {
			refusals = append(refusals, delivery.metadata.Refusal)
		}
	}
	return refusals
}

func fileDeliverRefusal(refusals []string) toolcontract.ToolResult {
	result := toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "file_deliver", strings.Join(refusals, "\n"))
	result.Failure.Retryable = true
	result.Failure.SafeRetry = true
	result.Failure.RetryPolicy = "different_input"
	return result
}

func deliveryCheckCommand(bundle skill.SkillBundle, concretePath string) string {
	words := strings.Fields(bundle.DeliveryCheck)
	quoted := []string{shellSingleQuoted(filepath.Join(bundle.DirectoryPath, words[0]))}
	for _, word := range append(words[1:], concretePath) {
		quoted = append(quoted, shellSingleQuoted(word))
	}
	return strings.Join(quoted, " ")
}

func boundedText(text string) string {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) <= deliveryCheckFailureLimit {
		return trimmed
	}
	return trimmed[:deliveryCheckFailureLimit]
}
