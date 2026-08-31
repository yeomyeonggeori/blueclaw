package enrollment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
)

func Materialize(home Home, enrollment Enrollment) error {
	if errorValue := enrollment.Validate(); errorValue != nil {
		return errorValue
	}
	if errorValue := os.MkdirAll(home.DirectoryPath, 0o700); errorValue != nil {
		return errorValue
	}
	if errorValue := os.MkdirAll(enrollment.WorkspaceRootPath, 0o755); errorValue != nil {
		return errorValue
	}
	if errorValue := writeMigrations(filepath.Join(home.DirectoryPath, "migrations")); errorValue != nil {
		return errorValue
	}
	if errorValue := writeJSONDocument(home.RuntimeConfigurationPath(), runtimeConfigurationFor(home, enrollment)); errorValue != nil {
		return errorValue
	}
	return writeJSONDocument(home.PolicyPath(), policyDocumentFor(enrollment))
}

func runtimeConfigurationFor(home Home, enrollment Enrollment) config.RuntimeConfiguration {
	runtimeConfiguration := config.RuntimeConfiguration{
		BaseURL: "http://" + availableListenAddress(),
		Database: config.DatabaseConfiguration{
			Driver:                 "postgres",
			ConnectionString:       enrollment.DatabaseConnectionString,
			MigrationDirectoryPath: filepath.Join(home.DirectoryPath, "migrations"),
		},
		Terminal: config.TerminalConfiguration{
			Mode:              "native",
			WorkspaceRootPath: enrollment.WorkspaceRootPath,
			TimeoutSecond:     600,
		},
		Agent: config.AgentConfiguration{
			Harness: config.HarnessConfiguration{
				Name:             enrollment.Harness.Name,
				AgentCommandPath: enrollment.Harness.AgentCommandPath,
			},
		},
	}
	if socketPath := strings.TrimSpace(enrollment.LanguageModel.LLMDUnixSocketPath); socketPath != "" {
		runtimeConfiguration.LanguageModel.DefaultProvider = "llmd"
		runtimeConfiguration.LanguageModel.LLMD.UnixSocketPath = socketPath
		runtimeConfiguration.LanguageModel.LLMD.ExecutionMode = "auto"
	}
	return runtimeConfiguration
}

func policyDocumentFor(enrollment Enrollment) map[string]any {
	return map[string]any{
		"people": []map[string]any{{
			"personID":          enrollment.Operator.PersonID,
			"displayName":       enrollment.Operator.DisplayName,
			"emails":            []string{enrollment.Operator.Email},
			"securityLevelName": "admin",
			"securityLevelRank": 100,
			"circles":           []string{"member"},
			"isAdmin":           true,
		}},
		"circles": []map[string]any{{
			"circleID":               "member",
			"displayName":            "Member",
			"workspaceDirectoryPath": filepath.Join(enrollment.WorkspaceRootPath, "circles", "member"),
		}},
	}
}

func writeJSONDocument(path string, document any) error {
	encodedDocument, errorValue := json.MarshalIndent(document, "", "  ")
	if errorValue != nil {
		return errorValue
	}
	return os.WriteFile(path, append(encodedDocument, '\n'), 0o600)
}
