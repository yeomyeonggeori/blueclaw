package filejournal

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
)

const (
	barrierRequest     = "wait"
	barrierKept        = "kept"
	barrierUnjournaled = "unjournaled"
)

func Keeper(socketPath string) func(context.Context) error {
	return func(ctx context.Context) error {
		var dialer net.Dialer
		connection, errorValue := dialer.DialContext(ctx, "unix", socketPath)
		if errorValue != nil {
			return fmt.Errorf("asking the file journal at %s to keep what was written: %w", socketPath, errorValue)
		}
		defer connection.Close()
		if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
			connection.SetDeadline(deadline)
		}
		if _, errorValue := fmt.Fprintln(connection, barrierRequest); errorValue != nil {
			return fmt.Errorf("asking the file journal at %s to keep what was written: %w", socketPath, errorValue)
		}
		answer, errorValue := bufio.NewReader(connection).ReadString('\n')
		if errorValue != nil {
			return fmt.Errorf("waiting for the file journal at %s: %w", socketPath, errorValue)
		}
		switch strings.TrimSpace(answer) {
		case barrierKept, barrierUnjournaled:
			return nil
		}
		return fmt.Errorf("the file journal at %s did not keep what was written: %s", socketPath, strings.TrimSpace(answer))
	}
}
