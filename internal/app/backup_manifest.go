package app

import (
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/backup"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/store/postgres"
)

func buildBackupManifest(runtimeConfiguration config.RuntimeConfiguration, database postgres.Database) backup.Manifest {
	databaseKind := "none"
	requiredArtifacts := []string{"policy", "workspace"}
	if database.SQL != nil {
		databaseKind = "postgres"
		requiredArtifacts = append(requiredArtifacts, "blueclaw-postgres-dump")
	}
	return backup.Manifest{
		ContractVersion: 1,
		BlueclawVersion: "main",
		SchemaVersion:   "012_graphiti_memory_metadata",
		PersistentDataRoots: []string{
			"/workspace/.blueclaw",
			graphitiKuzuPath(runtimeConfiguration),
			runtimeConfiguration.Terminal.WorkspaceRootPath,
		},
		DatabaseKind:            databaseKind,
		RequiredBackupArtifacts: requiredArtifacts,
	}
}

func graphitiKuzuPath(runtimeConfiguration config.RuntimeConfiguration) string {
	if strings.TrimSpace(runtimeConfiguration.Memory.GraphitiKuzuPath) != "" {
		return strings.TrimSpace(runtimeConfiguration.Memory.GraphitiKuzuPath)
	}
	return "/workspace/.blueclaw/graphiti/kuzu"
}
