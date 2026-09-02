package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/guest"
)

const waitForFileDeadline = 30 * time.Second

type fakeGuestHealthClient struct{}

func (fakeGuestHealthClient) CheckHealth(healthContext context.Context, bootSpecification guest.BootSpecification) error {
	_ = healthContext
	if bootSpecification.VSockUnixSocketPath == "" {
		return os.ErrInvalid
	}
	if bootSpecification.HealthPortOrService != "8080" {
		return os.ErrInvalid
	}
	return nil
}

func TestSupervisorBootGuestWithFakeCloudHypervisor(t *testing.T) {
	workspacePath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspacePath, "artifacts")
	cloudHypervisorPath := filepath.Join(workspacePath, "fake-cloud-hypervisor.sh")
	kernelImagePath := filepath.Join(workspacePath, "kernel")
	rootfsImagePath := filepath.Join(workspacePath, "rootfs.ext4")
	monitorOutputPath := filepath.Join(workspacePath, "monitor-output.txt")

	if errorValue := os.WriteFile(kernelImagePath, []byte("kernel"), 0o600); errorValue != nil {
		t.Fatalf("expected fake kernel to be written: %v", errorValue)
	}
	if errorValue := os.WriteFile(rootfsImagePath, []byte("rootfs"), 0o600); errorValue != nil {
		t.Fatalf("expected fake rootfs to be written: %v", errorValue)
	}
	workspaceImagePath := filepath.Join(workspacePath, "workspace.ext4")
	workspaceDocument := make([]byte, 4096)
	workspaceDocument[1080] = 0x53
	workspaceDocument[1081] = 0xef
	if errorValue := os.WriteFile(workspaceImagePath, workspaceDocument, 0o600); errorValue != nil {
		t.Fatalf("expected fake workspace to be written: %v", errorValue)
	}

	monitorScript := "#!/bin/sh\nprintf '%s\n' \"$@\" > \"" + monitorOutputPath + "\"\nsleep 5\n"
	if errorValue := os.WriteFile(cloudHypervisorPath, []byte(monitorScript), 0o700); errorValue != nil {
		t.Fatalf("expected fake cloud hypervisor to be written: %v", errorValue)
	}

	supervisorService := guest.NewSupervisorService(
		config.GuestConfiguration{
			CloudHypervisorPath:    cloudHypervisorPath,
			KernelImagePath:        kernelImagePath,
			RootfsImagePath:        rootfsImagePath,
			WorkspaceImagePath:     workspaceImagePath,
			VCPUCount:              4,
			MemoryMiB:              8192,
			VSockCID:               52,
			HealthPortOrService:    "8080",
			GuestHTTPPortOrService: "8081",
			HostHTTPListenAddress:  "127.0.0.1:8080",
			LogDirectoryPath:       artifactDirectoryPath,
		},
		guest.WorkspaceVolumeService{},
		fakeGuestHealthClient{},
	)

	guestInstance, errorValue := supervisorService.BootGuest(context.Background())
	if errorValue != nil {
		t.Fatalf("expected guest boot to succeed: %v", errorValue)
	}
	defer supervisorService.StopGuest(guestInstance)

	if errorValue := supervisorService.WaitForGuestHealth(context.Background(), guestInstance); errorValue != nil {
		t.Fatalf("expected guest health to succeed: %v", errorValue)
	}

	monitorOutputDocument, errorValue := waitForFile(monitorOutputPath)
	if errorValue != nil {
		t.Fatalf("expected fake cloud hypervisor output to be readable: %v", errorValue)
	}
	if !strings.Contains(string(monitorOutputDocument), "--kernel") {
		t.Fatalf("expected the monitor to receive the kernel argument, got %q", monitorOutputDocument)
	}
}

func waitForFile(documentPath string) ([]byte, error) {
	deadline := time.Now().Add(waitForFileDeadline)
	for {
		document, errorValue := os.ReadFile(documentPath)
		if errorValue == nil && len(document) > 0 {
			return document, nil
		}
		if time.Now().After(deadline) {
			if errorValue != nil {
				return nil, errorValue
			}
			return nil, fmt.Errorf("%s stayed empty for %s", documentPath, waitForFileDeadline)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
