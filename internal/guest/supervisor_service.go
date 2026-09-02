package guest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
)

type SupervisorService struct {
	GuestConfiguration     config.GuestConfiguration
	WorkspaceVolumeService WorkspaceVolumeService
	GuestHealthClient      GuestHealthClient
	OutboundNetworkService OutboundNetworkService
	VirtualMachineMonitor  VirtualMachineMonitor
	HealthCheckInterval    time.Duration

	mutex                sync.RWMutex
	commandByInstanceID  map[string]*exec.Cmd
	exitByInstanceID     map[string]*guestExitState
	sidecarsByInstanceID map[string][]*exec.Cmd
}

type guestExitState struct {
	exited    chan struct{}
	exitError error
}

type OutboundNetworkService interface {
	PrepareOutboundNetwork(OutboundNetwork) error
	CleanupOutboundNetwork(OutboundNetwork) error
}

func NewSupervisorService(
	guestConfiguration config.GuestConfiguration,
	workspaceVolumeService WorkspaceVolumeService,
	guestHealthClient GuestHealthClient,
) *SupervisorService {
	return &SupervisorService{
		GuestConfiguration:     guestConfiguration,
		WorkspaceVolumeService: workspaceVolumeService,
		GuestHealthClient:      guestHealthClient,
		OutboundNetworkService: HostOutboundNetworkService{},
		commandByInstanceID:    map[string]*exec.Cmd{},
		exitByInstanceID:       map[string]*guestExitState{},
		sidecarsByInstanceID:   map[string][]*exec.Cmd{},
	}
}

