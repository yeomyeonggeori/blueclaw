package postgres

import (
	"context"
	"database/sql"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestAPoolIsBoundedOnEveryAxis(t *testing.T) {
	sqlDatabase, errorValue := sql.Open("pgx", "postgres://nobody@127.0.0.1:1/nothing")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	defer sqlDatabase.Close()

	boundToTheConnectionBudget(sqlDatabase, 30)

	if openConnections := sqlDatabase.Stats().MaxOpenConnections; openConnections != 30 {
		t.Fatalf("the pool may open %d connections, wanted 30", openConnections)
	}
	assertPoolSetting(t, sqlDatabase, "maxIdleCount", 30)
	assertPoolSetting(t, sqlDatabase, "maxIdleTime", int64(connectionMaxIdleTime))
	assertPoolSetting(t, sqlDatabase, "maxLifetime", int64(connectionMaxLifetime))
}

func TestAnUnconfiguredPoolTakesWhatTheServerAllows(t *testing.T) {
	connectionString := os.Getenv("BLUECLAW_TEST_POSTGRES_URL")
	if connectionString == "" {
		t.Skip("set BLUECLAW_TEST_POSTGRES_URL to run the disposable PostgreSQL regression")
	}
	ctx := context.Background()
	database, errorValue := OpenDatabase(ctx, connectionString, 0)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	defer database.Close()

	allowed, errorValue := connectionsTheServerAllows(ctx, database.SQL)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if openConnections := database.SQL.Stats().MaxOpenConnections; openConnections != allowed {
		t.Fatalf("the pool may open %d connections, wanted the %d this server allows", openConnections, allowed)
	}
}

func TestAConfiguredPoolTakesTheShareItWasGiven(t *testing.T) {
	connectionString := os.Getenv("BLUECLAW_TEST_POSTGRES_URL")
	if connectionString == "" {
		t.Skip("set BLUECLAW_TEST_POSTGRES_URL to run the disposable PostgreSQL regression")
	}
	database, errorValue := OpenDatabase(context.Background(), connectionString, 4)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	defer database.Close()

	if openConnections := database.SQL.Stats().MaxOpenConnections; openConnections != 4 {
		t.Fatalf("the pool may open %d connections, wanted the 4 it was configured with", openConnections)
	}
	assertPoolSetting(t, database.SQL, "maxIdleCount", 4)
}

func assertPoolSetting(t *testing.T, sqlDatabase *sql.DB, fieldName string, want int64) {
	t.Helper()
	field := reflect.ValueOf(sqlDatabase).Elem().FieldByName(fieldName)
	if !field.IsValid() {
		t.Fatalf("database/sql no longer keeps its pool settings in a field called %s", fieldName)
	}
	if field.Int() != want {
		t.Fatalf("%s is %s, wanted %s", fieldName, time.Duration(field.Int()), time.Duration(want))
	}
}
