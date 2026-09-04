package enrollment

import (
	"os"
	"path/filepath"
	"strings"
)

const homeEnvironmentName = "BLUECLAW_HOME"

type Home struct {
	DirectoryPath string
}

func ResolveHome() Home {
	if configuredPath := strings.TrimSpace(os.Getenv(homeEnvironmentName)); configuredPath != "" {
		return Home{DirectoryPath: configuredPath}
	}
	if configurationHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); configurationHome != "" {
		return Home{DirectoryPath: filepath.Join(configurationHome, "blueclaw")}
	}
	userHome, errorValue := os.UserHomeDir()
	if errorValue != nil {
		return Home{DirectoryPath: ".blueclaw"}
	}
	return Home{DirectoryPath: filepath.Join(userHome, ".blueclaw")}
}

func (home Home) RuntimeConfigurationPath() string {
	return filepath.Join(home.DirectoryPath, "runtime.json")
}

func (home Home) PolicyPath() string {
	return filepath.Join(home.DirectoryPath, "policy.json")
}

func (home Home) ModelAPIKeyPath() string {
	return filepath.Join(home.DirectoryPath, "model-api-key")
}

func (home Home) WorkspaceRootPath() string {
	return filepath.Join(home.DirectoryPath, "workspace")
}

func (home Home) IsEnrolled() bool {
	if _, errorValue := os.Stat(home.RuntimeConfigurationPath()); errorValue != nil {
		return false
	}
	_, errorValue := os.Stat(home.PolicyPath())
	return errorValue == nil
}
