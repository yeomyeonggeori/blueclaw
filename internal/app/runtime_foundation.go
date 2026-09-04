package app

import (
	"context"
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
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
