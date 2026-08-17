package firecracker

import (
	"context"
	"errors"
	"io"
	"strings"
)

type GuestConnection interface {
	io.ReadWriteCloser
}

type GuestConnectionDialer func(context.Context, string, string) (GuestConnection, error)

type GuestHealthClient interface {
	CheckHealth(context.Context, BootSpecification) error
}

type VSockGuestHealthClient struct {
	DialGuestConnection GuestConnectionDialer
}

func (vsockGuestHealthClient VSockGuestHealthClient) CheckHealth(healthContext context.Context, bootSpecification BootSpecification) error {
	dialGuestConnection := vsockGuestHealthClient.DialGuestConnection
	if dialGuestConnection == nil {
		dialGuestConnection = GuestConnectionDialerFor(bootSpecification.VSockUnixSocketPathByPort)
	}
	vsockUnixSocketPath := bootSpecification.VSockUnixSocketPath
	healthPortOrService := bootSpecification.HealthPortOrService

	attemptContext, cancelAttempt := context.WithTimeout(healthContext, firecrackerVSockOperationTimeout)
	defer cancelAttempt()

	guestConnection, errorValue := dialGuestConnection(attemptContext, vsockUnixSocketPath, healthPortOrService)
	if errorValue != nil {
		return errorValue
	}
	defer guestConnection.Close()

	healthResultChannel := make(chan error, 1)
	go func() {
		_, errorValue = guestConnection.Write([]byte("health\n"))
		if errorValue != nil {
			healthResultChannel <- errorValue
			return
		}

		responseBuffer := make([]byte, 32)
		readLength, errorValue := guestConnection.Read(responseBuffer)
		if errorValue != nil {
			healthResultChannel <- errorValue
			return
		}

		if strings.TrimSpace(string(responseBuffer[:readLength])) != "ok" {
			healthResultChannel <- errors.New("guest health response was not ok")
			return
		}

		healthResultChannel <- nil
	}()

	select {
	case <-attemptContext.Done():
		_ = guestConnection.Close()
		return attemptContext.Err()
	case errorValue = <-healthResultChannel:
		return errorValue
	}
}
