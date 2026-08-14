package firecracker

import "fmt"

type VirtualMachineMonitor interface {
	Name() string
	PrepareGuestLaunch(GuestLaunchRequest) (GuestLaunch, error)
}

type GuestLaunchRequest struct {
	InstanceID              string
	KernelImagePath         string
	RootFilesystemImagePath string
	WorkspaceImagePath      string
	RuntimeDirectoryPath    string
	VCPUCount               int
	MemoryMiB               int
	VSockCID                uint32
	NetworkInterfaces       []GuestNetworkInterface
}

type GuestNetworkInterface struct {
	InterfaceID     string
	GuestMACAddress string
	HostDeviceName  string
}

type GuestLaunch struct {
	ExecutablePath      string
	Arguments           []string
	InstanceRootPath    string
	VSockUnixSocketPath string
}

const (
	FirecrackerMonitorName     = "firecracker"
	CloudHypervisorMonitorName = "cloudHypervisor"
)

func SelectVirtualMachineMonitor(monitorName string, configuration MonitorBinaryPaths) (VirtualMachineMonitor, error) {
	switch monitorName {
	case "", FirecrackerMonitorName:
		return FirecrackerMonitor{
			FirecrackerPath: configuration.FirecrackerPath,
			JailerPath:      configuration.JailerPath,
		}, nil
	case CloudHypervisorMonitorName:
		return CloudHypervisorMonitor{CloudHypervisorPath: configuration.CloudHypervisorPath}, nil
	}
	return nil, fmt.Errorf("unknown virtual machine monitor %q", monitorName)
}

type MonitorBinaryPaths struct {
	FirecrackerPath     string
	JailerPath          string
	CloudHypervisorPath string
}
