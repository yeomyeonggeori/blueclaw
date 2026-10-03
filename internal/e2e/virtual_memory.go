package e2e

import (
	"context"

	"github.com/yeomyeonggeori/bluememo"
	"github.com/yeomyeonggeori/bluememo/bluememotest"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
)

// VirtualMemoryFact is a memory a scenario starts from. The store settles what
// it is told through a model that echoes one statement, so a scenario reads
// back the sentence it wrote.
type VirtualMemoryFact struct {
	PersonID string
	Content  string
	IsStatic bool
}

func openVirtualMemory(workspacePath string) *memory.Stores {
	return memory.NewStores(workspacePath, bluememo.Configuration{
		Embedder: &bluememotest.HashEmbedder{},
		Model:    virtualMemoryModel{},
		Judge:    bluememo.DistributionJudge{Chooser: bluememotest.ScriptedChooser{}},
	}, memorytest.ReadsInThisProcess())
}

func seedVirtualMemory(ctx context.Context, stores *memory.Stores, facts []VirtualMemoryFact) error {
	for _, fact := range facts {
		note := bluememo.Note{GroupID: memory.NewIdentifier(), Body: fact.Content, IsExplicit: true}
		if _, errorValue := stores.Remember(ctx, []memory.Scope{memory.PersonScope(fact.PersonID)}, note); errorValue != nil {
			return errorValue
		}
	}
	return nil
}
