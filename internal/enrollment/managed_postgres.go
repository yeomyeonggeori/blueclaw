package enrollment

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"github.com/yeomyeonggeori/blueclaw/internal/store/postgres"
)

const (
	managedDatabaseName = "blueclaw"
	managedAccountName  = "blueclaw"
	managedPassword     = "blueclaw"
	managedPortEnvName  = "BLUECLAW_MANAGED_POSTGRES_PORT"
	defaultManagedPort  = 25432
)

type ManagedPostgres struct {
	home Home
}

func NewManagedPostgres(home Home) ManagedPostgres {
	return ManagedPostgres{home: home}
}

func (managed ManagedPostgres) DirectoryPath() string {
	return filepath.Join(managed.home.DirectoryPath, "postgres")
}

func (managed ManagedPostgres) Port() uint32 {
	if configuredPort := strings.TrimSpace(os.Getenv(managedPortEnvName)); configuredPort != "" {
		if parsedPort, errorValue := strconv.ParseUint(configuredPort, 10, 32); errorValue == nil {
			return uint32(parsedPort)
		}
	}
	return defaultManagedPort
}

func (managed ManagedPostgres) ConnectionString() string {
	return "postgres://" + managedAccountName + ":" + managedPassword + "@127.0.0.1:" + strconv.FormatUint(uint64(managed.Port()), 10) + "/" + managedDatabaseName + "?sslmode=disable"
}

func (managed ManagedPostgres) IsInstalled() bool {
	_, errorValue := os.Stat(filepath.Join(managed.DirectoryPath(), "data"))
	return errorValue == nil
}

func (managed ManagedPostgres) server() *embeddedpostgres.EmbeddedPostgres {
	directoryPath := managed.DirectoryPath()
	return embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Username(managedAccountName).
		Password(managedPassword).
		Database(managedDatabaseName).
		Port(managed.Port()).
		RuntimePath(filepath.Join(directoryPath, "runtime")).
		BinariesPath(filepath.Join(directoryPath, "binaries")).
		DataPath(filepath.Join(directoryPath, "data")).
		CachePath(filepath.Join(directoryPath, "cache")))
}

func (managed ManagedPostgres) EnsureRunning(ctx context.Context) error {
	if managed.IsReachable(ctx) {
		return nil
	}
	if errorValue := os.MkdirAll(managed.DirectoryPath(), 0o700); errorValue != nil {
		return errorValue
	}
	if errorValue := managed.server().Start(); errorValue != nil {
		return errors.New("blueclaw could not start the database it manages for you: " + errorValue.Error())
	}
	if !managed.IsReachable(ctx) {
		return errors.New("blueclaw started its database but cannot reach it, so something else is holding port " + strconv.FormatUint(uint64(managed.Port()), 10))
	}
	return nil
}

func (managed ManagedPostgres) IsReachable(ctx context.Context) bool {
	connectContext, cancel := context.WithTimeout(ctx, databaseCheckTimeout)
	defer cancel()
	database, errorValue := postgres.OpenDatabase(connectContext, managed.ConnectionString(), 0)
	if errorValue != nil {
		return false
	}
	database.Close()
	return true
}
