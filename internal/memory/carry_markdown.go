package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yeomyeonggeori/bluememo"
)

const markdownMemoryFileName = "MEMORY.md"

// CarryMarkdownMemory brings the memory blueclaw kept before bluememo, one
// MEMORY.md of bullet facts per person, into that person's store. Each fact
// is adopted in its own words, so no model is asked, and its identifier is
// derived from the fact, so carrying twice holds it once. A carried file is
// renamed rather than deleted. A file of somebody the roster no longer names
// is left and reported: their protected directory is not declared, so a store
// made there could not be read by anyone.
func (stores *Stores) CarryMarkdownMemory(ctx context.Context, isOnTheRoster func(personID string) bool) (CarryReport, error) {
	report := CarryReport{}
	peopleDirectory := filepath.Join(stores.workspaceRootPath, ".blueclaw", "memory", "people")
	entries, errorValue := os.ReadDir(peopleDirectory)
	if errors.Is(errorValue, os.ErrNotExist) {
		return report, nil
	}
	if errorValue != nil {
		return report, errorValue
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		personID := entry.Name()
		path := filepath.Join(peopleDirectory, personID, markdownMemoryFileName)
		if held, errorValue := holdsFile(path); errorValue != nil || !held {
			if errorValue != nil {
				return report, errorValue
			}
			continue
		}
		if !isOnTheRoster(personID) {
			report.Left = append(report.Left, path)
			continue
		}
		if errorValue := stores.carryMarkdownFile(ctx, path, personID); errorValue != nil {
			return report, fmt.Errorf("carry %s: %w", path, errorValue)
		}
		report.Carried++
	}
	return report, nil
}

func (stores *Stores) carryMarkdownFile(ctx context.Context, path string, personID string) error {
	information, errorValue := os.Stat(path)
	if errorValue != nil {
		return errorValue
	}
	document, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return errorValue
	}
	adopted := []bluememo.AdoptedMemory{}
	for _, fact := range markdownFacts(string(document)) {
		adopted = append(adopted, bluememo.AdoptedMemory{Memory: bluememo.Memory{
			MemoryID:   markdownFactIdentifier(personID, fact),
			Content:    fact,
			OccurredAt: information.ModTime().UTC(),
		}})
	}
	store, errorValue := stores.Store(ctx, PersonScope(personID))
	if errorValue != nil {
		return errorValue
	}
	if _, errorValue := store.Adopt(ctx, adopted); errorValue != nil {
		return errorValue
	}
	return os.Rename(path, path+".carried")
}

// markdownFacts reads the bullets of a MEMORY.md; headings and prose around
// them were the compressor's framing, not facts.
func markdownFacts(document string) []string {
	facts := []string{}
	for _, line := range strings.Split(document, "\n") {
		trimmed := strings.TrimSpace(line)
		for _, marker := range []string{"- ", "* "} {
			if fact, isBullet := strings.CutPrefix(trimmed, marker); isBullet {
				if fact = strings.TrimSpace(fact); fact != "" {
					facts = append(facts, fact)
				}
				break
			}
		}
	}
	return facts
}

func markdownFactIdentifier(personID string, fact string) string {
	digest := sha256.Sum256([]byte(personID + "\n" + fact))
	return "markdown-" + hex.EncodeToString(digest[:12])
}
