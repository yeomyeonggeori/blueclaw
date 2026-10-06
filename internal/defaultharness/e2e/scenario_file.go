//go:build !nobundledharness

package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/bluecollar/turnoptions"
)

type virtualSessionScenarioFile struct {
	Name                      string                                  `json:"name"`
	ProfileName               string                                  `json:"profileName,omitempty"`
	SkillDirectoryPaths       []string                                `json:"skillDirectoryPaths"`
	AllowedTools              []string                                `json:"allowedTools"`
	CapabilityToolNames       []string                                `json:"capabilityToolNames"`
	CapabilityToolDescriptors []agentruntime.CapabilityToolDescriptor `json:"capabilityToolDescriptors,omitempty"`
	InitialToolNames          []string                                `json:"initialToolNames,omitempty"`
	TurnOptions               turnoptions.TurnOptions                 `json:"turnOptions,omitempty"`
	Steps                     []VirtualTurn                           `json:"steps"`
}

func LoadScenarioFile(path string, artifactDirectoryPath string) (VirtualSessionScenario, error) {
	document, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return VirtualSessionScenario{}, errorValue
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var scenarioFile virtualSessionScenarioFile
	if errorValue := decoder.Decode(&scenarioFile); errorValue != nil {
		return VirtualSessionScenario{}, fmt.Errorf("decode expensive scenario %s: %w", path, errorValue)
	}
	if errorValue := validateScenarioFile(scenarioFile); errorValue != nil {
		return VirtualSessionScenario{}, fmt.Errorf("invalid expensive scenario %s: %w", path, errorValue)
	}
	applyScenarioFileDefaults(&scenarioFile)
	return VirtualSessionScenario{
		Name:                      strings.TrimSpace(scenarioFile.Name),
		ProfileName:               strings.TrimSpace(scenarioFile.ProfileName),
		ArtifactDirectoryPath:     artifactDirectoryPath,
		SkillDirectoryPaths:       resolveScenarioSkillPaths(path, scenarioFile.SkillDirectoryPaths),
		AllowedTools:              trimmedScenarioValues(scenarioFile.AllowedTools),
		CapabilityToolNames:       trimmedScenarioValues(scenarioFile.CapabilityToolNames),
		CapabilityToolDescriptors: scenarioFile.CapabilityToolDescriptors,
		InitialToolNames:          trimmedScenarioValues(scenarioFile.InitialToolNames),
		TurnOptions:               scenarioFile.TurnOptions,
		Turns:                     scenarioFile.Steps,
	}, nil
}

func applyScenarioFileDefaults(scenarioFile *virtualSessionScenarioFile) {
	for stepIndex := range scenarioFile.Steps {
		step := &scenarioFile.Steps[stepIndex]
		if normalizedResponseExpectation(step.ExpectedResponse) == VirtualResponseReply && step.MinimumReplyLength == 0 {
			step.MinimumReplyLength = 1
		}
	}
}

func validateScenarioFile(scenarioFile virtualSessionScenarioFile) error {
	if strings.TrimSpace(scenarioFile.Name) == "" {
		return errors.New("name is required")
	}
	if len(scenarioFile.Steps) == 0 {
		return errors.New("at least one sequential step is required")
	}
	for stepIndex, step := range scenarioFile.Steps {
		if strings.TrimSpace(step.Prompt) == "" {
			return fmt.Errorf("step %d prompt is required", stepIndex+1)
		}
		if len(step.ActionResponses) > 0 {
			return fmt.Errorf("step %d actionResponses are not allowed in expensive scenarios", stepIndex+1)
		}
		if !isValidResponseExpectation(step.ExpectedResponse) {
			return fmt.Errorf("step %d expectedResponse %q is invalid", stepIndex+1, step.ExpectedResponse)
		}
	}
	return nil
}

func isValidResponseExpectation(expectation VirtualResponseExpectation) bool {
	switch normalizedResponseExpectation(expectation) {
	case VirtualResponseReply, VirtualResponseIgnore, VirtualResponseIgnoreOrReact, VirtualResponseReact, VirtualResponseBackgroundAction:
		return true
	default:
		return false
	}
}

func resolveScenarioSkillPaths(scenarioPath string, skillDirectoryPaths []string) []string {
	scenarioDirectoryPath := filepath.Dir(scenarioPath)
	resolvedPaths := make([]string, 0, len(skillDirectoryPaths))
	for _, skillDirectoryPath := range trimmedScenarioValues(skillDirectoryPaths) {
		if filepath.IsAbs(skillDirectoryPath) {
			resolvedPaths = append(resolvedPaths, filepath.Clean(skillDirectoryPath))
			continue
		}
		resolvedPaths = append(resolvedPaths, filepath.Clean(filepath.Join(scenarioDirectoryPath, skillDirectoryPath)))
	}
	return resolvedPaths
}

func trimmedScenarioValues(values []string) []string {
	trimmedValues := make([]string, 0, len(values))
	for _, value := range values {
		if trimmedValue := strings.TrimSpace(value); trimmedValue != "" {
			trimmedValues = append(trimmedValues, trimmedValue)
		}
	}
	return trimmedValues
}