func (supervisorService *SupervisorService) BootGuest(bootContext context.Context) (GuestInstance, error) {
	bootSpecification, errorValue := supervisorService.buildBootSpecification()
	if errorValue != nil {
		return GuestInstance{}, errorValue
	}

	standardOutputFile, errorValue := os.OpenFile(filepath.Join(bootSpecification.LogDirectoryPath, "stdout.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if errorValue != nil {
		_ = supervisorService.cleanupOutboundNetwork(bootSpecification.OutboundNetwork)
		_ = removeGuestInstanceDirectory(bootSpecification)
		return GuestInstance{}, errorValue
	}

	standardErrorFile, errorValue := os.OpenFile(filepath.Join(bootSpecification.LogDirectoryPath, "stderr.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if errorValue != nil {
		_ = standardOutputFile.Close()
		_ = supervisorService.cleanupOutboundNetwork(bootSpecification.OutboundNetwork)
		_ = removeGuestInstanceDirectory(bootSpecification)
		return GuestInstance{}, errorValue
	}

	sidecarCommands, errorValue := startSidecars(bootContext, bootSpecification)
	if errorValue != nil {
		_ = standardOutputFile.Close()
		_ = standardErrorFile.Close()
		_ = supervisorService.cleanupOutboundNetwork(bootSpecification.OutboundNetwork)
		_ = removeGuestInstanceDirectory(bootSpecification)
		return GuestInstance{}, errorValue
	}

	command := exec.CommandContext(bootContext, bootSpecification.LaunchExecutablePath, bootSpecification.LaunchArguments...)
	command.Stdout = standardOutputFile
	command.Stderr = standardErrorFile

	errorValue = command.Start()
	_ = standardOutputFile.Close()
	_ = standardErrorFile.Close()
	if errorValue != nil {
		stopSidecars(sidecarCommands)
		_ = supervisorService.cleanupOutboundNetwork(bootSpecification.OutboundNetwork)
		_ = removeGuestInstanceDirectory(bootSpecification)
		return GuestInstance{}, errorValue
	}

	supervisorService.mutex.Lock()
	supervisorService.sidecarsByInstanceID[bootSpecification.InstanceID] = sidecarCommands
	supervisorService.mutex.Unlock()

	exitState := &guestExitState{exited: make(chan struct{})}
	go func() {
		exitState.exitError = command.Wait()
		close(exitState.exited)
	}()

	supervisorService.mutex.Lock()
	supervisorService.commandByInstanceID[bootSpecification.InstanceID] = command
	supervisorService.exitByInstanceID[bootSpecification.InstanceID] = exitState
	supervisorService.mutex.Unlock()

	return GuestInstance{
		InstanceID:        bootSpecification.InstanceID,
		BootSpecification: bootSpecification,
	}, nil
}

func (supervisorService *SupervisorService) StopGuest(guestInstance GuestInstance) error {
	supervisorService.mutex.Lock()
	command, isFound := supervisorService.commandByInstanceID[guestInstance.InstanceID]
	if !isFound {
		supervisorService.mutex.Unlock()
		return errors.New("guest instance was not found")
	}
	exitState := supervisorService.exitByInstanceID[guestInstance.InstanceID]
	sidecarCommands := supervisorService.sidecarsByInstanceID[guestInstance.InstanceID]
	delete(supervisorService.commandByInstanceID, guestInstance.InstanceID)
	delete(supervisorService.exitByInstanceID, guestInstance.InstanceID)
	delete(supervisorService.sidecarsByInstanceID, guestInstance.InstanceID)
	supervisorService.mutex.Unlock()

	var stopError error
	if command.Process != nil {
		if errorValue := command.Process.Kill(); errorValue != nil && !errors.Is(errorValue, os.ErrProcessDone) {
			stopError = errorValue
		}
		if exitState != nil {
			<-exitState.exited
		} else {
			_ = command.Wait()
		}
	}

	stopSidecars(sidecarCommands)

	if cleanupError := supervisorService.cleanupOutboundNetwork(guestInstance.BootSpecification.OutboundNetwork); cleanupError != nil && stopError == nil {
		stopError = cleanupError
	}

	if cleanupError := removeGuestInstanceDirectory(guestInstance.BootSpecification); cleanupError != nil && stopError == nil {
		stopError = cleanupError
	}
	return stopError
}

func (supervisorService *SupervisorService) RestartGuest(bootContext context.Context, guestInstance GuestInstance) (GuestInstance, error) {
	errorValue := supervisorService.StopGuest(guestInstance)
	if errorValue != nil {
		return GuestInstance{}, errorValue
	}

	return supervisorService.BootGuest(bootContext)
}

func (supervisorService *SupervisorService) GuestExited(guestInstance GuestInstance) <-chan struct{} {
	supervisorService.mutex.RLock()
	defer supervisorService.mutex.RUnlock()
	exitState, isFound := supervisorService.exitByInstanceID[guestInstance.InstanceID]
	if !isFound {
		closedChannel := make(chan struct{})
		close(closedChannel)
		return closedChannel
	}
	return exitState.exited
}

func (supervisorService *SupervisorService) WaitForGuestHealth(healthContext context.Context, guestInstance GuestInstance) error {
	healthCheckInterval := supervisorService.HealthCheckInterval
	if healthCheckInterval <= 0 {
		healthCheckInterval = 200 * time.Millisecond
	}

	supervisorService.mutex.RLock()
	exitState := supervisorService.exitByInstanceID[guestInstance.InstanceID]
	supervisorService.mutex.RUnlock()

	var lastError error
	for {
		if exitState != nil {
			select {
			case <-exitState.exited:
				return fmt.Errorf(
					"the monitor exited before the guest became healthy: %v; stderr tail: %s",
					exitState.exitError,
					readLogTail(filepath.Join(guestInstance.BootSpecification.LogDirectoryPath, "stderr.log")),
				)
			default:
			}
		}

		errorValue := supervisorService.GuestHealthClient.CheckHealth(healthContext, guestInstance.BootSpecification)
		if errorValue == nil {
			return nil
		}
		lastError = errorValue

		select {
		case <-healthContext.Done():
			if lastError != nil {
				return fmt.Errorf("guest health did not become ready: %w", lastError)
			}
			return healthContext.Err()
		case <-time.After(healthCheckInterval):
		}
	}
}

func readLogTail(logFilePath string) string {
	document, errorValue := os.ReadFile(logFilePath)
	if errorValue != nil {
		return "(unreadable: " + errorValue.Error() + ")"
	}
	const tailLimit = 2000
	if len(document) > tailLimit {
		document = document[len(document)-tailLimit:]
	}
	tail := strings.TrimSpace(string(document))
	if tail == "" {
		return "(empty)"
	}
	return tail
}

func (supervisorService *SupervisorService) buildBootSpecification() (BootSpecification, error) {
	errorValue := supervisorService.validateConfiguration()
	if errorValue != nil {
		return BootSpecification{}, errorValue
	}

	virtualMachineMonitor, errorValue := supervisorService.resolveVirtualMachineMonitor()
	if errorValue != nil {
		return BootSpecification{}, errorValue
	}

	workspaceVolumeMetadata, errorValue := supervisorService.WorkspaceVolumeService.EnsureWorkspaceImage(
		supervisorService.GuestConfiguration.WorkspaceImagePath,
		supervisorService.GuestConfiguration.WorkspaceMinimumBytes,
	)
	if errorValue != nil {
		return BootSpecification{}, errorValue
	}

	instanceID := newIdentifier()
	logDirectoryPath := filepath.Join(supervisorService.GuestConfiguration.LogDirectoryPath, instanceID)
	errorValue = os.MkdirAll(logDirectoryPath, 0o755)
	if errorValue != nil {
		return BootSpecification{}, errorValue
	}

	runtimeDirectoryPath := supervisorService.runtimeDirectoryPath()
	errorValue = os.MkdirAll(runtimeDirectoryPath, 0o755)
	if errorValue != nil {
		return BootSpecification{}, errorValue
	}
	if errorValue := supervisorService.removeInactiveInstanceDirectories(runtimeDirectoryPath, virtualMachineMonitor.Name()); errorValue != nil {
		return BootSpecification{}, errorValue
	}

	outboundNetwork, networkInterfaces, errorValue := supervisorService.prepareOutboundNetwork(instanceID)
	if errorValue != nil {
		return BootSpecification{}, errorValue
	}

	guestLaunch, errorValue := virtualMachineMonitor.PrepareGuestLaunch(GuestLaunchRequest{
		InstanceID:                instanceID,
		KernelImagePath:           supervisorService.GuestConfiguration.KernelImagePath,
		RootFilesystemImagePath:   supervisorService.GuestConfiguration.RootfsImagePath,
		WorkspaceImagePath:        workspaceVolumeMetadata.HostImagePath,
		RuntimeDirectoryPath:      runtimeDirectoryPath,
		VCPUCount:                 supervisorService.GuestConfiguration.VCPUCount,
		MemoryMiB:                 supervisorService.GuestConfiguration.MemoryMiB,
		VSockCID:                  supervisorService.GuestConfiguration.VSockCID,
		HostDialedGuestVSockPorts: supervisorService.hostDialedGuestVSockPorts(),
		GuestDialedHostVSockPorts: supervisorService.guestDialedHostVSockPorts(),
		NetworkInterfaces:         networkInterfaces,
		DeliveryDirectoryPath:     supervisorService.GuestConfiguration.DeliveryDirectoryPath,
		LogDirectoryPath:          logDirectoryPath,
	})
	if errorValue != nil {
		_ = supervisorService.cleanupOutboundNetwork(outboundNetwork)
		return BootSpecification{}, errorValue
	}

	return BootSpecification{
		InstanceID:                instanceID,
		MonitorName:               virtualMachineMonitor.Name(),
		LogDirectoryPath:          logDirectoryPath,
		InstanceRootPath:          guestLaunch.InstanceRootPath,
		LaunchExecutablePath:      guestLaunch.ExecutablePath,
		LaunchArguments:           guestLaunch.Arguments,
		VSockUnixSocketPath:       guestLaunch.VSockUnixSocketPath,
		VSockUnixSocketPathByPort: guestLaunch.VSockUnixSocketPathByPort,
		Sidecars:                  guestLaunch.Sidecars,
		OutboundNetwork:           outboundNetwork,
		HealthPortOrService:       supervisorService.GuestConfiguration.HealthPortOrService,
		VSockCID:                  supervisorService.GuestConfiguration.VSockCID,
		WorkspaceVolumeMetadata:   workspaceVolumeMetadata,
	}, nil
}

func (supervisorService *SupervisorService) hostDialedGuestVSockPorts() []uint32 {
	configuration := supervisorService.GuestConfiguration
	ports := []uint32{}
	for _, portOrService := range []string{configuration.HealthPortOrService, configuration.GuestHTTPPortOrService} {
		if port, errorValue := strconv.ParseUint(portOrService, 10, 32); errorValue == nil {
			ports = append(ports, uint32(port))
		}
	}
	return ports
}

func (supervisorService *SupervisorService) guestDialedHostVSockPorts() []uint32 {
	ports := []uint32{}
	for _, listenerProxy := range supervisorService.GuestConfiguration.GuestListenerProxies {
		ports = append(ports, listenerProxy.GuestPort)
	}
	return ports
}

func (supervisorService *SupervisorService) resolveVirtualMachineMonitor() (VirtualMachineMonitor, error) {
	if supervisorService.VirtualMachineMonitor != nil {
		return supervisorService.VirtualMachineMonitor, nil
	}
	return SelectVirtualMachineMonitor(
		supervisorService.GuestConfiguration.VirtualMachineMonitor,
		MonitorBinaryPaths{
			CloudHypervisorPath: supervisorService.GuestConfiguration.CloudHypervisorPath,
			VfkitPath:           supervisorService.GuestConfiguration.VfkitPath,
			VirtiofsdPath:       supervisorService.GuestConfiguration.VirtiofsdPath,
		},
	)
}

func (supervisorService *SupervisorService) prepareOutboundNetwork(instanceID string) (OutboundNetwork, []GuestNetworkInterface, error) {
	networkConfiguration := resolvedOutboundNetworkConfiguration(supervisorService.GuestConfiguration.OutboundNetwork, instanceID)
	if !networkConfiguration.Enabled {
		return OutboundNetwork{}, nil, nil
	}

	outboundNetwork := OutboundNetwork{
		Enabled:         true,
		HostDeviceName:  networkConfiguration.HostDeviceName,
		NetworkCIDR:     networkConfiguration.NetworkCIDR,
		HostAddressCIDR: networkConfiguration.HostAddressCIDR,
	}
	networkService := supervisorService.OutboundNetworkService
	if networkService == nil {
		networkService = HostOutboundNetworkService{}
	}
	if errorValue := networkService.PrepareOutboundNetwork(outboundNetwork); errorValue != nil {
		return OutboundNetwork{}, nil, errorValue
	}

	return outboundNetwork, []GuestNetworkInterface{{
		InterfaceID:     "eth0",
		GuestMACAddress: networkConfiguration.GuestMACAddress,
		HostDeviceName:  networkConfiguration.HostDeviceName,
	}}, nil
}

func (supervisorService *SupervisorService) cleanupOutboundNetwork(outboundNetwork OutboundNetwork) error {
	if !outboundNetwork.Enabled {
		return nil
	}
	networkService := supervisorService.OutboundNetworkService
	if networkService == nil {
		networkService = HostOutboundNetworkService{}
	}
	return networkService.CleanupOutboundNetwork(outboundNetwork)
}

func resolvedOutboundNetworkConfiguration(configuration config.OutboundNetworkConfiguration, instanceID string) config.OutboundNetworkConfiguration {
	if !configuration.Enabled {
		return config.OutboundNetworkConfiguration{}
	}
	if strings.TrimSpace(configuration.HostDeviceName) == "" {
		configuration.HostDeviceName = outboundNetworkDeviceName(instanceID)
	}
	if strings.TrimSpace(configuration.GuestMACAddress) == "" {
		configuration.GuestMACAddress = "AA:FC:00:00:00:01"
	}
	if strings.TrimSpace(configuration.NetworkCIDR) == "" {
		configuration.NetworkCIDR = "172.31.0.0/30"
	}
	if strings.TrimSpace(configuration.HostAddressCIDR) == "" {
		configuration.HostAddressCIDR = "172.31.0.1/30"
	}
	if strings.TrimSpace(configuration.GuestAddressCIDR) == "" {
		configuration.GuestAddressCIDR = "172.31.0.2/30"
	}
	if strings.TrimSpace(configuration.GuestGateway) == "" {
		configuration.GuestGateway = "172.31.0.1"
	}
	return configuration
}

func outboundNetworkDeviceName(instanceID string) string {
	trimmedInstanceID := strings.TrimSpace(instanceID)
	if len(trimmedInstanceID) > 8 {
		trimmedInstanceID = trimmedInstanceID[:8]
	}
	if trimmedInstanceID == "" {
		return "bctap0"
	}
	return "bctap" + trimmedInstanceID
}

func removeGuestInstanceDirectory(bootSpecification BootSpecification) error {
	if bootSpecification.InstanceRootPath == "" {
		return nil
	}
	return os.RemoveAll(filepath.Dir(bootSpecification.InstanceRootPath))
}

func (supervisorService *SupervisorService) removeInactiveInstanceDirectories(runtimeDirectoryPath string, monitorName string) error {
	monitorDirectoryPath := filepath.Join(runtimeDirectoryPath, instanceDirectoryNameFor(monitorName))
	entries, errorValue := os.ReadDir(monitorDirectoryPath)
	if os.IsNotExist(errorValue) {
		return nil
	}
	if errorValue != nil {
		return errorValue
	}

	activeInstanceIDs := supervisorService.activeInstanceIDs()
	for _, entry := range entries {
		if !entry.IsDir() || activeInstanceIDs[entry.Name()] {
			continue
		}
		if removeError := os.RemoveAll(filepath.Join(monitorDirectoryPath, entry.Name())); removeError != nil {
			return removeError
		}
	}
	return nil
}

func (supervisorService *SupervisorService) activeInstanceIDs() map[string]bool {
	supervisorService.mutex.RLock()
	defer supervisorService.mutex.RUnlock()

	activeInstanceIDs := map[string]bool{}
	for instanceID := range supervisorService.commandByInstanceID {
		activeInstanceIDs[instanceID] = true
	}
	return activeInstanceIDs
}

func (supervisorService *SupervisorService) runtimeDirectoryPath() string {
	if supervisorService.GuestConfiguration.RuntimeDirectoryPath != "" {
		return supervisorService.GuestConfiguration.RuntimeDirectoryPath
	}
	if runtime.GOOS == "linux" && os.Geteuid() == 0 {
		return "/var/lib/bc"
	}
	return filepath.Join(os.TempDir(), "blueclaw-guest")
}

func (supervisorService *SupervisorService) validateConfiguration() error {
	monitor, errorValue := supervisorService.resolveVirtualMachineMonitor()
	if errorValue != nil {
		return errorValue
	}
	if errorValue := monitor.ValidateBinaryPaths(); errorValue != nil {
		return errorValue
	}
	if supervisorService.GuestConfiguration.KernelImagePath == "" {
		return errors.New("kernelImagePath is required")
	}
	if supervisorService.GuestConfiguration.RootfsImagePath == "" {
		return errors.New("rootfsImagePath is required")
	}
	if supervisorService.GuestConfiguration.WorkspaceImagePath == "" {
		return errors.New("workspaceImagePath is required")
	}
	if supervisorService.GuestConfiguration.LogDirectoryPath == "" {
		return errors.New("logDirectoryPath is required")
	}
	if supervisorService.GuestConfiguration.VCPUCount <= 0 {
		return errors.New("vcpuCount must be positive")
	}
	if supervisorService.GuestConfiguration.MemoryMiB <= 0 {
		return errors.New("memoryMiB must be positive")
	}
	if supervisorService.GuestConfiguration.VSockCID == 0 {
		return errors.New("vsockCID must be positive")
	}
	if supervisorService.GuestConfiguration.HealthPortOrService == "" {
		return errors.New("healthPortOrService is required")
	}
	if supervisorService.GuestConfiguration.GuestHTTPPortOrService == "" {
		return errors.New("guestHTTPPortOrService is required")
	}
	if supervisorService.GuestConfiguration.HostHTTPListenAddress == "" {
		return errors.New("hostHTTPListenAddress is required")
	}

	return nil
}

func startSidecars(bootContext context.Context, bootSpecification BootSpecification) ([]*exec.Cmd, error) {
	started := []*exec.Cmd{}
	for _, sidecar := range bootSpecification.Sidecars {
		command := exec.CommandContext(bootContext, sidecar.ExecutablePath, sidecar.Arguments...)
		logFile, errorValue := os.OpenFile(
			filepath.Join(bootSpecification.LogDirectoryPath, sidecar.Name+".log"),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND,
			0o600,
		)
		if errorValue != nil {
			stopSidecars(started)
			return nil, errorValue
		}
		command.Stdout = logFile
		command.Stderr = logFile
		errorValue = command.Start()
		_ = logFile.Close()
		if errorValue != nil {
			stopSidecars(started)
			return nil, fmt.Errorf("start %s: %w", sidecar.Name, errorValue)
		}
		started = append(started, command)
	}
	return started, nil
}

func stopSidecars(sidecarCommands []*exec.Cmd) {
	for _, command := range sidecarCommands {
		if command.Process == nil {
			continue
		}
		_ = command.Process.Kill()
		_ = command.Wait()
	}
}
