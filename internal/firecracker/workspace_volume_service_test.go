package firecracker

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRequireWorkspaceImageReturnsMetadataForExistingExt4(t *testing.T) {
	workspacePath := t.TempDir()
	workspaceImagePath := writeFakeExt4WorkspaceImage(t, workspacePath)
	workspaceVolumeService := WorkspaceVolumeService{}

	workspaceVolumeMetadata, errorValue := workspaceVolumeService.RequireWorkspaceImage(workspaceImagePath)
	if errorValue != nil {
		t.Fatalf("expected workspace image to be required: %v", errorValue)
	}
	if workspaceVolumeMetadata.GuestMountPath != "/workspace" {
		t.Fatalf("expected guest mount path to match, got %q", workspaceVolumeMetadata.GuestMountPath)
	}
	if workspaceVolumeMetadata.DataDirectoryPath != "/workspace/.blueclaw" {
		t.Fatalf("expected data directory path to match, got %q", workspaceVolumeMetadata.DataDirectoryPath)
	}
	if _, errorValue := workspaceVolumeService.RequireWorkspaceImage(filepath.Join(workspacePath, "missing.ext4")); !os.IsNotExist(errorValue) {
		t.Fatalf("missing workspace image must fail without creation, got %v", errorValue)
	}
}

func TestRequireWorkspaceImageRefusesEveryExistingMalformedFile(t *testing.T) {
	workspacePath := t.TempDir()

	for _, fixture := range []struct {
		name     string
		document []byte
	}{
		{name: "empty"},
		{name: "zero filled", document: make([]byte, 8192)},
		{name: "nonzero", document: []byte("existing workspace state")},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			workspaceImagePath := filepath.Join(workspacePath, strings.ReplaceAll(fixture.name, " ", "-")+".ext4")
			if errorValue := os.WriteFile(workspaceImagePath, fixture.document, 0o600); errorValue != nil {
				t.Fatal(errorValue)
			}
			workspaceVolumeService := WorkspaceVolumeService{}
			if _, errorValue := workspaceVolumeService.RequireWorkspaceImage(workspaceImagePath); errorValue == nil || !strings.Contains(errorValue.Error(), "refusing to format") {
				t.Fatalf("expected malformed existing image to fail closed, got %v", errorValue)
			}
			document, errorValue := os.ReadFile(workspaceImagePath)
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if !slices.Equal(document, fixture.document) {
				t.Fatal("existing malformed image was modified")
			}
		})
	}
}

func writeFakeExt4WorkspaceImage(t *testing.T, workspacePath string) string {
	t.Helper()
	workspaceImagePath := filepath.Join(workspacePath, "workspace.ext4")
	document := make([]byte, 4096)
	document[1080] = 0x53
	document[1081] = 0xef
	if errorValue := os.WriteFile(workspaceImagePath, document, 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	return workspaceImagePath
}

func writeFakeWorkspaceCommand(t *testing.T, workspacePath string, commandName string, commandBody string) string {
	t.Helper()
	commandPath := filepath.Join(workspacePath, commandName)
	commandDocument := "#!/usr/bin/env bash\nset -euo pipefail\n" + commandBody + "\n"
	if errorValue := os.WriteFile(commandPath, []byte(commandDocument), 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	return commandPath
}

func writeConfigFile(t *testing.T, directory string, name string, content string) {
	t.Helper()
	if errorValue := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func readConfigFile(t *testing.T, directory string, name string) string {
	t.Helper()
	content, errorValue := os.ReadFile(filepath.Join(directory, name))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return string(content)
}
