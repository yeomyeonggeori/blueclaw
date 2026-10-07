package postgres

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestConcurrentRunnersMigrateAFreshDatabaseOneAtATime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	administrator := administratorDatabase(t, ctx)
	database := databaseUnderAnOrdinaryOwner(t, ctx, administrator)
	runner := MigrationRunner{MigrationDirectoryPath: "../../../migrations"}

	const runnerCount = 8
	errorValues := make([]error, runnerCount)
	var group sync.WaitGroup
	for index := range errorValues {
		group.Add(1)
		go func() {
			defer group.Done()
			errorValues[index] = runner.ApplyMigrations(ctx, database)
		}()
	}
	group.Wait()

	for index, errorValue := range errorValues {
		if errorValue != nil {
			t.Errorf("runner %d: %v", index, errorValue)
		}
	}
}
