package harnessdriver

import (
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

type Dependencies struct {
	RuntimeConfiguration        config.RuntimeConfiguration
	TaskRunStore                taskstate.TaskRunStore
	TaskStepStore               taskstate.TaskStepStore
	TaskArtifactStore           taskstate.TaskArtifactStore
	ToolResultSpillStore        agentruntime.ToolResultSpillStore
	ToolResultImageSource       agentruntime.ToolResultImageSource
	InstructionBundleLoader     func() agentcontract.InstructionBundle
	CompanyProvider             func() agentcontract.CompanyContext
	EmbeddingProvider           model.EmbeddingProvider
	EmbeddingModelName          string
	SkillIndexPath              string
	TaskTierLanguageModels      agentcontract.TaskTierLanguageModels
	IntakeLanguageModelProvider model.LanguageModelProvider

	IntakeOptions       *agentcontract.IntakeOptions
	TurnOptionOverrides agentcontract.TurnOptions
}

type Factory func(Dependencies) (agentcontract.Harness, agentcontract.SkillRetriever)
