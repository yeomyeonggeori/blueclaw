package lab

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigurationAppliesDefaults(t *testing.T) {
	workspacePath := t.TempDir()
	configurationPath := filepath.Join(workspacePath, "lab.json")
	errorValue := os.WriteFile(configurationPath, []byte(`{
  "host": {
    "companion": {
      "listenAddress": "127.0.0.1:7780"
    }
  },
  "vm": {
    "tart": {
      "name": "blueclaw-dev",
      "image": "ghcr.io/cirruslabs/ubuntu:latest",
      "nestedEnabled": true
    }
  }
}`), 0o600)
	if errorValue != nil {
		t.Fatalf("expected configuration to be written: %v", errorValue)
	}

	configuration, errorValue := LoadConfiguration(configurationPath)
	if errorValue != nil {
		t.Fatalf("expected configuration to load: %v", errorValue)
	}

	if configuration.Host.Mode != "single-mac" {
		t.Fatalf("expected default host mode, got %q", configuration.Host.Mode)
	}
	if configuration.VirtualMachine.Tart.BinaryPath != "tart" {
		t.Fatalf("expected default tart binary, got %q", configuration.VirtualMachine.Tart.BinaryPath)
	}
	if configuration.VirtualMachine.MountDirectoryPath != "/mnt/shared" {
		t.Fatalf("expected default mount directory, got %q", configuration.VirtualMachine.MountDirectoryPath)
	}
	if configuration.VirtualMachine.SSHUsername != "admin" {
		t.Fatalf("expected default ssh username, got %q", configuration.VirtualMachine.SSHUsername)
	}
}
