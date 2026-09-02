package lab

import (
	"encoding/json"
	"os"
)

type Configuration struct {
	Host           HostConfiguration           `json:"host"`
	VirtualMachine VirtualMachineConfiguration `json:"vm"`
}

type HostConfiguration struct {
	Mode      string                 `json:"mode"`
	Companion CompanionConfiguration `json:"companion"`
}

type CompanionConfiguration struct {
	ListenAddress   string `json:"listenAddress"`
	CallbackBaseURL string `json:"callbackBaseURL"`
}

type VirtualMachineConfiguration struct {
	Tart                TartConfiguration       `json:"tart"`
	Mattermost          MattermostConfiguration `json:"mattermost"`
	SharedWorkspacePath string                  `json:"sharedWorkspacePath"`
	MountDirectoryPath  string                  `json:"mountDirectoryPath"`
	SSHUsername         string                  `json:"sshUsername"`
	SSHPassword         string                  `json:"sshPassword"`
}

type TartConfiguration struct {
	BinaryPath    string `json:"binaryPath"`
	Name          string `json:"name"`
	Image         string `json:"image"`
	NestedEnabled bool   `json:"nestedEnabled"`
	CPUCount      int    `json:"cpuCount"`
	MemoryMiB     int    `json:"memoryMiB"`
	DiskGiB       int    `json:"diskGiB"`
}

type MattermostConfiguration struct {
	ListenAddress string `json:"listenAddress"`
}

func LoadConfiguration(path string) (Configuration, error) {
	document, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return Configuration{}, errorValue
	}

	var configuration Configuration
	errorValue = json.Unmarshal(document, &configuration)
	if errorValue != nil {
		return Configuration{}, errorValue
	}

	configuration = applyDefaultConfiguration(configuration)
	return configuration, nil
}

func applyDefaultConfiguration(configuration Configuration) Configuration {
	if configuration.Host.Mode == "" {
		configuration.Host.Mode = "single-mac"
	}
	if configuration.VirtualMachine.Tart.BinaryPath == "" {
		configuration.VirtualMachine.Tart.BinaryPath = "tart"
	}
	if configuration.VirtualMachine.MountDirectoryPath == "" {
		configuration.VirtualMachine.MountDirectoryPath = "/mnt/shared"
	}
	if configuration.VirtualMachine.SSHUsername == "" {
		configuration.VirtualMachine.SSHUsername = "admin"
	}
	if configuration.VirtualMachine.SSHPassword == "" {
		configuration.VirtualMachine.SSHPassword = "admin"
	}

	return configuration
}
