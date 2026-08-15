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
	GuestVSockPorts         []uint32
	NetworkInterfaces       []GuestNetworkInterface
}

type GuestNetworkInterface struct {
	InterfaceID     string
	GuestMACAddress string
	HostDeviceName  string
}

type GuestLaunch struct {
	ExecutablePath   string
	Arguments        []string
	InstanceRootPath string

	// Firecracker and Cloud Hypervisor multiplex every guest port over one socket and
	// open a port with a CONNECT line. vfkit binds a socket per port and speaks the
	// stream straight away, so a monitor fills in one of these and never both.
	VSockUnixSocketPath       string
	VSockUnixSocketPathByPort map[uint32]string
}

const (
	FirecrackerMonitorName     = "firecracker"
	CloudHypervisorMonitorName = "cloudHypervisor"
	VfkitMonitorName           = "vfkit"
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
	case VfkitMonitorName:
		return VfkitMonitor{VfkitPath: configuration.VfkitPath}, nil
	}
	return nil, fmt.Errorf("unknown virtual machine monitor %q", monitorName)
}

type MonitorBinaryPaths struct {
	FirecrackerPath     string
	JailerPath          string
	CloudHypervisorPath string
	VfkitPath           string
}
