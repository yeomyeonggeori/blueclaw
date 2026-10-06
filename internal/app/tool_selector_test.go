package app

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type namedToolSelector struct{}

func (namedToolSelector) SelectToolNames(context.Context, agentcontract.ToolSelectionNeed) ([]agentcontract.SelectedTool, error) {
	return nil, nil
}

type unusedDecisionModel struct{}

func (unusedDecisionModel) Decide(context.Context, model.DecisionRequest) (model.DecisionResponse, error) {
	return model.DecisionResponse{}, nil
}

func TestTheToolSelectorComesFromTheBundledHarnessOrNotAtAll(t *testing.T) {
	bundled := func(model.DecisionModel) agentcontract.ToolSelector { return namedToolSelector{} }

	if newToolSelector(nil, unusedDecisionModel{}) != nil {
		t.Fatal("a build with no bundled harness has no tool selector to offer")
	}
	if newToolSelector(bundled, nil) != nil {
		t.Fatal("a selector needs a decision model to ask")
	}
	if _, isBundled := newToolSelector(bundled, unusedDecisionModel{}).(namedToolSelector); !isBundled {
		t.Fatal("the bundled harness supplies the selector")
	}
}
