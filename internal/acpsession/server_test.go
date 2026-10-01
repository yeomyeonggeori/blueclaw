package acpsession

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestTheSocketLetsTheGroupItsDirectoryGivesItConnect(t *testing.T) {
	directoryPath, errorValue := os.MkdirTemp("/tmp", "bc-acp-*")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directoryPath) })
	server := NewServer(filepath.Join(directoryPath, "blueclaw.sock"), Collaborators{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if errorValue := server.Listen(); errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(server.Close)

	information, errorValue := os.Stat(server.socketPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if mode := information.Mode().Perm(); mode != 0o660 {
		t.Fatalf("%s is mode %#o; its group needs read and write to connect, which 0660 gives and nothing wider", server.socketPath, mode)
	}
}
