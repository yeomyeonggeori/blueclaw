package agentruntime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/loop"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type routerLedgerLanguageModel struct {
	decision agentcontract.TurnDecision
	response model.StructuredResponse
}

func (languageModel *routerLedgerLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *routerLedgerLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name != agentcontract.TurnRouterSchemaName {
		return model.StructuredResponse{Content: "{}"}, nil
	}
	document, errorValue := json.Marshal(languageModel.decision)
	if errorValue != nil {
		return model.StructuredResponse{}, errorValue
	}
	response := languageModel.response
	response.Content = string(document)
	return response, nil
}

func persistedTurnRouterCallRecords(taskEvents []task.TaskEvent) []agentcontract.LLMCallRecord {
	records := []agentcontract.LLMCallRecord{}
	for _, taskEvent := range taskEvents {
		if taskEvent.Name != "llm.call" {
			continue
		}
		var record agentcontract.LLMCallRecord
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &record); errorValue == nil && record.SchemaName == agentcontract.TurnRouterSchemaName {
			records = append(records, record)
		}
	}
	return records
}

func TestTaskLauncherPersistsTurnRouterLLMCall(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := loop.NewAgentKernel(taskRunService, task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(staticRuntimeLanguageModel{content: runtimeFinishMessage("완료했습니다.")})

	routerLanguageModel := &routerLedgerLanguageModel{
		decision: agentcontract.TurnDecision{
			Route:            agentcontract.TurnRouteStartTask,
			Classification:   agentcontract.IntakeClassificationQuickReply,
			TaskShape:        agentcontract.TaskShapeImmediateReply,
			TaskLevel:        agentcontract.TaskLevelXLow,
			ResponseLanguage: "ko",
			Reason:           "direct answer",
		},
		response: model.StructuredResponse{
			ProviderName: "llmd",
			ModelName:    "router-model",
			ModelTier:    "xlow",
			Usage: model.Usage{
				PromptTokens:     11,
				CompletionTokens: 7,
				TotalTokens:      18,
			},
		},
	}

	launchResult, errorValue := routedTaskLauncher(agentKernel, taskRunService, NewToolCatalogBuilder(), routerLanguageModel).Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "오늘 무슨 요일이야?",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
	})
	if errorValue != nil {
		t.Fatalf("expected bounded run to complete: %v", errorValue)
	}

	records := persistedTurnRouterCallRecords(taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID))
	if len(records) != 1 {
		t.Fatalf("expected one persisted router call, got %+v", records)
	}
	if records[0].Provider != "llmd" || records[0].Model != "router-model" || records[0].ModelTier != "xlow" || records[0].UsedFallback {
		t.Fatalf("expected LLMD router metadata without fallback, got %+v", records[0])
	}
	if records[0].PromptTokens != 11 || records[0].CompletionTokens != 7 || records[0].TotalTokens != 18 {
		t.Fatalf("expected router token metadata, got %+v", records[0])
	}
}
