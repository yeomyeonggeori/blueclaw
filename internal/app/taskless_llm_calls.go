package app

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/store/postgres"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type tasklessLLMCallRecorder func(subjects []string, record agentcontract.LLMCallRecord)

func newTasklessLLMCallRecorder(repository *postgres.LLMCallRepository, logger *slog.Logger) tasklessLLMCallRecorder {
	if repository == nil {
		return nil
	}
	return func(subjects []string, record agentcontract.LLMCallRecord) {
		if record.Exchange == nil && record.Input == nil {
			return
		}
		body, errorValue := json.Marshal(record)
		if errorValue != nil {
			return
		}
		taskEvent := task.TaskEvent{TaskEventID: task.NewIdentifier(), Name: agentcontract.TaskEventLLMCall, Body: string(body), CreatedAt: time.Now()}
		if errorValue := repository.InsertTasklessLLMCall(taskEvent, subjects, record); errorValue != nil {
			logger.Warn("llm_call.taskless_record_failed", "subjects", subjects, "kind", record.Kind, "error", errorValue.Error())
		}
	}
}

func (recorder tasklessLLMCallRecorder) observer() agentcontract.LLMCallObserver {
	if recorder == nil {
		return nil
	}
	return func(record agentcontract.LLMCallRecord) { recorder(nil, record) }
}

func observedTaskTierLanguageModels(languageModels agentcontract.TaskTierLanguageModels, observe agentcontract.LLMCallObserver) agentcontract.TaskTierLanguageModels {
	return agentcontract.TaskTierLanguageModels{
		XLow:   observedLanguageModel(languageModels.XLow, observe),
		Low:    observedLanguageModel(languageModels.Low, observe),
		Medium: observedLanguageModel(languageModels.Medium, observe),
		High:   observedLanguageModel(languageModels.High, observe),
		XHigh:  observedLanguageModel(languageModels.XHigh, observe),
		Max:    observedLanguageModel(languageModels.Max, observe),
	}
}

func observedLanguageModel(languageModel model.LanguageModelProvider, observe agentcontract.LLMCallObserver) model.LanguageModelProvider {
	if languageModel == nil {
		return nil
	}
	return agentcontract.ObserveLanguageModel(languageModel, observe)
}
