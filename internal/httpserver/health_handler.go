package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/protocolidentity"
	"github.com/yeomyeonggeori/blueclaw/internal/store/postgres"
)

type HealthHandler struct {
	Database                 postgres.Database
	ConnectorRuntime         *connectors.ConnectorRuntime
	MaximumBacklog           int
	ProtocolIdentity         *protocolidentity.Result
	ProtocolIdentityChecker  *protocolidentity.Checker
	ProtocolIdentityExpected protocolidentity.Identity
}

type healthResponse struct {
	Status           string                            `json:"status"`
	Database         databaseHealth                    `json:"database"`
	Connector        connectors.ConnectorRuntimeHealth `json:"connector"`
	Backlog          postgres.ConnectorDeliveryBacklog `json:"backlog"`
	ProtocolIdentity protocolidentity.Result           `json:"protocolIdentity"`
	FailureReasons   []string                          `json:"failureReasons,omitempty"`
	CheckedAt        time.Time                         `json:"checkedAt"`
}

type databaseHealth struct {
	Reachable         bool   `json:"reachable"`
	MigrationsApplied bool   `json:"migrationsApplied"`
	SchemaValid       bool   `json:"schemaValid"`
	Error             string `json:"error,omitempty"`
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
		Status:    "ok",
		CheckedAt: time.Now().UTC(),
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

func (healthHandler HealthHandler) databaseHealth(ctx context.Context) databaseHealth {
	if healthHandler.Database.SQL == nil {
		return databaseHealth{Error: "postgres database is not configured"}
	}
	if errorValue := healthHandler.Database.SQL.PingContext(ctx); errorValue != nil {
		return databaseHealth{Error: errorValue.Error()}
	}
	if errorValue := postgres.ValidateConnectorDeliverySchema(ctx, healthHandler.Database); errorValue != nil {
		return databaseHealth{Reachable: true, MigrationsApplied: false, Error: errorValue.Error()}
	}
	return databaseHealth{Reachable: true, MigrationsApplied: true, SchemaValid: true}
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
	if !response.Database.Reachable {
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
