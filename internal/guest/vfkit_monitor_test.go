package guest

import (
	"slices"
	"testing"
)

func TestTheGuestConsoleLandsWhereTheSupervisorReadsLogs(t *testing.T) {
	monitor := VfkitMonitor{VfkitPath: "/usr/local/bin/vfkit"}
	launch, errorValue := monitor.PrepareGuestLaunch(GuestLaunchRequest{
		InstanceID:                "instance",
		KernelImagePath:           "/k/vmlinux.bin",
		RootFilesystemImagePath:   "/k/rootfs.ext4",
		WorkspaceImagePath:        "/k/workspace.ext4",
		RuntimeDirectoryPath:      "/tmp/vfkit-console-test",
		LogDirectoryPath:          "/var/log/blueclaw-supervisor/instance",
		VCPUCount:                 2,
		MemoryMiB:                 2048,
		VSockCID:                  3,
		HostDialedGuestVSockPorts: []uint32{8082},
	})
	if errorValue != nil {
		t.Fatalf("expected a launch: %v", errorValue)
	}

	if !slices.Contains(launch.Arguments, "virtio-serial,logFilePath=/var/log/blueclaw-supervisor/instance/console.log") {
		t.Fatalf("vfkit writes the console to a file, and a file nobody reads is a guest that fails silently: %v", launch.Arguments)
	}
}
