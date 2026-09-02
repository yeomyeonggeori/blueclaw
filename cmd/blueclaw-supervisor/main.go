package main

import (
	"context"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/guest"
)

const guestHealthTimeout = 300 * time.Second

func main() {
	runtimeConfigurationPath := flag.String("runtime", "config/runtime.example.json", "runtime configuration path")
	flag.Parse()

	runtimeConfiguration, errorValue := config.LoadRuntimeConfiguration(*runtimeConfigurationPath)
	if errorValue != nil {
		log.Fatal(errorValue)
	}

	interruptContext, stopSignalNotification := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignalNotification()

	supervisorService := guest.NewSupervisorService(
		runtimeConfiguration.Guest,
		guest.WorkspaceVolumeService{},
		guest.VSockGuestHealthClient{},
	)

	guestInstance, errorValue := supervisorService.BootGuest(interruptContext)
	if errorValue != nil {
		log.Fatal(errorValue)
	}

	listenerContext, stopListenerProxies := context.WithCancel(interruptContext)
	defer stopListenerProxies()
	for _, listenerProxyConfiguration := range runtimeConfiguration.Guest.GuestListenerProxies {
		listenerProxyConfiguration := listenerProxyConfiguration
		go func() {
			errorValue := guest.GuestListenerProxy{
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
		proxyErrorChannel <- guest.HostHTTPProxy{
			ListenAddress:       runtimeConfiguration.Guest.HostHTTPListenAddress,
			VSockUnixSocketPath: guestInstance.BootSpecification.VSockUnixSocketPath,
			GuestPortOrService:  runtimeConfiguration.Guest.GuestHTTPPortOrService,
			DialGuestConnection: guest.GuestConnectionDialerFor(guestInstance.BootSpecification.VSockUnixSocketPathByPort),
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
	prepareGuestShutdown(runtimeConfiguration.Guest.HostHTTPListenAddress)
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
