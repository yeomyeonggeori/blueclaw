package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/firecracker"
)

const guestHealthTimeout = 300 * time.Second

func main() {
	if len(os.Args) > 1 && os.Args[1] == "sync-workspace" {
		if errorValue := syncWorkspace(os.Args[2:]); errorValue != nil {
			log.Fatal(errorValue)
		}
		return
	}

	runtimeConfigurationPath := flag.String("runtime", "config/runtime.example.json", "runtime configuration path")
	flag.Parse()

	runtimeConfiguration, errorValue := config.LoadRuntimeConfiguration(*runtimeConfigurationPath)
	if errorValue != nil {
		log.Fatal(errorValue)
	}

	interruptContext, stopSignalNotification := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignalNotification()

	supervisorService := firecracker.NewSupervisorService(
		runtimeConfiguration.Firecracker,
		firecracker.WorkspaceVolumeService{},
		firecracker.VSockGuestHealthClient{},
	)

	guestInstance, errorValue := supervisorService.BootGuest(interruptContext)
	if errorValue != nil {
		log.Fatal(errorValue)
	}

	listenerContext, stopListenerProxies := context.WithCancel(interruptContext)
	defer stopListenerProxies()
	for _, listenerProxyConfiguration := range runtimeConfiguration.Firecracker.GuestListenerProxies {
		listenerProxyConfiguration := listenerProxyConfiguration
		go func() {
			errorValue := firecracker.GuestListenerProxy{
				VSockUnixSocketPath:  guestInstance.BootSpecification.VSockUnixSocketPath,
				GuestPort:            listenerProxyConfiguration.GuestPort,
				TargetUnixSocketPath: listenerProxyConfiguration.TargetUnixSocketPath,
			}.Serve(listenerContext)
			if errorValue != nil && listenerContext.Err() == nil {
				log.Printf("guest listener proxy stopped: %v", errorValue)
			}
		}()
	}

	healthContext, cancelHealthCheck := context.WithTimeout(interruptContext, guestHealthTimeout)
	defer cancelHealthCheck()

	errorValue = supervisorService.WaitForGuestHealth(healthContext, guestInstance)
	if errorValue != nil {
		_ = supervisorService.StopGuest(guestInstance)
		log.Fatal(errorValue)
	}

	proxyContext, stopProxy := context.WithCancel(context.Background())
	defer stopProxy()
	proxyErrorChannel := make(chan error, 1)
	go func() {
		proxyErrorChannel <- firecracker.HostHTTPProxy{
			ListenAddress:       runtimeConfiguration.Firecracker.HostHTTPListenAddress,
			VSockUnixSocketPath: guestInstance.BootSpecification.VSockUnixSocketPath,
			GuestPortOrService:  runtimeConfiguration.Firecracker.GuestHTTPPortOrService,
		}.Serve(proxyContext)
	}()

	select {
	case errorValue = <-proxyErrorChannel:
		_ = supervisorService.StopGuest(guestInstance)
		log.Fatal(errorValue)
	case <-time.After(200 * time.Millisecond):
	}

	select {
	case <-supervisorService.GuestExited(guestInstance):
		stopProxy()
		stopListenerProxies()
		_ = supervisorService.StopGuest(guestInstance)
		log.Fatal("guest exited after becoming healthy; restarting the supervisor")
	case <-interruptContext.Done():
	}
	prepareGuestShutdown(runtimeConfiguration.Firecracker.HostHTTPListenAddress)
	stopProxy()
	stopListenerProxies()
	errorValue = supervisorService.StopGuest(guestInstance)
	if errorValue != nil {
		log.Fatal(errorValue)
	}
}

func prepareGuestShutdown(hostHTTPListenAddress string) {
	if hostHTTPListenAddress == "" {
		return
	}
	requestContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, errorValue := http.NewRequestWithContext(requestContext, http.MethodPost, "http://"+hostHTTPListenAddress+"/admin/api/runtime/prepare-shutdown", nil)
	if errorValue != nil {
		log.Printf("guest prepare-shutdown request build failed: %v", errorValue)
		return
	}
	response, errorValue := http.DefaultClient.Do(request)
	if errorValue != nil {
		log.Printf("guest prepare-shutdown unavailable; stopping without task interruption marker: %v", errorValue)
		return
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	log.Printf("guest prepare-shutdown status=%d body=%s", response.StatusCode, strings.TrimSpace(string(body)))
}

func syncWorkspace(arguments []string) error {
	flagSet := flag.NewFlagSet("sync-workspace", flag.ContinueOnError)
	runtimeConfigurationPath := flagSet.String("runtime", "", "runtime configuration path")
	workspaceImagePath := flagSet.String("workspace-image", "", "workspace image path")
	sourceDirectoryPath := flagSet.String("source", "", "source directory path")
	relativeTargetPath := flagSet.String("relative-target", "", "workspace-relative target directory")
	isAtomic := flagSet.Bool("atomic", false, "copy and atomically replace the workspace image")
	preserveGuestState := flagSet.Bool("preserve-guest-state", false, "preserve guest-owned workspace state")
	if errorValue := flagSet.Parse(arguments); errorValue != nil {
		return errorValue
	}
	if *sourceDirectoryPath == "" {
		return fmt.Errorf("source directory path is required")
	}
	if !*isAtomic {
		return fmt.Errorf("sync-workspace requires --atomic")
	}

	resolvedWorkspaceImagePath := *workspaceImagePath
	if resolvedWorkspaceImagePath == "" {
		if *runtimeConfigurationPath == "" {
			return fmt.Errorf("workspace image path or runtime configuration path is required")
		}
		runtimeConfiguration, errorValue := config.LoadRuntimeConfiguration(*runtimeConfigurationPath)
		if errorValue != nil {
			return errorValue
		}
		resolvedWorkspaceImagePath = runtimeConfiguration.Firecracker.WorkspaceImagePath
	}

	workspaceVolumeService := firecracker.WorkspaceVolumeService{}
	if *preserveGuestState {
		if *relativeTargetPath != "" {
			return fmt.Errorf("--preserve-guest-state cannot be combined with --relative-target")
		}
		return workspaceVolumeService.SyncWorkspaceDirectoryPreservingGuestState(resolvedWorkspaceImagePath, *sourceDirectoryPath)
	}
	return workspaceVolumeService.SyncWorkspaceDirectory(resolvedWorkspaceImagePath, *sourceDirectoryPath, *relativeTargetPath)
}
