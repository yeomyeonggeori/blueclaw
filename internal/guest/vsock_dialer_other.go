//go:build !linux

package guest

import (
	"context"
	"errors"
)

func DefaultGuestConnectionDialer(healthContext context.Context, vsockUnixSocketPath string, healthPortOrService string) (GuestConnection, error) {
	_ = healthContext
	_ = vsockUnixSocketPath
	_ = healthPortOrService
	return nil, errors.New("vsock guest health is only available on linux")
}
