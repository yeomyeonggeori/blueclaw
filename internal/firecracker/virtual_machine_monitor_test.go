package firecracker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func guestLaunchRequestFixture(t *testing.T) GuestLaunchRequest {
	t.Helper()
	temporaryDirectory := t.TempDir()
	kernelImagePath := filepath.Join(temporaryDirectory, "vmlinux.bin")
	rootFilesystemImagePath := filepath.Join(temporaryDirectory, "rootfs.ext4")
	workspaceImagePath := filepath.Join(temporaryDirectory, "workspace.ext4")
	for _, path := range []string{kernelImagePath, rootFilesystemImagePath, workspaceImagePath} {
		if errorValue := os.WriteFile(path, []byte("fixture"), 0o600); errorValue != nil {
			t.Fatalf("expected fixture at %q: %v", path, errorValue)
		}
	}
	return GuestLaunchRequest{
		InstanceID:              "testinstance",
		KernelImagePath:         kernelImagePath,
		RootFilesystemImagePath: rootFilesystemImagePath,
		WorkspaceImagePath:      workspaceImagePath,
		RuntimeDirectoryPath:    filepath.Join(temporaryDirectory, "runtime"),
		VCPUCount:               4,
		MemoryMiB:               8192,
		VSockCID:                52,
		NetworkInterfaces: []GuestNetworkInterface{{
			InterfaceID:     "eth0",
			GuestMACAddress: "AA:FC:00:00:00:01",
			HostDeviceName:  "bctap0",
		}},
	}
}

func TestFirecrackerMonitorWritesJailedConfiguration(t *testing.T) {
	request := guestLaunchRequestFixture(t)
	monitor := FirecrackerMonitor{FirecrackerPath: "/usr/bin/firecracker", JailerPath: "/usr/bin/jailer"}

	guestLaunch, errorValue := monitor.PrepareGuestLaunch(request)
	if errorValue != nil {
		t.Fatalf("expected launch to prepare: %v", errorValue)
	}

	if guestLaunch.ExecutablePath != "/usr/bin/jailer" {
		t.Fatalf("expected the jailer to be executed, got %q", guestLaunch.ExecutablePath)
	}
	if !strings.Contains(strings.Join(guestLaunch.Arguments, " "), "--exec-file /usr/bin/firecracker") {
		t.Fatalf("expected the jailer to exec firecracker, got %v", guestLaunch.Arguments)
	}

	document, errorValue := os.ReadFile(filepath.Join(guestLaunch.InstanceRootPath, "firecracker-config.json"))
	if errorValue != nil {
		t.Fatalf("expected a written configuration document: %v", errorValue)
	}
	var configurationDocument ConfigurationDocument
	if errorValue := json.Unmarshal(document, &configurationDocument); errorValue != nil {
		t.Fatalf("expected the configuration document to parse: %v", errorValue)
	}

	if configurationDocument.MachineConfiguration.VCPUCount != 4 {
		t.Fatalf("expected vcpu count to match, got %d", configurationDocument.MachineConfiguration.VCPUCount)
	}
	if configurationDocument.VSockConfiguration.GuestCID != 52 {
		t.Fatalf("expected guest cid to match, got %d", configurationDocument.VSockConfiguration.GuestCID)
	}
	if configurationDocument.BootSource.KernelImagePath != "/vmlinux.bin" {
		t.Fatalf("expected jailed kernel path, got %q", configurationDocument.BootSource.KernelImagePath)
	}
	if !strings.Contains(configurationDocument.BootSource.BootArguments, " rw") {
		t.Fatalf("expected guest rootfs to boot writable, got %q", configurationDocument.BootSource.BootArguments)
	}
	if len(configurationDocument.DriveConfigurations) != 2 {
		t.Fatalf("expected rootfs and workspace drives, got %d", len(configurationDocument.DriveConfigurations))
	}
	if configurationDocument.DriveConfigurations[0].PathOnHost != "/rootfs.ext4" || configurationDocument.DriveConfigurations[0].IsReadOnly {
		t.Fatalf("expected a writable jailed rootfs, got %+v", configurationDocument.DriveConfigurations[0])
	}
	if configurationDocument.DriveConfigurations[1].PathOnHost != "/workspace.ext4" {
		t.Fatalf("expected jailed workspace path, got %q", configurationDocument.DriveConfigurations[1].PathOnHost)
	}
	if configurationDocument.VSockConfiguration.UnixSocketPath != "/firecracker-vsock.socket" {
		t.Fatalf("expected jailed vsock path, got %q", configurationDocument.VSockConfiguration.UnixSocketPath)
	}
	if len(configurationDocument.NetworkConfigurations) != 1 {
		t.Fatalf("expected one network interface, got %+v", configurationDocument.NetworkConfigurations)
	}
	if configurationDocument.NetworkConfigurations[0].HostDeviceName != "bctap0" {
		t.Fatalf("expected the tap device to reach the guest, got %+v", configurationDocument.NetworkConfigurations[0])
	}
}

