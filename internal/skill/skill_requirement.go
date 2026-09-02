package skill

import (
	"os"
	"strings"
)

type UnavailableSkill struct {
	Name                        string   `json:"name"`
	Description                 string   `json:"description,omitempty"`
	Path                        string   `json:"path"`
	MissingEnvironmentVariables []string `json:"missingEnvironmentVariables"`
}

func (skillBundle SkillBundle) MissingEnvironmentVariables() []string {
	missingVariableNames := []string{}
	for _, variableName := range skillBundle.RequiredEnvironmentVariables {
		value, isSet := os.LookupEnv(variableName)
		if !isSet || strings.TrimSpace(value) == "" {
			missingVariableNames = append(missingVariableNames, variableName)
		}
	}
	return missingVariableNames
}
