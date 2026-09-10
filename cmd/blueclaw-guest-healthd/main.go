//go:build linux

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mdlayher/vsock"
)

func main() {
	port := flag.Uint("port", 8080, "guest vsock health port")
	blueclawURL := flag.String("blueclaw-url", "", "optional Blueclaw HTTP health URL; empty means liveness only")
	flag.Parse()

	listener, errorValue := listenVSock(uint32(*port))
	if errorValue != nil {
		log.Fatal(errorValue)
	}
	defer listener.Close()

	for {
		connection, errorValue := listener.Accept()
		if errorValue != nil {
			log.Printf("accept health connection: %v", errorValue)
			continue
		}
		go handleHealthConnection(connection, []string{*blueclawURL})
	}
}

func listenVSock(port uint32) (net.Listener, error) {
	return vsock.Listen(port, nil)
}

func handleHealthConnection(connection net.Conn, healthURLs []string) {
	defer connection.Close()

	buffer := make([]byte, 64)
	byteCount, errorValue := connection.Read(buffer)
	if errorValue != nil && errorValue != io.EOF {
		_, _ = connection.Write([]byte("error\n"))
		return
	}
	if strings.TrimSpace(string(buffer[:byteCount])) != "health" {
		_, _ = connection.Write([]byte("error\n"))
		return
	}
	if errorValue := checkHealthURLs(healthURLs); errorValue != nil {
		_, _ = connection.Write([]byte("error\n"))
		return
	}
	_, _ = connection.Write([]byte("ok\n"))
}

func checkHealthURLs(healthURLs []string) error {
	client := http.Client{Timeout: 2 * time.Second}
	for _, healthURL := range healthURLs {
		if strings.TrimSpace(healthURL) == "" {
			continue
		}
		requestContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		request, errorValue := http.NewRequestWithContext(requestContext, http.MethodGet, healthURL, nil)
		if errorValue != nil {
			cancel()
			return errorValue
		}
		response, errorValue := client.Do(request)
		cancel()
		if errorValue != nil {
			return errorValue
		}
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("health URL %s returned %d", healthURL, response.StatusCode)
		}
	}
	return nil
}
