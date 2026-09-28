package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/modelstandin"
)

func main() {
	listenAddress := flag.String("listen", "127.0.0.1:0", "the address the stand-in model answers on")
	flag.Parse()
	server := &http.Server{
		Addr:              *listenAddress,
		Handler:           modelstandin.NewServer(os.Stderr).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	if errorValue := server.ListenAndServe(); !errors.Is(errorValue, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "blueclaw-model-stand-in:", errorValue)
		os.Exit(1)
	}
}
