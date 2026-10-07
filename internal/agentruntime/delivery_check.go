package agentruntime

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/skill"
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
