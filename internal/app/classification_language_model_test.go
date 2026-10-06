package app

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type namedLanguageModel struct {
	name string
}

func (languageModel namedLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return languageModel.name, nil
}

func (languageModel namedLanguageModel) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	return model.StructuredResponse{Content: languageModel.name}, nil
}

func languageModelName(t *testing.T, languageModel model.LanguageModelProvider) string {
	t.Helper()
	name, errorValue := languageModel.GenerateResponse(context.Background(), "")
	if errorValue != nil {
		t.Fatalf("unexpected error resolving language model name: %v", errorValue)
	}
	return name
}

func TestClassificationLanguageModelPrefersTheCheapestConfiguredTier(t *testing.T) {
	xLow := namedLanguageModel{name: "xlow"}
	high := namedLanguageModel{name: "high"}
	intakeProvider := namedLanguageModel{name: "intake"}

	withEveryTier := classificationLanguageModelProvider(agentcontract.TaskTierLanguageModels{XLow: xLow, High: high}, intakeProvider)
	if languageModelName(t, withEveryTier) != "xlow" {
		t.Fatalf("expected the xLow tier to classify, got %s", languageModelName(t, withEveryTier))
	}

	withoutXLow := classificationLanguageModelProvider(agentcontract.TaskTierLanguageModels{High: high}, intakeProvider)
	if languageModelName(t, withoutXLow) != "intake" {
		t.Fatalf("expected the intake provider to classify when no xLow tier is configured, got %s", languageModelName(t, withoutXLow))
	}

	withOnlyHigh := classificationLanguageModelProvider(agentcontract.TaskTierLanguageModels{High: high}, nil)
	if languageModelName(t, withOnlyHigh) != "high" {
		t.Fatalf("expected the high tier as the last resort, got %s", languageModelName(t, withOnlyHigh))
	}
}
