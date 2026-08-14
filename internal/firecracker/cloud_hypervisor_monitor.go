package firecracker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

type CloudHypervisorMonitor struct {
	CloudHypervisorPath string
}

func (monitor CloudHypervisorMonitor) Name() string {
	return CloudHypervisorMonitorName
}

func (monitor CloudHypervisorMonitor) PrepareGuestLaunch(request GuestLaunchRequest) (GuestLaunch, error) {
	if monitor.CloudHypervisorPath == "" {
		return GuestLaunch{}, errors.New("cloudHypervisorPath is required")
	}

	instanceRootPath := buildCloudHypervisorInstanceRootPath(request.RuntimeDirectoryPath, request.InstanceID)
	if errorValue := os.MkdirAll(instanceRootPath, 0o700); errorValue != nil {
		return GuestLaunch{}, errorValue
	}

	vsockUnixSocketPath := filepath.Join(instanceRootPath, "cloud-hypervisor-vsock.socket")
	arguments := []string{
		"--api-socket", filepath.Join(instanceRootPath, "cloud-hypervisor-api.socket"),
		"--kernel", request.KernelImagePath,
		"--cmdline", "console=ttyAMA0 reboot=k panic=1 root=/dev/vda rw",
		"--disk",
		cloudHypervisorDiskArgument(request.RootFilesystemImagePath),
		cloudHypervisorDiskArgument(request.WorkspaceImagePath),
		"--cpus", "boot=" + strconv.Itoa(request.VCPUCount),
		"--memory", "size=" + strconv.Itoa(request.MemoryMiB) + "M",
		"--vsock", "cid=" + strconv.FormatUint(uint64(request.VSockCID), 10) + ",socket=" + vsockUnixSocketPath,
		"--serial", "tty",
		"--console", "off",
		"--seccomp", "true",
	}
	arguments = append(arguments, cloudHypervisorNetworkArguments(request.NetworkInterfaces)...)

	return GuestLaunch{
		ExecutablePath:      monitor.CloudHypervisorPath,
		Arguments:           arguments,
		InstanceRootPath:    instanceRootPath,
		VSockUnixSocketPath: vsockUnixSocketPath,
	}, nil
}

// Cloud Hypervisor guards its image-format autodetection by refusing writes to sector 0
// unless the type is stated, which leaves ext4 unable to write its superblock.
func cloudHypervisorDiskArgument(imagePath string) string {
	return "path=" + imagePath + ",image_type=raw"
}

func cloudHypervisorNetworkArguments(networkInterfaces []GuestNetworkInterface) []string {
	if len(networkInterfaces) == 0 {
		return nil
	}
	arguments := []string{"--net"}
	for _, networkInterface := range networkInterfaces {
		arguments = append(arguments, fmt.Sprintf("tap=%s,mac=%s", networkInterface.HostDeviceName, networkInterface.GuestMACAddress))
	}
	return arguments
}

func buildCloudHypervisorInstanceRootPath(runtimeDirectoryPath string, instanceID string) string {
	return filepath.Join(runtimeDirectoryPath, "cloud-hypervisor", instanceID, "root")
}
