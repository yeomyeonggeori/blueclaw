package guest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
)

type GuestListenerProxy struct {
	VSockUnixSocketPath  string
	GuestPort            uint32
	TargetUnixSocketPath string
}

func (guestListenerProxy GuestListenerProxy) Serve(ctx context.Context) error {
	if guestListenerProxy.VSockUnixSocketPath == "" {
		return errors.New("vsock unix socket path is required")
	}
	if guestListenerProxy.GuestPort == 0 {
		return errors.New("guest port is required")
	}
	if guestListenerProxy.TargetUnixSocketPath == "" {
		return errors.New("target unix socket path is required")
	}

	listenPath := fmt.Sprintf("%s_%d", guestListenerProxy.VSockUnixSocketPath, guestListenerProxy.GuestPort)
	_ = os.Remove(listenPath)

	listener, errorValue := net.Listen("unix", listenPath)
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
		go guestListenerProxy.proxyConnection(ctx, clientConnection)
	}
}

func (guestListenerProxy GuestListenerProxy) proxyConnection(ctx context.Context, clientConnection net.Conn) {
	defer clientConnection.Close()

	dialer := net.Dialer{}
	targetConnection, errorValue := dialer.DialContext(ctx, "unix", guestListenerProxy.TargetUnixSocketPath)
	if errorValue != nil {
		return
	}
	defer targetConnection.Close()

	done := make(chan struct{}, 2)
	go copyConnection(targetConnection, clientConnection, done)
	go copyConnection(clientConnection, targetConnection, done)
	<-done
}
