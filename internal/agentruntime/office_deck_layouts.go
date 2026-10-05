package agentruntime

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"

	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

const officeDeckLayoutsReadLimit = 1 << 20

type officeDeckLayouts struct {
	Digest  string                        `json:"digest"`
	Choices map[string]officeDesignChoice `json:"choices"`
	Model   string                        `json:"model,omitempty"`
	CostUSD float64                       `json:"costUSD"`
	Failure string                        `json:"failure,omitempty"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) choosesDeckLayouts() bool {
	return toolCatalogBuilder.deckDesignModel != nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) chooseRequestedDeckLayouts(ctx context.Context, actor security.WorkspaceActor, request ToolCatalogRequest) {
	if !toolCatalogBuilder.choosesDeckLayouts() {
		return
	}
	taskDirectoryPath := security.TaskTemporaryDirectoryPath(toolCatalogBuilder.requesterHomePath(request), toolcontract.TaskRunIDFromContext(ctx))
	contextPath := officeRuntimeContextPath(taskDirectoryPath)
	if contextPath == "" {
		return
	}
	content, errorValue := actor.ReadFile(ctx, filepath.Join(taskDirectoryPath, officeContract.DeckLayouts.RequestFile), officeDeckLayoutsReadLimit)
	if errorValue != nil {
		return
	}
	digest := sha256Digest(content)
	if toolCatalogBuilder.areLayoutsChosen(ctx, actor, contextPath, digest) {
		return
	}
	layouts := toolCatalogBuilder.decideDeckLayouts(ctx, request, content)
	layouts.Digest = digest
	if errorValue := toolCatalogBuilder.writeOfficeRuntimeContext(ctx, actor, request, func(runtimeContext *officeRuntimeContext) { runtimeContext.DeckLayouts = &layouts }); errorValue != nil {
		slog.Warn("deck layouts were not recorded", "error", errorValue)
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) areLayoutsChosen(ctx context.Context, actor security.WorkspaceActor, contextPath string, digest string) bool {
	content, errorValue := actor.ReadFile(ctx, contextPath, officeRuntimeContextReadLimit)
	if errorValue != nil {
		return false
	}
	var recorded officeRuntimeContext
	return json.Unmarshal(content, &recorded) == nil && recorded.DeckLayouts != nil && recorded.DeckLayouts.Digest == digest
}

func (toolCatalogBuilder *ToolCatalogBuilder) decideDeckLayouts(ctx context.Context, request ToolCatalogRequest, content []byte) officeDeckLayouts {
	var definition officeDesignDefinition
	if json.Unmarshal(content, &definition) != nil || len(definition.Questions) == 0 {
		return officeDeckLayouts{Choices: map[string]officeDesignChoice{}, Failure: "the layout request holds no question"}
	}
	decisionContext, cancel := context.WithTimeout(ctx, officeDeckDecisionTimeout)
	defer cancel()
	response, errorValue := toolCatalogBuilder.deckDesignModel.Decide(decisionContext, model.DecisionRequest{
		State:     map[string]any{"request": requestWordings(request)},
		Questions: designQuestions(definition),
	})
	if errorValue != nil {
		slog.Warn("deck layouts were not chosen", "error", errorValue)
		return officeDeckLayouts{Choices: map[string]officeDesignChoice{}, Failure: errorValue.Error()}
	}
	layouts := officeDeckLayouts{Choices: map[string]officeDesignChoice{}, Model: response.ModelName, CostUSD: response.Usage.CostUSD}
	for name, answer := range response.Answers {
		layouts.Choices[name] = officeDesignChoice{Option: answer.Choice, Probabilities: answer.Probabilities}
	}
	return layouts
}
