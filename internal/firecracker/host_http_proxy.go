package firecracker

import (
	"context"
	"errors"
	"io"
	"net"
)

type HostHTTPProxy struct {
	ListenAddress       string
	VSockUnixSocketPath string
	GuestPortOrService  string
	DialGuestConnection GuestConnectionDialer
}

func (hostHTTPProxy HostHTTPProxy) Serve(ctx context.Context) error {
	if hostHTTPProxy.ListenAddress == "" {
		return errors.New("host HTTP listen address is required")
	}
	if hostHTTPProxy.VSockUnixSocketPath == "" && hostHTTPProxy.DialGuestConnection == nil {
		return errors.New("vsock unix socket path is required")
	}
	if hostHTTPProxy.GuestPortOrService == "" {
		return errors.New("guest HTTP port is required")
	}

	listener, errorValue := net.Listen("tcp", hostHTTPProxy.ListenAddress)
	if errorValue != nil {
		return errorValue
	}
	defer listener.Close()

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		clientConnection, errorValue := listener.Accept()
		if errorValue != nil {
			if ctx.Err() != nil {
				return nil
			}
			return errorValue
		}
		go hostHTTPProxy.proxyConnection(ctx, clientConnection)
	}
}

func (hostHTTPProxy HostHTTPProxy) proxyConnection(ctx context.Context, clientConnection net.Conn) {
	defer clientConnection.Close()

	dialGuestConnection := hostHTTPProxy.DialGuestConnection
	if dialGuestConnection == nil {
		dialGuestConnection = DefaultGuestConnectionDialer
	}

	guestConnection, errorValue := dialGuestConnection(ctx, hostHTTPProxy.VSockUnixSocketPath, hostHTTPProxy.GuestPortOrService)
	if errorValue != nil {
		return
	}
	defer guestConnection.Close()

	done := make(chan struct{}, 2)
	go copyConnection(guestConnection, clientConnection, done)
	go copyConnection(clientConnection, guestConnection, done)
	<-done
}

func copyConnection(destination io.WriteCloser, source io.Reader, done chan<- struct{}) {
	_, _ = io.Copy(destination, source)
	_ = destination.Close()
	done <- struct{}{}
}
