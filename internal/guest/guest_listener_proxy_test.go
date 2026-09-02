package guest

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGuestListenerProxyForwardsBytesBidirectionally(t *testing.T) {
	temporaryDirectory := mustShortTempDir(t)
	vsockUnixSocketPath := filepath.Join(temporaryDirectory, "guest-vsock.socket")
	targetUnixSocketPath := filepath.Join(temporaryDirectory, "target.socket")
	const guestPort uint32 = 7000

	targetListener, errorValue := net.Listen("unix", targetUnixSocketPath)
	if errorValue != nil {
		t.Fatalf("create target listener: %v", errorValue)
	}
	defer targetListener.Close()

	targetReceived := make(chan string, 1)
	go func() {
		targetConnection, errorValue := targetListener.Accept()
		if errorValue != nil {
			return
		}
		defer targetConnection.Close()
		buffer := make([]byte, 64)
		readLength, _ := targetConnection.Read(buffer)
		targetReceived <- string(buffer[:readLength])
		_, _ = targetConnection.Write([]byte("response-from-target"))
	}()

	proxyContext, cancelProxy := context.WithCancel(context.Background())
	defer cancelProxy()

	proxy := GuestListenerProxy{
		VSockUnixSocketPath:  vsockUnixSocketPath,
		GuestPort:            guestPort,
		TargetUnixSocketPath: targetUnixSocketPath,
	}
	proxyDone := make(chan error, 1)
	go func() { proxyDone <- proxy.Serve(proxyContext) }()

	listenPath := fmt.Sprintf("%s_%d", vsockUnixSocketPath, guestPort)
	clientConnection, errorValue := dialUnixWithRetry(listenPath, 2*time.Second)
	if errorValue != nil {
		t.Fatalf("dial proxy listener: %v", errorValue)
	}
	defer clientConnection.Close()

	if _, errorValue := clientConnection.Write([]byte("hello-from-guest")); errorValue != nil {
		t.Fatalf("write to proxy: %v", errorValue)
	}

	select {
	case message := <-targetReceived:
		if message != "hello-from-guest" {
			t.Fatalf("target received unexpected message: %q", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("target did not receive message within timeout")
	}

	buffer := make([]byte, 64)
	_ = clientConnection.SetReadDeadline(time.Now().Add(2 * time.Second))
	readLength, errorValue := clientConnection.Read(buffer)
	if errorValue != nil {
		t.Fatalf("read response from proxy: %v", errorValue)
	}
	if string(buffer[:readLength]) != "response-from-target" {
		t.Fatalf("proxy returned unexpected response: %q", string(buffer[:readLength]))
	}

	cancelProxy()
	select {
	case <-proxyDone:
	case <-time.After(time.Second):
		t.Fatal("proxy did not stop after context cancel")
	}
}

func TestGuestListenerProxyRejectsMissingFields(t *testing.T) {
	cases := []GuestListenerProxy{
		{GuestPort: 7000, TargetUnixSocketPath: "/tmp/target.socket"},
		{VSockUnixSocketPath: "/tmp/guest-vsock.socket", TargetUnixSocketPath: "/tmp/target.socket"},
		{VSockUnixSocketPath: "/tmp/guest-vsock.socket", GuestPort: 7000},
	}
	for index, proxy := range cases {
		errorValue := proxy.Serve(context.Background())
		if errorValue == nil {
			t.Fatalf("case %d expected validation error", index)
		}
	}
}

func TestGuestListenerProxyReplacesStaleListenSocket(t *testing.T) {
	temporaryDirectory := mustShortTempDir(t)
	vsockUnixSocketPath := filepath.Join(temporaryDirectory, "guest-vsock.socket")
	const guestPort uint32 = 7000
	listenPath := fmt.Sprintf("%s_%d", vsockUnixSocketPath, guestPort)

	if errorValue := os.WriteFile(listenPath, []byte{}, 0o600); errorValue != nil {
		t.Fatalf("create stale file: %v", errorValue)
	}

	targetUnixSocketPath := filepath.Join(temporaryDirectory, "target.socket")
	targetListener, errorValue := net.Listen("unix", targetUnixSocketPath)
	if errorValue != nil {
		t.Fatalf("create target listener: %v", errorValue)
	}
	defer targetListener.Close()

	proxyContext, cancelProxy := context.WithCancel(context.Background())
	defer cancelProxy()

	proxy := GuestListenerProxy{
		VSockUnixSocketPath:  vsockUnixSocketPath,
		GuestPort:            guestPort,
		TargetUnixSocketPath: targetUnixSocketPath,
	}
	proxyDone := make(chan error, 1)
	go func() { proxyDone <- proxy.Serve(proxyContext) }()

	clientConnection, errorValue := dialUnixWithRetry(listenPath, 2*time.Second)
	if errorValue != nil {
		t.Fatalf("dial proxy listener after stale removal: %v", errorValue)
	}
	clientConnection.Close()

	cancelProxy()
	select {
	case <-proxyDone:
	case <-time.After(time.Second):
		t.Fatal("proxy did not stop after context cancel")
	}
}

func mustShortTempDir(t *testing.T) string {
	t.Helper()
	directoryPath, errorValue := os.MkdirTemp("/tmp", "bc-glp-")
	if errorValue != nil {
		t.Fatalf("create temp dir: %v", errorValue)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directoryPath) })
	return directoryPath
}

func dialUnixWithRetry(path string, totalTimeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(totalTimeout)
	for {
		connection, errorValue := net.Dial("unix", path)
		if errorValue == nil {
			return connection, nil
		}
		if time.Now().After(deadline) {
			return nil, errorValue
		}
		time.Sleep(20 * time.Millisecond)
	}
}
