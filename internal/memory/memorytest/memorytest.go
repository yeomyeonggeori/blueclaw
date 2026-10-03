// Package memorytest opens memory files a test can write to and read back
// without a model or a network.
package memorytest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluememo"
	"github.com/yeomyeonggeori/bluememo/bluememotest"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

// Open gives a Stores under the test's own directory, embedding by hash and
// settling through a model that keeps each note as one statement. Nothing a
// test writes relates to what is already held.
func Open(t *testing.T) *memory.Stores {
	t.Helper()
	stores, _ := OpenWithRoot(t)
	return stores
}

// OpenWithRoot gives the same Stores and the workspace root it keeps its files
// under, for a test that has to put a file somewhere itself.
func OpenWithRoot(t *testing.T) (*memory.Stores, string) {
	t.Helper()
	root := t.TempDir()
	return openUnder(t, root, chooserAnswering(relationUnrelatedAnswer)), root
}

// OpenCorrecting gives a Stores whose judge reads every new statement as a
// correction of what it is nearest to, so a test can claim a supersede.
func OpenCorrecting(t *testing.T) *memory.Stores {
	t.Helper()
	return open(t, chooserAnswering(relationUpdatesAnswer))
}

func open(t *testing.T, chooser bluememo.Chooser) *memory.Stores {
	t.Helper()
	return openUnder(t, t.TempDir(), chooser)
}

func openUnder(t *testing.T, root string, chooser bluememo.Chooser) *memory.Stores {
	t.Helper()
	stores := memory.NewStores(root, bluememo.Configuration{
		Embedder:       &bluememotest.HashEmbedder{},
		EmbeddingModel: "test-embed",
		Model:          EchoModel{},
		Judge:          bluememo.DistributionJudge{Chooser: chooser},
	}, currentProcessActor{})
	t.Cleanup(func() { _ = stores.Close() })
	return stores
}

// fixedChooser answers each of the judge's questions the one way a test wants:
// the given relation, an importance that is not the noise rating, and the
// nearest candidate when a relation points at one.
// The judge numbers its relation answers the way RelationInstruction lists
// them.
const (
	relationUpdatesAnswer   = "2"
	relationUnrelatedAnswer = "4"
)

type fixedChooser struct {
	relation string
}

func (chooser fixedChooser) Choose(_ context.Context, request bluememo.ChoiceRequest) (map[string]float64, error) {
	answer := chooser.answerFor(request)
	distribution := make(map[string]float64, len(request.Answers))
	for _, offered := range request.Answers {
		distribution[offered] = 0
	}
	if _, isOffered := distribution[answer]; !isOffered {
		return nil, fmt.Errorf("the judge did not offer %q among %v", answer, request.Answers)
	}
	distribution[answer] = 1
	return distribution, nil
}

func (chooser fixedChooser) answerFor(request bluememo.ChoiceRequest) string {
	switch request.Instruction {
	case bluememo.RelationInstruction:
		return chooser.relation
	case bluememo.ImportanceInstruction:
		return "3"
	}
	return request.Answers[0]
}

func chooserAnswering(relation string) bluememo.Chooser {
	return fixedChooser{relation: relation}
}

// Remember settles one sentence into a scope, so a test reads back what it
// wrote.
func Remember(t *testing.T, stores *memory.Stores, scope memory.Scope, sentences ...string) {
	t.Helper()
	for _, sentence := range sentences {
		note := bluememo.Note{GroupID: memory.NewIdentifier(), Body: sentence, IsExplicit: true}
		if _, errorValue := stores.Remember(context.Background(), scope, note); errorValue != nil {
			t.Fatalf("remember %q into %s: %v", sentence, scope.Kind, errorValue)
		}
	}
}

type EchoModel struct{}

func (EchoModel) GenerateStructured(_ context.Context, request bluememo.StructuredRequest) (string, error) {
	if request.SchemaName == "memory_trigger" {
		return `{"phrases":[]}`, nil
	}
	content := request.Subject
	if marker := strings.LastIndex(content, "\nText\n"); marker >= 0 {
		content = content[marker+len("\nText\n"):]
	}
	content = strings.TrimSpace(content)
	if runes := []rune(content); len(runes) > bluememo.ContentCharacterLimit {
		content = string(runes[:bluememo.ContentCharacterLimit])
	}
	document, errorValue := json.Marshal(map[string]any{"propositions": []bluememo.Proposition{
		{Content: content, Expiry: bluememo.ExpiryNone},
	}})
	if errorValue != nil {
		return "", errorValue
	}
	return string(document), nil
}

// Count is how many memories a scope's file holds, so a test can claim that
// something wrote none of its own.
func Count(t *testing.T, stores *memory.Stores, scope memory.Scope) int {
	t.Helper()
	store, errorValue := stores.Store(context.Background(), scope)
	if errorValue != nil {
		t.Fatalf("open %s: %v", scope.Kind, errorValue)
	}
	memories, errorValue := store.Memories(context.Background())
	if errorValue != nil {
		t.Fatalf("list %s: %v", scope.Kind, errorValue)
	}
	return len(memories)
}

// Recall is what a reader is shown for a query, across the files their access
// opens.
func Recall(t *testing.T, stores *memory.Stores, scopes []memory.Scope, query string) []memory.MemoryFact {
	t.Helper()
	recalled, errorValue := stores.RecallAcross(context.Background(), policy.PersonAccess{PersonID: "reader"}, scopes, query, memory.DefaultRecallLimit)
	if errorValue != nil {
		t.Fatalf("recall %q: %v", query, errorValue)
	}
	return recalled.Facts
}
