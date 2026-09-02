package guest

import (
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
	monitor, errorValue := SelectVirtualMachineMonitor("", MonitorBinaryPaths{CloudHypervisorPath: "/c"})
	if errorValue != nil || monitor.Name() != CloudHypervisorMonitorName {
		t.Fatalf("expected cloud hypervisor when the runtime names no monitor, got %v %v", monitor, errorValue)
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
	request.HostDialedGuestVSockPorts = []uint32{8080, 8081}
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
	request.HostDialedGuestVSockPorts = []uint32{8080, 8081}
	monitor := VfkitMonitor{VfkitPath: "/usr/local/bin/vfkit"}

	guestLaunch, errorValue := monitor.PrepareGuestLaunch(request)
	if errorValue != nil {
		t.Fatalf("expected launch to prepare: %v", errorValue)
	}

	if guestLaunch.VSockUnixSocketPath != "" {
		t.Fatal("vfkit multiplexes nothing, so a single socket would be dialled with a CONNECT line it never answers")
	}
	for _, guestPort := range request.HostDialedGuestVSockPorts {
		socketPath := guestLaunch.VSockUnixSocketPathByPort[guestPort]
		if socketPath == "" {
			t.Fatalf("expected a socket bound for guest port %d", guestPort)
		}
		if !containsArgument(guestLaunch.Arguments, fmt.Sprintf("virtio-vsock,port=%d,socketURL=%s,connect", guestPort, socketPath)) {
			t.Fatalf("a port the host dials needs connect, or vfkit waits for the guest and binds nothing: %d", guestPort)
		}
	}

	if _, errorValue := monitor.PrepareGuestLaunch(GuestLaunchRequest{
		InstanceID:           "noports",
		RuntimeDirectoryPath: shortRuntimeDirectory(t),
	}); errorValue == nil {
		t.Fatal("expected a launch with no guest ports to be refused, since nothing could reach the guest")
	}

	guestDialed := guestLaunchRequestFixture(t)
	guestDialed.RuntimeDirectoryPath = shortRuntimeDirectory(t)
	guestDialed.HostDialedGuestVSockPorts = []uint32{8082}
	guestDialed.GuestDialedHostVSockPorts = []uint32{7000}
	launchWithBothDirections, errorValue := monitor.PrepareGuestLaunch(guestDialed)
	if errorValue != nil {
		t.Fatalf("expected launch to prepare: %v", errorValue)
	}
	for _, argument := range launchWithBothDirections.Arguments {
		if strings.HasPrefix(argument, "virtio-vsock,port=7000") && strings.HasSuffix(argument, ",connect") {
			t.Fatal("a port the guest dials must stay in listen mode, or the host never receives the connection")
		}
	}
}

func TestVfkitMonitorRefusesASocketPathMacOSCannotBind(t *testing.T) {
	request := guestLaunchRequestFixture(t)
	request.RuntimeDirectoryPath = filepath.Join(shortRuntimeDirectory(t), strings.Repeat("deep", 30))
	request.HostDialedGuestVSockPorts = []uint32{8080}
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

func TestCloudHypervisorMonitorServesTheDeliveryShare(t *testing.T) {
	request := guestLaunchRequestFixture(t)
	request.DeliveryDirectoryPath = "/var/lib/blueclaw/delivery"
	monitor := CloudHypervisorMonitor{
		CloudHypervisorPath: "/usr/local/bin/cloud-hypervisor",
		VirtiofsdPath:       "/usr/libexec/virtiofsd",
	}

	guestLaunch, errorValue := monitor.PrepareGuestLaunch(request)
	if errorValue != nil {
		t.Fatalf("expected launch to prepare: %v", errorValue)
	}

	if !containsArgument(guestLaunch.Arguments, "size=8192M,shared=on") {
		t.Fatalf("virtio-fs reaches guest memory directly, so the mapping must be shared, got %v", guestLaunch.Arguments)
	}
	if len(guestLaunch.Sidecars) != 1 || guestLaunch.Sidecars[0].ExecutablePath != "/usr/libexec/virtiofsd" {
		t.Fatalf("expected virtiofsd beside the guest, got %+v", guestLaunch.Sidecars)
	}
	if !containsArgument(guestLaunch.Arguments, "tag="+DeliveryMountTag+",socket="+filepath.Join(guestLaunch.InstanceRootPath, "virtiofsd-delivery.socket")) {
		t.Fatalf("expected the share to be offered on the socket virtiofsd binds, got %v", guestLaunch.Arguments)
	}
	if !containsArgument(guestLaunch.Sidecars[0].Arguments, "--shared-dir=/var/lib/blueclaw/delivery") {
		t.Fatalf("expected virtiofsd to serve the delivery directory, got %v", guestLaunch.Sidecars[0].Arguments)
	}
}

func TestCloudHypervisorMonitorAsksForNoShareWhenNoneIsGiven(t *testing.T) {
	request := guestLaunchRequestFixture(t)
	monitor := CloudHypervisorMonitor{CloudHypervisorPath: "/usr/local/bin/cloud-hypervisor"}

	guestLaunch, errorValue := monitor.PrepareGuestLaunch(request)
	if errorValue != nil {
		t.Fatalf("expected launch to prepare: %v", errorValue)
	}

	if len(guestLaunch.Sidecars) != 0 {
		t.Fatalf("a guest with no delivery directory needs no daemon beside it, got %+v", guestLaunch.Sidecars)
	}
	if containsArgument(guestLaunch.Arguments, "size=8192M,shared=on") {
		t.Fatal("a shared mapping is what virtio-fs needs, so it should not be asked for without one")
	}
}

func TestCloudHypervisorMonitorRefusesAShareItCannotServe(t *testing.T) {
	request := guestLaunchRequestFixture(t)
	request.DeliveryDirectoryPath = "/var/lib/blueclaw/delivery"
	monitor := CloudHypervisorMonitor{CloudHypervisorPath: "/usr/local/bin/cloud-hypervisor"}

	if _, errorValue := monitor.PrepareGuestLaunch(request); errorValue == nil {
		t.Fatal("expected a delivery directory with no virtiofsd to be refused rather than silently dropped")
	}
}

func TestVfkitMonitorServesTheDeliveryShareItself(t *testing.T) {
	request := guestLaunchRequestFixture(t)
	request.RuntimeDirectoryPath = shortRuntimeDirectory(t)
	request.HostDialedGuestVSockPorts = []uint32{8082}
	request.DeliveryDirectoryPath = "/var/lib/blueclaw/delivery"
	monitor := VfkitMonitor{VfkitPath: "/usr/local/bin/vfkit"}

	guestLaunch, errorValue := monitor.PrepareGuestLaunch(request)
	if errorValue != nil {
		t.Fatalf("expected launch to prepare: %v", errorValue)
	}

	if len(guestLaunch.Sidecars) != 0 {
		t.Fatalf("Virtualization.framework serves the share itself, so nothing runs beside the VM, got %+v", guestLaunch.Sidecars)
	}
	if !containsArgument(guestLaunch.Arguments, "virtio-fs,sharedDir=/var/lib/blueclaw/delivery,mountTag="+DeliveryMountTag) {
		t.Fatalf("expected the share to carry the tag the guest mounts, got %v", guestLaunch.Arguments)
	}
}

func TestVfkitBindsTheGuestDialedPortsWhereTheProxyListens(t *testing.T) {
	request := guestLaunchRequestFixture(t)
	request.RuntimeDirectoryPath = shortRuntimeDirectory(t)
	request.HostDialedGuestVSockPorts = []uint32{8082}
	request.GuestDialedHostVSockPorts = []uint32{7000}
	monitor := VfkitMonitor{VfkitPath: "/usr/local/bin/vfkit"}

	guestLaunch, errorValue := monitor.PrepareGuestLaunch(request)
	if errorValue != nil {
		t.Fatalf("expected launch to prepare: %v", errorValue)
	}

	expectedProxyPath := filepath.Join(guestLaunch.InstanceRootPath, "vfkit-vsock.socket") + "_7000"
	if !containsArgument(guestLaunch.Arguments, fmt.Sprintf("virtio-vsock,port=7000,socketURL=%s", expectedProxyPath)) {
		t.Fatalf("GuestListenerProxy listens on <socket>_<port>, so vfkit has to connect there, got %v", guestLaunch.Arguments)
	}
}

func TestAMonitorIsNeverAskedForBinariesItDoesNotRun(t *testing.T) {
	testCases := []struct {
		monitorName   string
		paths         MonitorBinaryPaths
		expectedError string
	}{
		{VfkitMonitorName, MonitorBinaryPaths{VfkitPath: "/usr/local/bin/vfkit"}, ""},
		{VfkitMonitorName, MonitorBinaryPaths{}, "vfkitPath is required"},
		{CloudHypervisorMonitorName, MonitorBinaryPaths{CloudHypervisorPath: "/usr/local/bin/cloud-hypervisor"}, ""},
		{CloudHypervisorMonitorName, MonitorBinaryPaths{}, "cloudHypervisorPath is required"},
	}

	for _, testCase := range testCases {
		monitor, errorValue := SelectVirtualMachineMonitor(testCase.monitorName, testCase.paths)
		if errorValue != nil {
			t.Fatalf("expected %s to be selectable: %v", testCase.monitorName, errorValue)
		}
		validationError := monitor.ValidateBinaryPaths()
		if testCase.expectedError == "" {
			if validationError != nil {
				t.Fatalf("a Mac has no cloud-hypervisor, so %s must not ask for one: %v", testCase.monitorName, validationError)
			}
			continue
		}
		if validationError == nil || validationError.Error() != testCase.expectedError {
			t.Fatalf("expected %q from %s, got %v", testCase.expectedError, testCase.monitorName, validationError)
		}
	}
}
