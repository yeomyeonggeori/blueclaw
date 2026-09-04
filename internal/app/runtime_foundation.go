package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	runtimelogging "github.com/yeomyeonggeori/blueclaw/internal/runtime"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/store/postgres"
)

func openRuntimeDatabase(runtimeConfiguration config.RuntimeConfiguration, logger *slog.Logger) (postgres.Database, error) {
	if strings.TrimSpace(runtimeConfiguration.Database.ConnectionString) == "" {
		return postgres.Database{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), databaseInitializationTimeout)
	defer cancel()
	logger.Info("application.open_database.phase", "phase", "connect")
	database, errorValue := postgres.OpenDatabase(ctx, runtimeConfiguration.Database.ConnectionString)
	if errorValue != nil {
		return postgres.Database{}, errorValue
	}
	logger.Info("application.open_database.phase", "phase", "validate_migration_directory")
	migrationDirectoryPath := strings.TrimSpace(runtimeConfiguration.Database.MigrationDirectoryPath)
	if migrationDirectoryPath == "" {
		migrationDirectoryPath = "migrations"
	}
	migrationRunner := postgres.MigrationRunner{MigrationDirectoryPath: migrationDirectoryPath, Logger: logger}
	if errorValue := postgres.ValidateConnectorMigrationDirectory(migrationRunner); errorValue != nil {
		_ = database.Close()
		return postgres.Database{}, errorValue
	}
	logger.Info("application.open_database.phase", "phase", "apply_migrations")
	if errorValue := migrationRunner.ApplyMigrations(ctx, database); errorValue != nil {
		_ = database.Close()
		return postgres.Database{}, errorValue
	}
	logger.Info("application.open_database.phase", "phase", "validate_schema")
	if errorValue := postgres.ValidateConnectorDeliverySchema(ctx, database); errorValue != nil {
		_ = database.Close()
		return postgres.Database{}, errorValue
	}
	logger.Info("application.open_database.phase", "phase", "ready")
	return database, nil
}

type runtimeFoundation struct {
	runtimeLogger     *runtimelogging.PersistentLogger
	logger            *slog.Logger
	database          postgres.Database
	policyLoader      policy.PolicyLoader
	policyDocument    policy.PolicyDocument
	posixSynchronizer security.POSIXSynchronizer
	startupError      error
}

func newRuntimeFoundation(runtimeConfiguration config.RuntimeConfiguration, policyPath string) runtimeFoundation {
	runtimeLogger, startupError := runtimelogging.NewPersistentLogger(runtimeConfiguration, time.Now())
	if startupError != nil {
		runtimeLogger = runtimelogging.NewDiscardLogger()
	}
	logger := runtimeLogger.Logger
	logger.Info("application.initializing", "stage", "open_database")
	database, databaseError := openRuntimeDatabase(runtimeConfiguration, logger)
	if databaseError != nil && startupError == nil {
		startupError = databaseError
	}
	logger.Info("application.initializing", "stage", "load_policy")
	policyLoader := policy.PolicyLoader{}
	policyDocument, _ := policyLoader.LoadPolicyDocument(policyPath)
	logger.Info("application.initializing", "stage", "posix_synchronize")
	posixSynchronizer := security.NewPOSIXSynchronizer(runtimeConfiguration.Terminal, policyPath)
	if errorValue := posixSynchronizer.Synchronize(context.Background()); errorValue != nil && startupError == nil {
		startupError = errorValue
	}
	logger.Info("application.initializing", "stage", "capability_socket_invariant")
	if errorValue := checkCapabilitySocketInvariant(runtimeConfiguration, policyDocument, logger); errorValue != nil && startupError == nil {
		startupError = errorValue
	}
	logger.Info("application.initializing", "stage", "project_policy")
	if database.SQL != nil {
		_ = postgres.NewPersonRepository(database).UpsertPeople(policyDocument)
	}
	return runtimeFoundation{
		runtimeLogger:     runtimeLogger,
		logger:            logger,
		database:          database,
		policyLoader:      policyLoader,
		policyDocument:    policyDocument,
		posixSynchronizer: posixSynchronizer,
		startupError:      startupError,
	}
}

func checkCapabilitySocketInvariant(runtimeConfiguration config.RuntimeConfiguration, policyDocument policy.PolicyDocument, logger *slog.Logger) error {
	result, errorValue := security.EnsureCapabilitySocketInvariant(runtimeConfiguration.Capabilities.UnixSocketPath, policyDocument)
	if result.Skipped {
		logger.Info("application.capability_socket_invariant.skipped", "reason", result.SkipReason)
		return errorValue
	}
	if errorValue == nil {
		logger.Info("application.capability_socket_invariant.passed", "socketPath", result.SocketPath, "group", result.GroupName)
	}
	return errorValue
}
