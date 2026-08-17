package firecracker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

type VfkitMonitor struct {
	VfkitPath string
}

func (monitor VfkitMonitor) ValidateBinaryPaths() error {
	if monitor.VfkitPath == "" {
		return errors.New("vfkitPath is required")
	}
	return nil
}

func (monitor VfkitMonitor) Name() string {
	return VfkitMonitorName
}

func (monitor VfkitMonitor) PrepareGuestLaunch(request GuestLaunchRequest) (GuestLaunch, error) {
	if monitor.VfkitPath == "" {
		return GuestLaunch{}, errors.New("vfkitPath is required")
	}
	if len(request.HostDialedGuestVSockPorts) == 0 {
		return GuestLaunch{}, errors.New("vfkit binds one socket per guest port, so the ports the host dials must be named")
	}

	instanceRootPath := buildVfkitInstanceRootPath(request.RuntimeDirectoryPath, request.InstanceID)
	if errorValue := os.MkdirAll(instanceRootPath, 0o700); errorValue != nil {
		return GuestLaunch{}, errorValue
	}

	initialRamdiskPath := filepath.Join(instanceRootPath, "empty.initrd")
	if errorValue := writeEmptyInitialRamdisk(initialRamdiskPath); errorValue != nil {
		return GuestLaunch{}, errorValue
	}

	restfulSocketPath := filepath.Join(instanceRootPath, "vfkit-api.socket")
	arguments := []string{
		"--restful-uri", "unix://" + restfulSocketPath,
		"--bootloader", fmt.Sprintf(
			`linux,kernel=%s,initrd=%s,cmdline="console=hvc0 reboot=k panic=1 root=/dev/vda rw"`,
			request.KernelImagePath,
			initialRamdiskPath,
		),
		"--cpus", strconv.Itoa(request.VCPUCount),
		"--memory", strconv.Itoa(request.MemoryMiB),
		"--device", "virtio-blk,path=" + request.RootFilesystemImagePath,
		"--device", "virtio-blk,path=" + request.WorkspaceImagePath,
		"--device", "virtio-serial,logFilePath=" + vfkitConsoleLogPath(request, instanceRootPath),
		"--device", "virtio-rng",
	}

	// A vsock device listens by default, meaning vfkit waits for the guest to dial out and
	// connects to a socket the host already holds. Reaching a listener inside the guest is
	// the other direction and needs connect, or the socket is never bound and the health
	// check waits on a path nothing creates.
	vsockUnixSocketPathByPort := map[uint32]string{}
	for _, guestPort := range request.HostDialedGuestVSockPorts {
		socketPath := filepath.Join(instanceRootPath, fmt.Sprintf("vfkit-vsock-%d.socket", guestPort))
		vsockUnixSocketPathByPort[guestPort] = socketPath
		arguments = append(arguments, "--device", fmt.Sprintf("virtio-vsock,port=%d,socketURL=%s,connect", guestPort, socketPath))
	}
	for _, hostPort := range request.GuestDialedHostVSockPorts {
		socketPath := fmt.Sprintf("%s_%d", filepath.Join(instanceRootPath, "vfkit-vsock.socket"), hostPort)
		arguments = append(arguments, "--device", fmt.Sprintf("virtio-vsock,port=%d,socketURL=%s", hostPort, socketPath))
	}

	for _, networkInterface := range request.NetworkInterfaces {
		arguments = append(arguments, "--device", "virtio-net,nat,mac="+networkInterface.GuestMACAddress)
	}

	// Virtualization.framework serves the share itself, so there is no daemon to run
	// beside the VM and no shared memory to ask for.
	if request.DeliveryDirectoryPath != "" {
		arguments = append(arguments, "--device", fmt.Sprintf("virtio-fs,sharedDir=%s,mountTag=%s", request.DeliveryDirectoryPath, DeliveryMountTag))
	}

	for _, socketPath := range append([]string{restfulSocketPath}, sortedSocketPaths(vsockUnixSocketPathByPort)...) {
		if errorValue := assertUnixSocketPathFits(socketPath); errorValue != nil {
			return GuestLaunch{}, errorValue
		}
	}

	return GuestLaunch{
		ExecutablePath:            monitor.VfkitPath,
		Arguments:                 arguments,
		InstanceRootPath:          instanceRootPath,
		VSockUnixSocketPathByPort: vsockUnixSocketPathByPort,
	}, nil
}

// vfkit stats the initrd path whether or not the guest needs one, and this guest mounts
// its root directly. An archive holding only the trailer unpacks to nothing, so the
// kernel falls through to root=/dev/vda as it does under the other monitors.
func writeEmptyInitialRamdisk(path string) error {
	trailer := append(
		[]byte("07070100000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000B00000000TRAILER!!!"),
		0, 0, 0, 0,
	)
	return os.WriteFile(path, trailer, 0o600)
}

// macOS caps sun_path at 104 bytes, and vfkit reports the overflow as an invalid URI
// rather than naming the path, so a long runtime directory reads as a malformed flag.
func assertUnixSocketPathFits(socketPath string) error {
	const macOSUnixSocketPathLimit = 104
	if len(socketPath) >= macOSUnixSocketPathLimit {
		return fmt.Errorf("unix socket path %q is %d bytes, over the %d macOS allows", socketPath, len(socketPath), macOSUnixSocketPathLimit)
	}
	return nil
}

func sortedSocketPaths(socketPathByPort map[uint32]string) []string {
	socketPaths := make([]string, 0, len(socketPathByPort))
	for _, socketPath := range socketPathByPort {
		socketPaths = append(socketPaths, socketPath)
	}
	sort.Strings(socketPaths)
	return socketPaths
}

func buildVfkitInstanceRootPath(runtimeDirectoryPath string, instanceID string) string {
	return filepath.Join(runtimeDirectoryPath, "vfkit", instanceID, "root")
}

func vfkitConsoleLogPath(request GuestLaunchRequest, instanceRootPath string) string {
	if request.LogDirectoryPath != "" {
		return filepath.Join(request.LogDirectoryPath, "console.log")
	}
	return filepath.Join(instanceRootPath, "console.log")
}
