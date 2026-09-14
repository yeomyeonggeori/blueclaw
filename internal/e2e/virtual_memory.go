package e2e

import (
	"context"
	"time"

	"github.com/yeomyeonggeori/bluememo"
	"github.com/yeomyeonggeori/bluememo/bluememotest"
)

func seedVirtualMemory(repository *bluememo.InMemoryRepository, facts []bluememo.Fact) error {
	for _, fact := range facts {
		episode := bluememo.Episode{EpisodeID: bluememo.NewIdentifier(), SourceKind: bluememo.EpisodeSourceKindImport, SourceID: fact.FactID, RequesterPersonID: fact.OwnerPersonID, Content: fact.Content, OccurredAt: time.Now().UTC()}
		fact.EpisodeID = episode.EpisodeID
		write := bluememo.EpisodeWrite{Episode: episode, Facts: []bluememo.FactWrite{{Fact: fact, Embedding: bluememotest.Embed(fact.Content)}}}
		if errorValue := repository.SaveEpisode(context.Background(), write); errorValue != nil {
			return errorValue
		}
	}
	return nil
}
