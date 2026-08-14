package firecracker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

type FirecrackerMonitor struct {
	FirecrackerPath string
	JailerPath      string
}

const (
	jailedKernelImagePath    = "/vmlinux.bin"
	jailedRootFilesystemPath = "/rootfs.ext4"
	jailedWorkspaceImagePath = "/workspace.ext4"
	jailedAPISocketPath      = "/firecracker-api.socket"
	jailedVSockSocketPath    = "/firecracker-vsock.socket"
	jailedConfigurationPath  = "/firecracker-config.json"
)

func (monitor FirecrackerMonitor) Name() string {
	return FirecrackerMonitorName
}

func (monitor FirecrackerMonitor) PrepareGuestLaunch(request GuestLaunchRequest) (GuestLaunch, error) {
	if monitor.FirecrackerPath == "" {
		return GuestLaunch{}, errors.New("firecrackerPath is required")
	}
	if monitor.JailerPath == "" {
		return GuestLaunch{}, errors.New("jailerPath is required")
	}

	jailerRootPath := buildJailerRootPath(request.RuntimeDirectoryPath, request.InstanceID)
	if errorValue := monitor.linkJailerRootAssets(jailerRootPath, request); errorValue != nil {
		return GuestLaunch{}, errorValue
	}

	configurationDocument := firecrackerConfigurationDocument(request)
	if errorValue := writeFirecrackerConfiguration(jailerRootPath, configurationDocument); errorValue != nil {
		return GuestLaunch{}, errorValue
	}

	return GuestLaunch{
		ExecutablePath:      monitor.JailerPath,
		InstanceRootPath:    jailerRootPath,
		VSockUnixSocketPath: filepath.Join(jailerRootPath, "firecracker-vsock.socket"),
		Arguments: []string{
			"--id", request.InstanceID,
			"--exec-file", monitor.FirecrackerPath,
			"--uid", strconv.Itoa(os.Getuid()),
			"--gid", strconv.Itoa(os.Getgid()),
			"--chroot-base-dir", request.RuntimeDirectoryPath,
			"--",
			"--api-sock", jailedAPISocketPath,
			"--config-file", jailedConfigurationPath,
		},
	}, nil
}

func firecrackerConfigurationDocument(request GuestLaunchRequest) ConfigurationDocument {
	networkConfigurations := make([]NetworkInterfaceConfiguration, 0, len(request.NetworkInterfaces))
	for _, networkInterface := range request.NetworkInterfaces {
		networkConfigurations = append(networkConfigurations, NetworkInterfaceConfiguration{
			InterfaceID:     networkInterface.InterfaceID,
			GuestMACAddress: networkInterface.GuestMACAddress,
			HostDeviceName:  networkInterface.HostDeviceName,
		})
	}

	return ConfigurationDocument{
		BootSource: BootSourceConfiguration{
			KernelImagePath: jailedKernelImagePath,
			BootArguments:   "console=ttyS0 reboot=k panic=1 pci=off rw",
		},
		DriveConfigurations: []DriveConfiguration{
			{
				DriveID:      "rootfs",
				PathOnHost:   jailedRootFilesystemPath,
				IsRootDevice: true,
				IsReadOnly:   false,
			},
			{
				DriveID:      "workspace",
				PathOnHost:   jailedWorkspaceImagePath,
				IsRootDevice: false,
				IsReadOnly:   false,
			},
		},
		MachineConfiguration: MachineConfiguration{
			VCPUCount: request.VCPUCount,
			MemoryMiB: request.MemoryMiB,
		},
		VSockConfiguration: VSockConfiguration{
			GuestCID:       request.VSockCID,
			UnixSocketPath: jailedVSockSocketPath,
		},
		NetworkConfigurations: networkConfigurations,
	}
}

func (monitor FirecrackerMonitor) linkJailerRootAssets(jailerRootPath string, request GuestLaunchRequest) error {
	if errorValue := os.MkdirAll(jailerRootPath, 0o700); errorValue != nil {
		return errorValue
	}

	assetLinks := map[string]string{
		request.KernelImagePath:         filepath.Join(jailerRootPath, "vmlinux.bin"),
		request.RootFilesystemImagePath: filepath.Join(jailerRootPath, "rootfs.ext4"),
		request.WorkspaceImagePath:      filepath.Join(jailerRootPath, "workspace.ext4"),
	}
	for sourcePath, destinationPath := range assetLinks {
		if errorValue := replaceHardLink(sourcePath, destinationPath); errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func writeFirecrackerConfiguration(jailerRootPath string, configurationDocument ConfigurationDocument) error {
	document, errorValue := json.MarshalIndent(configurationDocument, "", "  ")
	if errorValue != nil {
		return errorValue
	}
	return os.WriteFile(filepath.Join(jailerRootPath, "firecracker-config.json"), document, 0o600)
}

func replaceHardLink(sourcePath string, destinationPath string) error {
	if errorValue := os.Remove(destinationPath); errorValue != nil && !os.IsNotExist(errorValue) {
		return errorValue
	}
	if errorValue := os.Link(sourcePath, destinationPath); errorValue != nil {
		return fmt.Errorf("link %q into jail root at %q: %w", sourcePath, destinationPath, errorValue)
	}
	return nil
}

func buildJailerRootPath(runtimeDirectoryPath string, instanceID string) string {
	return filepath.Join(runtimeDirectoryPath, "firecracker", instanceID, "root")
}
