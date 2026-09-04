package acpsession

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"os"
	"path/filepath"

	acp "github.com/coder/acp-go-sdk"
)

type Server struct {
	socketPath      string
	taskLauncher    TaskLauncher
	directory       PersonDirectory
	permissionRelay *PermissionRelay
	turnRouter      TurnRouter
	logger          *slog.Logger
	listener        net.Listener
}

func NewServer(socketPath string, taskLauncher TaskLauncher, directory PersonDirectory, permissionRelay *PermissionRelay, turnRouter TurnRouter, logger *slog.Logger) *Server {
	return &Server{
		socketPath:      socketPath,
		taskLauncher:    taskLauncher,
		directory:       directory,
		permissionRelay: permissionRelay,
		turnRouter:      turnRouter,
		logger:          logger,
	}
}

func (server *Server) Listen() error {
	if errorValue := os.MkdirAll(filepath.Dir(server.socketPath), 0o755); errorValue != nil {
		return errorValue
	}
	if errorValue := os.Remove(server.socketPath); errorValue != nil && !os.IsNotExist(errorValue) {
		return errorValue
	}
	listener, errorValue := net.Listen("unix", server.socketPath)
	if errorValue != nil {
		return errorValue
	}
	if errorValue := os.Chmod(server.socketPath, 0o600); errorValue != nil {
		listener.Close()
		return errorValue
	}
	server.listener = listener
	return nil
}

func (server *Server) Serve(ctx context.Context) {
	server.logger.Info("acpsession.listening", "socketPath", server.socketPath)
	go func() {
		<-ctx.Done()
		server.Close()
	}()
	for {
		connection, errorValue := server.listener.Accept()
		if errorValue != nil {
			if ctx.Err() == nil {
				server.logger.Warn("acpsession.accept.failed", "error", errorValue.Error())
			}
			return
		}
		go server.serveConnection(connection)
	}
}

func (server *Server) serveConnection(connection net.Conn) {
	defer connection.Close()
	agent := NewAgent(server.taskLauncher, server.directory, server.permissionRelay, server.turnRouter, server.logger)
	agentConnection := acp.NewAgentSideConnection(agent, connection, connection)
	agentConnection.SetLogger(server.logger)
	agent.UseConnection(agentConnection)
	<-agentConnection.Done()
	agent.closeEverySession()
	server.logger.Info("acpsession.client.left")
}

func (server *Server) Close() {
	if server.listener == nil {
		return
	}
	server.listener.Close()
	os.Remove(server.socketPath)
}

func newSessionIdentifier() string {
	identifier := make([]byte, 16)
	rand.Read(identifier)
	return hex.EncodeToString(identifier)
}
