package httpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/protocolidentity"
	"github.com/yeomyeonggeori/blueclaw/internal/store/postgres"
)

type HealthHandler struct {
	LanguageModel            LanguageModelHealth
	Database                 postgres.Database
	ConnectorRuntime         *connectors.ConnectorRuntime
	MaximumBacklog           int
	ProtocolIdentity         *protocolidentity.Result
	ProtocolIdentityChecker  *protocolidentity.Checker
	ProtocolIdentityExpected protocolidentity.Identity
}

type healthResponse struct {
	LanguageModel    LanguageModelHealth               `json:"languageModel"`
	Status           string                            `json:"status"`
	Database         databaseHealth                    `json:"database"`
	Connector        connectors.ConnectorRuntimeHealth `json:"connector"`
	Backlog          postgres.ConnectorDeliveryBacklog `json:"backlog"`
	ProtocolIdentity protocolidentity.Result           `json:"protocolIdentity"`
	FailureReasons   []string                          `json:"failureReasons,omitempty"`
	CheckedAt        time.Time                         `json:"checkedAt"`
}

type LanguageModelHealth struct {
	Configured bool   `json:"configured"`
	Error      string `json:"error,omitempty"`
}

type databaseHealth struct {
	Reachable         bool                 `json:"reachable"`
	MigrationsApplied bool                 `json:"migrationsApplied"`
	SchemaValid       bool                 `json:"schemaValid"`
	Error             string               `json:"error,omitempty"`
	Connections       connectionPoolHealth `json:"connections"`
}

type connectionPoolHealth struct {
	Allowed      int    `json:"allowed"`
	InUse        int    `json:"inUse"`
	Idle         int    `json:"idle"`
	Exhausted    bool   `json:"exhausted"`
	WaitCount    int64  `json:"waitCount"`
	WaitDuration string `json:"waitDuration"`
}

func (healthHandler HealthHandler) HandleHealth(responseWriter http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 10*time.Second)
	defer cancel()

	response := healthHandler.health(ctx)
	statusCode := http.StatusOK
	if response.Status != "ok" {
		statusCode = http.StatusServiceUnavailable
	}
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(statusCode)
	_ = json.NewEncoder(responseWriter).Encode(response)
}

func (healthHandler HealthHandler) health(ctx context.Context) healthResponse {
	response := healthResponse{
		Status:        "ok",
		CheckedAt:     time.Now().UTC(),
		LanguageModel: healthHandler.LanguageModel,
	}
	if healthHandler.MaximumBacklog <= 0 {
		healthHandler.MaximumBacklog = 1000
	}
	if healthHandler.ConnectorRuntime != nil {
		response.Connector = healthHandler.ConnectorRuntime.Health()
	}
	if healthHandler.ProtocolIdentityChecker != nil {
		response.ProtocolIdentity = healthHandler.ProtocolIdentityChecker.Check(ctx, healthHandler.ProtocolIdentityExpected)
	} else if healthHandler.ProtocolIdentity != nil {
		response.ProtocolIdentity = *healthHandler.ProtocolIdentity
	}
	response.Database = healthHandler.databaseHealth(ctx)
	if response.Database.SchemaValid {
		backlog, errorValue := postgres.QueryConnectorDeliveryBacklog(ctx, healthHandler.Database)
		if errorValue != nil {
			response.FailureReasons = append(response.FailureReasons, errorValue.Error())
		} else {
			response.Backlog = backlog
			response.FailureReasons = append(response.FailureReasons, healthHandler.backlogFailureReasons(backlog)...)
		}
	}
	response.FailureReasons = append(response.FailureReasons, healthFailureReasons(response)...)
	if len(response.FailureReasons) > 0 {
		response.Status = "unhealthy"
	}
	return response
}

// A bounded pool does not fail when it runs out, it waits. A health request
// that only pinged would wait with everyone else and then report the database
// unreachable while the database was answering every other caller. So the
// connection is taken with a budget of its own and the ping runs on that
// connection: a budget that expires while every connection in the share is in
// use is our own share running out, and anything else is the server.
//
// Two seconds because the company host's own healthcheck gives curl five
// (host/quickstart/compose.yaml), and an answer that arrives after the caller
// has given up is the silence this field exists to break.
const healthConnectionAcquireBudget = 2 * time.Second

