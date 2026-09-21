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
	MissingToolNames            []string `json:"missingToolNames,omitempty"`
	MissingAnyFilePaths         []string `json:"missingAnyFilePaths,omitempty"`
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

// The declared paths are alternatives, so one of them being a readable file
// satisfies all of them. When none is, the whole list is what the reader needs:
// any one of these would have done.
//
// This asks the filesystem whether a path is there and never opens it for its
// contents. A path a skill has no business reading is refused by its owner and
// its mode bits, the same boundary every other file this runtime touches
// answers to, so there is no path vocabulary here to keep in step with one.
func (skillBundle SkillBundle) MissingAnyFilePaths() []string {
	if len(skillBundle.RequiredAnyFilePaths) == 0 {
		return []string{}
	}
	for _, requiredPath := range skillBundle.RequiredAnyFilePaths {
		if isReadableFile(requiredPath) {
			return []string{}
		}
	}
	return append([]string{}, skillBundle.RequiredAnyFilePaths...)
}

func isReadableFile(path string) bool {
	information, errorValue := os.Stat(path)
	if errorValue != nil || !information.Mode().IsRegular() {
		return false
	}
	file, errorValue := os.Open(path)
	if errorValue != nil {
		return false
	}
	_ = file.Close()
	return true
}
