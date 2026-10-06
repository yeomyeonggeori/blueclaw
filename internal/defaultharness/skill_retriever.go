//go:build !nobundledharness

package defaultharness

import (
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/bluecollar/loop"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func newSkillRetriever(dependencies harnessdriver.Dependencies) agentcontract.SkillRetriever {
	skillRetriever := loop.NewEmbeddingSkillRetriever(dependencies.EmbeddingProvider, dependencies.SkillIndexPath)
	skillRetriever.EmbeddingModel = dependencies.EmbeddingModelName
	return skillRetriever
}
