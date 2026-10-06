package harnessdriver

import (
	"github.com/yeomyeonggeori/blueclaw/internal/acpharness"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

type Dependencies struct {
	RuntimeConfiguration        config.RuntimeConfiguration
	TaskRunStore                taskstate.TaskRunStore
	InstructionBundleLoader     func() agentcontract.InstructionBundle
	CompanyProvider             func() agentcontract.CompanyContext
	EmbeddingProvider           model.EmbeddingProvider
	EmbeddingModelName          string
	SkillIndexPath              string
	TaskTierLanguageModels      agentcontract.TaskTierLanguageModels
	IntakeLanguageModelProvider model.LanguageModelProvider
	DecisionModel               model.DecisionModel
	LLMCallRepository           taskstate.LLMCallRepository
}

type Factory func(Dependencies) (agentcontract.Harness, agentcontract.SkillRetriever)

type ACPFactory func(toolCatalogPublisher acpharness.ToolCatalogPublisher) Factory