func TestCloudHypervisorMonitorStatesTheDiskImageType(t *testing.T) {
	request := guestLaunchRequestFixture(t)
	monitor := CloudHypervisorMonitor{CloudHypervisorPath: "/usr/local/bin/cloud-hypervisor"}

	guestLaunch, errorValue := monitor.PrepareGuestLaunch(request)
	if errorValue != nil {
		t.Fatalf("expected launch to prepare: %v", errorValue)
	}

	for _, imagePath := range []string{request.RootFilesystemImagePath, request.WorkspaceImagePath} {
		expectedArgument := "path=" + imagePath + ",image_type=raw"
		if !containsArgument(guestLaunch.Arguments, expectedArgument) {
			t.Fatalf("expected %q so the guest can write sector 0, got %v", expectedArgument, guestLaunch.Arguments)
		}
	}
}

func TestCloudHypervisorMonitorBootsThroughVirtioPCI(t *testing.T) {
	request := guestLaunchRequestFixture(t)
	monitor := CloudHypervisorMonitor{CloudHypervisorPath: "/usr/local/bin/cloud-hypervisor"}

	guestLaunch, errorValue := monitor.PrepareGuestLaunch(request)
	if errorValue != nil {
		t.Fatalf("expected launch to prepare: %v", errorValue)
	}

	commandLine := strings.Join(guestLaunch.Arguments, " ")
	if !strings.Contains(commandLine, "console=ttyAMA0") {
		t.Fatalf("expected the PL011 console Cloud Hypervisor offers, got %q", commandLine)
	}
	if !strings.Contains(commandLine, "root=/dev/vda") {
		t.Fatalf("expected the root device to be named, got %q", commandLine)
	}
	if strings.Contains(commandLine, "pci=off") {
		t.Fatalf("expected PCI to stay on, since Cloud Hypervisor reaches virtio through it, got %q", commandLine)
	}
	if !containsArgument(guestLaunch.Arguments, "cid=52,socket="+guestLaunch.VSockUnixSocketPath) {
		t.Fatalf("expected the vsock socket to match the reported path, got %v", guestLaunch.Arguments)
	}
	if !containsArgument(guestLaunch.Arguments, "tap=bctap0,mac=AA:FC:00:00:00:01") {
		t.Fatalf("expected the tap device to reach the guest, got %v", guestLaunch.Arguments)
	}
}

func TestCloudHypervisorMonitorKeepsSeccompAndLeavesLandlockAlone(t *testing.T) {
	request := guestLaunchRequestFixture(t)
	monitor := CloudHypervisorMonitor{CloudHypervisorPath: "/usr/local/bin/cloud-hypervisor"}

	guestLaunch, errorValue := monitor.PrepareGuestLaunch(request)
	if errorValue != nil {
		t.Fatalf("expected launch to prepare: %v", errorValue)
	}

	if !containsArgument(guestLaunch.Arguments, "--seccomp") {
		t.Fatalf("expected seccomp to stay on, got %v", guestLaunch.Arguments)
	}
	if containsArgument(guestLaunch.Arguments, "--landlock") {
		t.Fatal("Cloud Hypervisor refuses to start when the host kernel lacks the Landlock access rights it asks for, so filesystem confinement belongs to the service unit")
	}
}

func TestSelectVirtualMachineMonitorRejectsAnUnknownName(t *testing.T) {
	if _, errorValue := SelectVirtualMachineMonitor("qemu", MonitorBinaryPaths{}); errorValue == nil {
		t.Fatal("expected an unknown monitor to be refused")
	}
	monitor, errorValue := SelectVirtualMachineMonitor("", MonitorBinaryPaths{FirecrackerPath: "/f", JailerPath: "/j"})
	if errorValue != nil || monitor.Name() != FirecrackerMonitorName {
		t.Fatalf("expected firecracker when the runtime names no monitor, got %v %v", monitor, errorValue)
	}
	monitor, errorValue = SelectVirtualMachineMonitor(CloudHypervisorMonitorName, MonitorBinaryPaths{CloudHypervisorPath: "/c"})
	if errorValue != nil || monitor.Name() != CloudHypervisorMonitorName {
		t.Fatalf("expected cloud hypervisor to be selectable, got %v %v", monitor, errorValue)
	}
}

