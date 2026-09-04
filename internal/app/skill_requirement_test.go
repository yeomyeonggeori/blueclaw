package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
)

const skillRequiringAToken = `---
name: internkim-api
description: Drives the company workspace over its public API.
compatibility: Requires python3, network access, and a personal access token in INTERNKIM_TOKEN.
metadata:
  kim.intern.requires-environment: "INTERNKIM_TOKEN"
---
Body.
`

func instructionsForSkillRequiringAToken(t *testing.T) agentInstructions {
	t.Helper()
	deliveredSkillsPath := t.TempDir()
	skillDirectoryPath := filepath.Join(deliveredSkillsPath, "internkim-api")
	if errorValue := os.MkdirAll(skillDirectoryPath, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(skillDirectoryPath, "SKILL.md"), []byte(skillRequiringAToken), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Setenv("BLUECLAW_BUNDLED_SKILLS_PATH", deliveredSkillsPath)
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Terminal.WorkspaceRootPath = t.TempDir()
	return loadAgentInstructions(runtimeConfiguration, agentruntime.NewCapabilityRegistry(capability.Client{}, nil))
}

func skillNames(instructions agentInstructions) []string {
	names := []string{}
	for _, skillInstruction := range instructions.Bundle.Skills {
		names = append(names, skillInstruction.Name)
	}
	return names
}

func TestASkillWhoseVariableThisHostLacksStaysOutOfThePrompt(t *testing.T) {
	t.Setenv("INTERNKIM_TOKEN", "")

	instructions := instructionsForSkillRequiringAToken(t)

	for _, skillName := range skillNames(instructions) {
		if skillName == "internkim-api" {
			t.Fatal("a skill this host cannot run was offered to the agent anyway")
		}
	}
	if len(instructions.UnavailableSkills) != 1 {
		t.Fatalf("expected the skill listed as unavailable, got %+v", instructions.UnavailableSkills)
	}
	unavailableSkill := instructions.UnavailableSkills[0]
	if unavailableSkill.Name != "internkim-api" {
		t.Fatalf("expected internkim-api, got %q", unavailableSkill.Name)
	}
	if len(unavailableSkill.MissingEnvironmentVariables) != 1 || unavailableSkill.MissingEnvironmentVariables[0] != "INTERNKIM_TOKEN" {
		t.Fatalf("the reason must name the variable, got %v", unavailableSkill.MissingEnvironmentVariables)
	}
	if unavailableSkill.Path == "" {
		t.Fatal("the inventory must say where the skill was read from")
	}
}

func TestTheSameSkillIsSelectableOnceItsVariableIsSet(t *testing.T) {
	t.Setenv("INTERNKIM_TOKEN", "ik_a-token")

	instructions := instructionsForSkillRequiringAToken(t)

	selected := false
	for _, skillName := range skillNames(instructions) {
		if skillName == "internkim-api" {
			selected = true
		}
	}
	if !selected {
		t.Fatal("a host that meets the requirement must get the skill")
	}
	if len(instructions.UnavailableSkills) != 0 {
		t.Fatalf("nothing is unavailable here, got %+v", instructions.UnavailableSkills)
	}
}

const skillRequiringACapabilityTool = `---
name: messages
description: Writes to a person on the messenger.
metadata:
  kim.intern.tool-references: "message_send"
---
Body.
`

func TestASkillNamingACapabilityToolIsSelectableFromTheLiveRegistry(t *testing.T) {
	deliveredSkillsPath := t.TempDir()
	skillDirectoryPath := filepath.Join(deliveredSkillsPath, "messages")
	if errorValue := os.MkdirAll(skillDirectoryPath, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(skillDirectoryPath, "SKILL.md"), []byte(skillRequiringACapabilityTool), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Setenv("BLUECLAW_BUNDLED_SKILLS_PATH", deliveredSkillsPath)
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"deviceCapabilities":[{"name":"message_send"}]}`))
	}))
	defer server.Close()
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Terminal.WorkspaceRootPath = t.TempDir()
	capabilityRegistry := agentruntime.NewCapabilityRegistry(
		capability.Client{Endpoint: server.URL, HTTPClient: server.Client()},
		nil,
	)

	instructions := loadAgentInstructions(runtimeConfiguration, capabilityRegistry)

	if len(instructions.UnavailableSkills) != 0 {
		t.Fatalf("expected the live registry to offer message_send, got %+v", instructions.UnavailableSkills)
	}
	selected := false
	for _, skillName := range skillNames(instructions) {
		if skillName == "messages" {
			selected = true
		}
	}
	if !selected {
		t.Fatalf("expected the skill in the prompt, got %v", skillNames(instructions))
	}
}
