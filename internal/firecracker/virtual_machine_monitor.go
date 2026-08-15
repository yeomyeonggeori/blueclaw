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
	// vfkit binds each direction differently, so they are named apart: the host opens a
	// connection to a guest listener on the first, and listens for the guest on the second.
	HostDialedGuestVSockPorts []uint32
	GuestDialedHostVSockPorts []uint32
	NetworkInterfaces         []GuestNetworkInterface
	DeliveryDirectoryPath     string
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

	// A monitor that reaches the delivery directory through a separate daemon names it
	// here. The supervisor starts it before the guest and stops it after, because the
	// daemon serves a single client and exits with it.
	Sidecars []SidecarCommand
}

type SidecarCommand struct {
	Name           string
	ExecutablePath string
	Arguments      []string
}

const (
	FirecrackerMonitorName     = "firecracker"
	CloudHypervisorMonitorName = "cloudHypervisor"
	VfkitMonitorName           = "vfkit"

	// The guest mounts the delivery share by this tag, so it is one name in two repositories.
	DeliveryMountTag = "delivery"
)

func SelectVirtualMachineMonitor(monitorName string, configuration MonitorBinaryPaths) (VirtualMachineMonitor, error) {
	switch monitorName {
	case "", FirecrackerMonitorName:
		return FirecrackerMonitor{
			FirecrackerPath: configuration.FirecrackerPath,
			JailerPath:      configuration.JailerPath,
		}, nil
	case CloudHypervisorMonitorName:
		return CloudHypervisorMonitor{
			CloudHypervisorPath: configuration.CloudHypervisorPath,
			VirtiofsdPath:       configuration.VirtiofsdPath,
		}, nil
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
	VirtiofsdPath       string
}