func containsArgument(arguments []string, expected string) bool {
	for _, argument := range arguments {
		if argument == expected {
			return true
		}
	}
	return false
}

func TestVfkitMonitorBootsWithoutAnInitialRamdiskOfItsOwn(t *testing.T) {
	request := guestLaunchRequestFixture(t)
	request.RuntimeDirectoryPath = shortRuntimeDirectory(t)
	request.GuestVSockPorts = []uint32{8080, 8081}
	monitor := VfkitMonitor{VfkitPath: "/usr/local/bin/vfkit"}

	guestLaunch, errorValue := monitor.PrepareGuestLaunch(request)
	if errorValue != nil {
		t.Fatalf("expected launch to prepare: %v", errorValue)
	}

	commandLine := strings.Join(guestLaunch.Arguments, " ")
	if !strings.Contains(commandLine, "console=hvc0") {
		t.Fatalf("expected the virtio console Virtualization.framework offers, got %q", commandLine)
	}
	if !strings.Contains(commandLine, "root=/dev/vda") {
		t.Fatalf("expected the root device to be named, got %q", commandLine)
	}

	initialRamdiskPath := filepath.Join(guestLaunch.InstanceRootPath, "empty.initrd")
	document, errorValue := os.ReadFile(initialRamdiskPath)
	if errorValue != nil {
		t.Fatalf("vfkit stats the initrd whether or not the guest needs one: %v", errorValue)
	}
	if !strings.Contains(string(document), "TRAILER!!!") {
		t.Fatal("expected an archive that unpacks to nothing so the kernel falls through to the root device")
	}
}

func TestVfkitMonitorBindsASocketPerGuestPort(t *testing.T) {
	request := guestLaunchRequestFixture(t)
	request.RuntimeDirectoryPath = shortRuntimeDirectory(t)
	request.GuestVSockPorts = []uint32{8080, 8081}
	monitor := VfkitMonitor{VfkitPath: "/usr/local/bin/vfkit"}

	guestLaunch, errorValue := monitor.PrepareGuestLaunch(request)
	if errorValue != nil {
		t.Fatalf("expected launch to prepare: %v", errorValue)
	}

	if guestLaunch.VSockUnixSocketPath != "" {
		t.Fatal("vfkit multiplexes nothing, so a single socket would be dialled with a CONNECT line it never answers")
	}
	for _, guestPort := range request.GuestVSockPorts {
		socketPath := guestLaunch.VSockUnixSocketPathByPort[guestPort]
		if socketPath == "" {
			t.Fatalf("expected a socket bound for guest port %d", guestPort)
		}
		if !containsArgument(guestLaunch.Arguments, fmt.Sprintf("virtio-vsock,port=%d,socketURL=%s", guestPort, socketPath)) {
			t.Fatalf("expected the device for guest port %d to match the reported socket", guestPort)
		}
	}

	if _, errorValue := monitor.PrepareGuestLaunch(GuestLaunchRequest{
		InstanceID:           "noports",
		RuntimeDirectoryPath: shortRuntimeDirectory(t),
	}); errorValue == nil {
		t.Fatal("expected a launch with no guest ports to be refused, since nothing could reach the guest")
	}
}

func TestVfkitMonitorRefusesASocketPathMacOSCannotBind(t *testing.T) {
	request := guestLaunchRequestFixture(t)
	request.RuntimeDirectoryPath = filepath.Join(shortRuntimeDirectory(t), strings.Repeat("deep", 30))
	request.GuestVSockPorts = []uint32{8080}
	monitor := VfkitMonitor{VfkitPath: "/usr/local/bin/vfkit"}

	if _, errorValue := monitor.PrepareGuestLaunch(request); errorValue == nil {
		t.Fatal("expected an over-long socket path to be named here rather than reported by vfkit as an invalid URI")
	}
}

// macOS temporary directories are long enough on their own to trip the socket path
// limit, which is the point of the guard but not the subject of these tests.
func shortRuntimeDirectory(t *testing.T) string {
	t.Helper()
	directory, errorValue := os.MkdirTemp("/tmp", "vk")
	if errorValue != nil {
		t.Fatalf("expected a short runtime directory: %v", errorValue)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}
