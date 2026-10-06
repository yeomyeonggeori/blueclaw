package bluecollaracp

import (
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/loop"
)

func newSkillRetriever(dependencies harnessdriver.Dependencies) agentcontract.SkillRetriever {
	skillRetriever := loop.NewEmbeddingSkillRetriever(dependencies.EmbeddingProvider, dependencies.SkillIndexPath)
	skillRetriever.EmbeddingModel = dependencies.EmbeddingModelName
	return skillRetriever
}