var errTheShareRanOut = errors.New("every connection in this agent's share is in use")

func (healthHandler HealthHandler) databaseHealth(ctx context.Context) databaseHealth {
	if healthHandler.Database.SQL == nil {
		return databaseHealth{Error: "postgres database is not configured"}
	}
	reachError := healthHandler.reachDatabaseWithinItsShare(ctx)
	connections := connectionPoolHealthOf(healthHandler.Database.SQL.Stats())
	connections.Exhausted = errors.Is(reachError, errTheShareRanOut)
	if reachError != nil {
		return databaseHealth{Error: reachError.Error(), Connections: connections}
	}
	if errorValue := postgres.ValidateConnectorDeliverySchema(ctx, healthHandler.Database); errorValue != nil {
		return databaseHealth{Reachable: true, MigrationsApplied: false, Error: errorValue.Error(), Connections: connections}
	}
	return databaseHealth{Reachable: true, MigrationsApplied: true, SchemaValid: true, Connections: connections}
}

func (healthHandler HealthHandler) reachDatabaseWithinItsShare(ctx context.Context) error {
	acquireContext, cancel := context.WithTimeout(ctx, healthConnectionAcquireBudget)
	defer cancel()
	connection, errorValue := healthHandler.Database.SQL.Conn(acquireContext)
	if errorValue != nil {
		return healthHandler.describeUnreachableDatabase(ctx, acquireContext, errorValue)
	}
	defer connection.Close()
	return connection.PingContext(ctx)
}

// Conn waits for a connection in the share and dials a new one when the share
// has room, so a budget that expired tells us nothing on its own: a slow dial
// expires it too. What separates the two is whether the share had room at the
// moment it expired.
func (healthHandler HealthHandler) describeUnreachableDatabase(ctx context.Context, acquireContext context.Context, errorValue error) error {
	statistics := healthHandler.Database.SQL.Stats()
	budgetExpired := acquireContext.Err() != nil && ctx.Err() == nil
	if !budgetExpired || statistics.MaxOpenConnections <= 0 || statistics.InUse < statistics.MaxOpenConnections {
		return errorValue
	}
	return fmt.Errorf("%w: %d of %d, and %d callers have queued for one",
		errTheShareRanOut, statistics.InUse, statistics.MaxOpenConnections, statistics.WaitCount)
}

func connectionPoolHealthOf(statistics sql.DBStats) connectionPoolHealth {
	return connectionPoolHealth{
		Allowed:      statistics.MaxOpenConnections,
		InUse:        statistics.InUse,
		Idle:         statistics.Idle,
		WaitCount:    statistics.WaitCount,
		WaitDuration: statistics.WaitDuration.Round(time.Millisecond).String(),
	}
}

func (healthHandler HealthHandler) backlogFailureReasons(backlog postgres.ConnectorDeliveryBacklog) []string {
	totalBacklog := backlog.RawEventPendingCount + backlog.RawEventRunningCount + backlog.OutboxPendingCount + backlog.OutboxRunningCount
	if totalBacklog <= healthHandler.MaximumBacklog {
		return nil
	}
	return []string{"connector backlog exceeds limit"}
}

func healthFailureReasons(response healthResponse) []string {
	failureReasons := []string{}
	if !response.LanguageModel.Configured {
		failureReasons = append(failureReasons, "language model configuration is not valid")
	}
	if !response.Database.Reachable && response.Database.Connections.Exhausted {
		failureReasons = append(failureReasons, "postgres connection budget is exhausted")
	} else if !response.Database.Reachable {
		failureReasons = append(failureReasons, "postgres database is not reachable")
	}
	if !response.Database.SchemaValid {
		failureReasons = append(failureReasons, "database schema is not valid")
	}
	if !response.Connector.Passed {
		failureReasons = append(failureReasons, "connector runtime is not healthy")
	}
	if !response.ProtocolIdentity.CheckedAt.IsZero() && !response.ProtocolIdentity.Passed {
		failureReasons = append(failureReasons, "protocol identity is not valid")
	}
	return failureReasons
}
