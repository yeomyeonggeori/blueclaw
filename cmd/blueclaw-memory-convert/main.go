// Command blueclaw-memory-convert carries the memory the retired store holds
// into one file per subject.
//
// It prints the placement and stops. Only -apply writes, and writing is
// idempotent, so a run interrupted halfway finishes by running again. The
// Postgres rows are left where they are.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/store/postgres"
	"github.com/yeomyeonggeori/bluememo"
)

func main() {
	connectionString := flag.String("postgres", os.Getenv("BLUECLAW_POSTGRES_URL"), "the database the retired memory lives in")
	memoryDirectory := flag.String("memory-directory", "", "where the per-subject files go")
	embeddingModel := flag.String("embedding-model", "", "the model the carried embeddings were made with, when it differs from the facts' own")
	embeddingWidth := flag.Int("embedding-width", 1024, "the width the store should hold; a carried vector of any other width is refused before the first write")
	apply := flag.Bool("apply", false, "write the memories, rather than only printing where they would go")
	flag.Parse()

	if errorValue := run(context.Background(), *connectionString, *memoryDirectory, *embeddingModel, *embeddingWidth, *apply); errorValue != nil {
		fmt.Fprintf(os.Stderr, "blueclaw-memory-convert: %v\n", errorValue)
		os.Exit(1)
	}
}

func run(ctx context.Context, connectionString string, memoryDirectory string, embeddingModel string, expectedEmbeddingWidth int, apply bool) error {
	database, errorValue := postgres.OpenDatabase(ctx, connectionString, 2)
	if errorValue != nil {
		return errorValue
	}
	defer database.SQL.Close()

	repository := postgres.NewLegacyMemoryRepository(database)
	facts, errorValue := repository.LiveFacts(ctx)
	if errorValue != nil {
		return fmt.Errorf("read the retired memory: %w", errorValue)
	}
	roster, errorValue := repository.Roster(ctx)
	if errorValue != nil {
		return fmt.Errorf("read the roster: %w", errorValue)
	}
	if embeddingModel != "" {
		for index := range facts {
			facts[index].EmbeddingModel = embeddingModel
		}
	}
	embeddingModel = carriedEmbeddingModel(facts, embeddingModel)

	placements := memory.PlanConversion(facts, roster)
	printPlan(facts, roster, placements)
	if !apply {
		fmt.Println("\nNothing was written. Pass -apply to carry it.")
		return nil
	}

	if strings.TrimSpace(memoryDirectory) == "" {
		return fmt.Errorf("-memory-directory is required to apply")
	}
	stores := memory.NewStores(memoryDirectory, bluememo.Configuration{EmbeddingModel: embeddingModel})
	defer stores.Close()
	report, errorValue := stores.Convert(ctx, facts, roster, expectedEmbeddingWidth)
	if errorValue != nil {
		return errorValue
	}
	fmt.Printf("\nCarried %d facts into %d memories (%d were already there, %d had no reader).\n",
		report.Facts, report.Memories, report.AlreadyHeld, report.Unread)
	return nil
}

func printPlan(facts []memory.LegacyFact, roster policy.PolicyProjection, placements []memory.Placement) {
	fmt.Printf("%d people on the roster, %d live facts\n\n", len(roster.PersonAccessByPersonID), len(facts))
	fmt.Printf("%-38s %-22s %-8s %-s\n", "fact", "old scope", "audience", "lands in")
	fmt.Println(strings.Repeat("-", 110))
	memories := 0
	byBoundary := map[string]int{}
	for _, placement := range placements {
		fmt.Printf("%-38s %-22s %-8d %s\n",
			placement.FactID, placement.OldScope, len(placement.Audience), placement.Boundary)
		memories += len(placement.Destinations)
		byBoundary[placement.Boundary]++
	}
	fmt.Printf("\n%d facts become %d memories\n\n", len(placements), memories)
	boundaries := make([]string, 0, len(byBoundary))
	for boundary := range byBoundary {
		boundaries = append(boundaries, boundary)
	}
	sort.Strings(boundaries)
	for _, boundary := range boundaries {
		fmt.Printf("  %3d facts -> %s\n", byBoundary[boundary], boundary)
	}
}

// carriedEmbeddingModel is the model the facts arrived with. A store has to be
// configured with it or recall passes over every vector it was just given.
func carriedEmbeddingModel(facts []memory.LegacyFact, given string) string {
	if given != "" {
		return given
	}
	for _, fact := range facts {
		if fact.EmbeddingModel != "" {
			return fact.EmbeddingModel
		}
	}
	return ""
}
