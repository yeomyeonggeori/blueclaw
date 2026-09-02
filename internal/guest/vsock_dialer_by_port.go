package guest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
)

// A monitor that binds a socket per guest port hands back a stream with nothing to
// negotiate, so the CONNECT line the multiplexed monitors expect would be read by the
// guest as the first bytes of the conversation.
func GuestConnectionDialerFor(vsockUnixSocketPathByPort map[uint32]string) GuestConnectionDialer {
	if len(vsockUnixSocketPathByPort) == 0 {
		return DefaultGuestConnectionDialer
	}
	return func(dialContext context.Context, vsockUnixSocketPath string, guestPortOrService string) (GuestConnection, error) {
		_ = vsockUnixSocketPath
		guestPort, errorValue := strconv.ParseUint(guestPortOrService, 10, 32)
		if errorValue != nil {
			return nil, errorValue
		}
		socketPath := vsockUnixSocketPathByPort[uint32(guestPort)]
		if socketPath == "" {
			return nil, fmt.Errorf("no vsock socket is bound for guest port %d", guestPort)
		}
		return dialGuestPortDirectly(dialContext, socketPath)
	}
}

func dialGuestPortDirectly(dialContext context.Context, vsockUnixSocketPath string) (GuestConnection, error) {
	if vsockUnixSocketPath == "" {
		return nil, errors.New("no vsock socket path was given")
	}

	var dialer net.Dialer
	return dialer.DialContext(dialContext, "unix", vsockUnixSocketPath)
}
