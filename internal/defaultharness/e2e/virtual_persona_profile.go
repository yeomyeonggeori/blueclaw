//go:build !nobundledharness

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/persona"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

const (
	virtualPersonaProfileGetToolName    = "persona_profile_get"
	virtualPersonaProfileUpdateToolName = "persona_profile_update"
)

func (service *virtualCapabilityService) personaProfileResponse(toolName string, requestBody []byte) string {
	personID := strings.TrimSpace(virtualInvokeContextOf(requestBody).RequesterPersonID)
	if personID == "" {
		return virtualCapabilityInvalidInput(toolName, "the call names no requester")
	}
	path := filepath.Join(security.PersonHomeDirectoryPath(service.workspacePath, personID), persona.UserDocumentRelativePath)
	current, errorValue := virtualPersonaProfile(path)
	if errorValue != nil {
		return virtualCapabilityInvalidInput(toolName, errorValue.Error())
	}
	if toolName == virtualPersonaProfileGetToolName {
		return virtualPersonaProfileSuccess(toolName, current)
	}
	updated, errorValue := virtualPersonaProfileMerged(current, virtualCapabilityInput(requestBody))
	if errorValue != nil {
		return virtualCapabilityInvalidInput(toolName, errorValue.Error())
	}
	if errorValue := os.MkdirAll(filepath.Dir(path), 0700); errorValue != nil {
		return virtualCapabilityInvalidInput(toolName, errorValue.Error())
	}
	if errorValue := os.WriteFile(path, updated, 0600); errorValue != nil {
		return virtualCapabilityInvalidInput(toolName, errorValue.Error())
	}
	return virtualPersonaProfileSuccess(toolName, updated)
}

func virtualPersonaProfile(path string) ([]byte, error) {
	document, errorValue := os.ReadFile(path)
	if os.IsNotExist(errorValue) {
		return persona.CanonicalUser(persona.User{})
	}
	if errorValue != nil {
		return nil, errorValue
	}
	user, errorValue := persona.ParseUser(document)
	if errorValue != nil {
		return nil, errorValue
	}
	return persona.CanonicalUser(user)
}

func virtualPersonaProfileMerged(current []byte, patch map[string]any) ([]byte, error) {
	var document any
	if errorValue := json.Unmarshal(current, &document); errorValue != nil {
		return nil, errorValue
	}
	merged, errorValue := json.Marshal(mergePatch(document, patch))
	if errorValue != nil {
		return nil, errorValue
	}
	user, errorValue := persona.ParseUser(merged)
	if errorValue != nil {
		return nil, errorValue
	}
	return persona.CanonicalUser(user)
}

// RFC 7396, JSON Merge Patch, section 2.
func mergePatch(target any, patch any) any {
	patchObject, isPatchObject := patch.(map[string]any)
	if !isPatchObject {
		return patch
	}
	merged := map[string]any{}
	if targetObject, isTargetObject := target.(map[string]any); isTargetObject {
		for name, value := range targetObject {
			merged[name] = value
		}
	}
	for name, value := range patchObject {
		if value == nil {
			delete(merged, name)
			continue
		}
		merged[name] = mergePatch(merged[name], value)
	}
	return merged
}

func virtualPersonaProfileSuccess(toolName string, document []byte) string {
	var result map[string]any
	_ = json.Unmarshal(document, &result)
	return virtualCapabilityJSON(map[string]any{
		"provider": "virtual", "selectedBackend": "device", "toolName": toolName, "outcome": "succeeded", "status": "ok",
		"content": string(document), "result": result, "effects": virtualSingletonEffects(toolName),
	})
}

func virtualSingletonEffects(toolName string) []map[string]string {
	effects := []map[string]string{}
	contract := virtualCapabilityToolResultContract(toolName)
	if contract == nil {
		return effects
	}
	for _, effect := range contract.Effects {
		effects = append(effects, map[string]string{"objectType": effect.ObjectType, "effect": effect.Effect})
	}
	return effects
}
