package filejournal

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestKeeperReturnsOnlyOnceTheJournalSaysKept(t *testing.T) {
	for _, answer := range []string{"kept", "unjournaled", "failed: the bucket is unreachable"} {
		directory, errorValue := os.MkdirTemp("", "fj")
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		t.Cleanup(func() { os.RemoveAll(directory) })
		socketPath := filepath.Join(directory, "b.sock")
		listener, errorValue := net.Listen("unix", socketPath)
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		go func() {
			connection, errorValue := listener.Accept()
			if errorValue != nil {
				return
			}
			defer connection.Close()
			request, _ := bufio.NewReader(connection).ReadString('\n')
			if request != "wait\n" {
				fmt.Fprintln(connection, "refused")
				return
			}
			fmt.Fprintln(connection, answer)
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		errorValue = Keeper(socketPath)(ctx)
		cancel()
		listener.Close()
		if isKept := errorValue == nil; isKept != (answer != "failed: the bucket is unreachable") {
			t.Errorf("the journal answered %q and the keeper returned %v", answer, errorValue)
		}
	}
}
