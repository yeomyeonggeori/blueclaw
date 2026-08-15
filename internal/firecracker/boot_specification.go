package firecracker

type BootSpecification struct {
	InstanceID                string
	MonitorName               string
	LogDirectoryPath          string
	InstanceRootPath          string
	LaunchExecutablePath      string
	LaunchArguments           []string
	VSockUnixSocketPath       string
	VSockUnixSocketPathByPort map[uint32]string
	OutboundNetwork           OutboundNetwork
	HealthPortOrService       string
	VSockCID                  uint32
	WorkspaceVolumeMetadata   WorkspaceVolumeMetadata
}

type ConfigurationDocument struct {
	BootSource            BootSourceConfiguration         `json:"boot-source"`
	DriveConfigurations   []DriveConfiguration            `json:"drives"`
	MachineConfiguration  MachineConfiguration            `json:"machine-config"`
	VSockConfiguration    VSockConfiguration              `json:"vsock"`
	NetworkConfigurations []NetworkInterfaceConfiguration `json:"network-interfaces,omitempty"`
}

type BootSourceConfiguration struct {
	KernelImagePath string `json:"kernel_image_path"`
	BootArguments   string `json:"boot_args"`
}

type DriveConfiguration struct {
	DriveID      string `json:"drive_id"`
	PathOnHost   string `json:"path_on_host"`
	IsRootDevice bool   `json:"is_root_device"`
	IsReadOnly   bool   `json:"is_read_only"`
}

type MachineConfiguration struct {
	VCPUCount int `json:"vcpu_count"`
	MemoryMiB int `json:"mem_size_mib"`
}

type VSockConfiguration struct {
	GuestCID       uint32 `json:"guest_cid"`
	UnixSocketPath string `json:"uds_path"`
}

type NetworkInterfaceConfiguration struct {
	InterfaceID     string `json:"iface_id"`
	GuestMACAddress string `json:"guest_mac"`
	HostDeviceName  string `json:"host_dev_name"`
}

type OutboundNetwork struct {
	Enabled         bool
	HostDeviceName  string
	NetworkCIDR     string
	HostAddressCIDR string
}

type GuestInstance struct {
	InstanceID        string
	BootSpecification BootSpecification
}
