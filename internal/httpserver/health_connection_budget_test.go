package httpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/yeomyeonggeori/blueclaw/internal/store/postgres"
)

func saturatedDatabase(t *testing.T) (postgres.Database, func()) {
	t.Helper()
	connectionString := os.Getenv("BLUECLAW_TEST_POSTGRES_URL")
	if connectionString == "" {
		t.Skip("set BLUECLAW_TEST_POSTGRES_URL to run the disposable PostgreSQL regression")
	}
	database, errorValue := postgres.OpenDatabase(context.Background(), connectionString, 2)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	holding, releaseHolders := context.WithCancel(context.Background())
	var holders sync.WaitGroup
	for index := 0; index < 2; index++ {
		holders.Add(1)
		go func() {
			defer holders.Done()
			var one int
			_ = database.SQL.QueryRowContext(holding, "SELECT pg_sleep(30), 1").Scan(&one, &one)
		}()
	}
	for waited := time.Duration(0); database.SQL.Stats().InUse < 2; waited += 20 * time.Millisecond {
		if waited > 10*time.Second {
			t.Fatal("the pool never reached its bound, so this test proves nothing")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return database, func() {
		releaseHolders()
		holders.Wait()
		_ = database.Close()
	}
}

// The failure this distinguishes is the one the budget introduced: a database
// that is answering every other caller while this process has no share left.
func TestARanOutShareReadsAsOurOwnBudgetRatherThanAnAbsentServer(t *testing.T) {
	database, release := saturatedDatabase(t)
	defer release()

	started := time.Now()
	health := HealthHandler{Database: database}.databaseHealth(context.Background())
	elapsed := time.Since(started)

	if elapsed > 2*healthConnectionAcquireBudget {
		t.Fatalf("the health answer took %s, so it queued with everyone else instead of keeping a budget of its own", elapsed)
	}
	if !health.Connections.Exhausted {
		t.Fatalf("a saturated share did not report itself exhausted: %+v", health.Connections)
	}
	if health.Connections.InUse != health.Connections.Allowed {
		t.Fatalf("the share reports %d of %d in use, which is not what saturating it looks like", health.Connections.InUse, health.Connections.Allowed)
	}
	if !strings.Contains(health.Error, "every connection in this agent's share is in use") {
		t.Fatalf("the reported reason does not name the share running out: %q", health.Error)
	}

	reasons := healthFailureReasons(healthResponse{Database: health})
	if !containsReason(reasons, "postgres connection budget is exhausted") {
		t.Fatalf("the failure reasons do not name the budget: %v", reasons)
	}
	if containsReason(reasons, "postgres database is not reachable") {
		t.Fatalf("a database that is answering everyone else was reported absent: %v", reasons)
	}
}

// The other half of the same distinction: nothing about the budget may swallow
// a server that genuinely is not there.
func TestAnAbsentServerStillReadsAsAnAbsentServer(t *testing.T) {
	sqlDatabase, errorValue := sql.Open("pgx", "postgres://nobody@127.0.0.1:1/nothing")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	defer sqlDatabase.Close()
	sqlDatabase.SetMaxOpenConns(2)

	health := HealthHandler{Database: postgres.Database{SQL: sqlDatabase}}.databaseHealth(context.Background())

	if health.Connections.Exhausted {
		t.Fatalf("an unreachable server was reported as our own budget: %+v", health.Connections)
	}
	reasons := healthFailureReasons(healthResponse{Database: health})
	if !containsReason(reasons, "postgres database is not reachable") {
		t.Fatalf("the failure reasons do not name the absent server: %v", reasons)
	}
}

func TestTheHealthEndpointAnswersWhileTheShareIsFull(t *testing.T) {
	database, release := saturatedDatabase(t)
	defer release()

	recorder := httptest.NewRecorder()
	started := time.Now()
	HealthHandler{Database: database}.HandleHealth(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/health", nil))
	elapsed := time.Since(started)

	if elapsed > 2*healthConnectionAcquireBudget {
		t.Fatalf("the endpoint took %s to answer a saturated share", elapsed)
	}
	var answer struct {
		Status   string `json:"status"`
		Database struct {
			Connections connectionPoolHealth `json:"connections"`
		} `json:"database"`
		FailureReasons []string `json:"failureReasons"`
	}
	if errorValue := json.Unmarshal(recorder.Body.Bytes(), &answer); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !answer.Database.Connections.Exhausted {
		t.Fatalf("the answer does not carry the exhausted share: %s", recorder.Body.String())
	}
	if !containsReason(answer.FailureReasons, "postgres connection budget is exhausted") {
		t.Fatalf("the answer does not name the budget: %s", recorder.Body.String())
	}
}

func containsReason(reasons []string, wanted string) bool {
	for _, reason := range reasons {
		if reason == wanted {
			return true
		}
	}
	return false
}
