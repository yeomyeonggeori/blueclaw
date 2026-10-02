package postgres

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestAFreshDatabaseMigratesUnderAnOwnerThatMayNotCreateVector(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	administrator := administratorDatabase(t, ctx)
	if !offersTheVectorExtension(t, ctx, administrator) {
		t.Skip("this PostgreSQL has no vector extension installed, so the owner could not have been tempted to create it")
	}
	database := databaseUnderAnOrdinaryOwner(t, ctx, administrator)
	if errorValue := (MigrationRunner{MigrationDirectoryPath: "../../../migrations"}).ApplyMigrations(ctx, database); errorValue != nil {
		t.Fatalf("a fresh database owned by an ordinary role did not migrate: %v", errorValue)
	}
	var hasVector bool
	if errorValue := database.SQL.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector')`).Scan(&hasVector); errorValue != nil {
		t.Fatal(errorValue)
	}
	if hasVector {
		t.Fatal("the migrations created the vector extension, and nothing the agent keeps in Postgres uses it")
	}
}

func administratorDatabase(t *testing.T, ctx context.Context) Database {
	t.Helper()
	connectionString := os.Getenv("BLUECLAW_TEST_POSTGRES_URL")
	if connectionString == "" {
		t.Skip("BLUECLAW_TEST_POSTGRES_URL is not configured")
	}
	parsedURL, errorValue := url.Parse(connectionString)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	parsedURL.Path = "/postgres"
	administrator, errorValue := OpenDatabase(ctx, parsedURL.String(), 0)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() { administrator.Close() })
	return administrator
}

func offersTheVectorExtension(t *testing.T, ctx context.Context, administrator Database) bool {
	t.Helper()
	var isOffered bool
	if errorValue := administrator.SQL.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'vector')`).Scan(&isOffered); errorValue != nil {
		t.Fatal(errorValue)
	}
	return isOffered
}

func databaseUnderAnOrdinaryOwner(t *testing.T, ctx context.Context, administrator Database) Database {
	t.Helper()
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())))
	ownerName := fmt.Sprintf("ordinary_%x", digest[:6])
	password := fmt.Sprintf("%x", digest[6:18])
	for _, statement := range []string{
		"CREATE ROLE " + ownerName + " LOGIN NOSUPERUSER PASSWORD '" + password + "'",
		"CREATE DATABASE " + ownerName + " OWNER " + ownerName,
	} {
		if _, errorValue := administrator.SQL.ExecContext(ctx, statement); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	databaseURL, errorValue := url.Parse(os.Getenv("BLUECLAW_TEST_POSTGRES_URL"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	databaseURL.User = url.UserPassword(ownerName, password)
	databaseURL.Path = "/" + ownerName
	database, errorValue := OpenDatabase(ctx, databaseURL.String(), 0)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() {
		database.Close()
		_, _ = administrator.SQL.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+ownerName+" WITH (FORCE)")
		_, _ = administrator.SQL.ExecContext(context.Background(), "DROP ROLE IF EXISTS "+ownerName)
	})
	return database
}
