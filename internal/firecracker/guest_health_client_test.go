package firecracker

import (
	"context"
	"errors"
	"io"
	"testing"
)

type staticGuestConnection struct {
	response io.Reader
}

func (staticGuestConnection) Close() error {
	return nil
}

func (staticGuestConnection) Write(message []byte) (int, error) {
	return len(message), nil
}

func (guestConnection staticGuestConnection) Read(buffer []byte) (int, error) {
	return guestConnection.response.Read(buffer)
}

func TestVSockGuestHealthClientChecksHealth(t *testing.T) {
	guestHealthClient := VSockGuestHealthClient{
		DialGuestConnection: func(healthContext context.Context, vsockUnixSocketPath string, healthPortOrService string) (GuestConnection, error) {
			_ = healthContext
			if vsockUnixSocketPath != "/tmp/firecracker-vsock.socket" {
				t.Fatalf("expected vsock socket path to match, got %q", vsockUnixSocketPath)
			}
			if healthPortOrService != "8080" {
				t.Fatalf("expected health service to match, got %q", healthPortOrService)
			}
			return staticGuestConnection{response: stringsReader("ok\n")}, nil
		},
	}

	errorValue := guestHealthClient.CheckHealth(context.Background(), BootSpecification{VSockUnixSocketPath: "/tmp/firecracker-vsock.socket", HealthPortOrService: "8080"})
	if errorValue != nil {
		t.Fatalf("expected health check to succeed: %v", errorValue)
	}
}

func TestVSockGuestHealthClientFailsOnUnexpectedHealth(t *testing.T) {
	guestHealthClient := VSockGuestHealthClient{
		DialGuestConnection: func(healthContext context.Context, vsockUnixSocketPath string, healthPortOrService string) (GuestConnection, error) {
			_ = healthContext
			_ = vsockUnixSocketPath
			_ = healthPortOrService
			return staticGuestConnection{response: stringsReader("bad\n")}, nil
		},
	}

	errorValue := guestHealthClient.CheckHealth(context.Background(), BootSpecification{VSockUnixSocketPath: "/tmp/firecracker-vsock.socket", HealthPortOrService: "8080"})
	if errorValue == nil {
		t.Fatal("expected unexpected health response to fail")
	}
}

func stringsReader(value string) io.Reader {
	return &staticStringReader{value: []byte(value)}
}

type staticStringReader struct {
	value []byte
}

func (staticStringReader *staticStringReader) Read(buffer []byte) (int, error) {
	if len(staticStringReader.value) == 0 {
		return 0, io.EOF
	}

	readLength := copy(buffer, staticStringReader.value)
	staticStringReader.value = staticStringReader.value[readLength:]
	return readLength, nil
}

type failingGuestConnection struct{}

func (failingGuestConnection) Close() error {
	return nil
}

func (failingGuestConnection) Write(message []byte) (int, error) {
	_ = message
	return 0, errors.New("write failed")
}

func (failingGuestConnection) Read(buffer []byte) (int, error) {
	_ = buffer
	return 0, io.EOF
}
