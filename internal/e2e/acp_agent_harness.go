package e2e

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessselection"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
)

func UseBundledACPHarness(bundledACPFactory harnessdriver.ACPFactory) error {
	factory, errorValue := bundledACPHarnessFactory(bundledACPFactory)
	if errorValue != nil {
		return errorValue
	}
	UseAgentHarnessFactory(factory)
	return nil
}

func bundledACPHarnessFactory(bundledACPFactory harnessdriver.ACPFactory) (harnessdriver.Factory, error) {
	resolver := mcpserver.NewSessionTokenRequesterResolver(newSessionToken)
	listener, errorValue := net.Listen("tcp", "127.0.0.1:0")
	if errorValue != nil {
		return nil, errorValue
	}
	go func() { _ = http.Serve(listener, mcpserver.NewToolCatalogHandler(resolver, "e2e")) }()
	return harnessselection.Select(
		config.HarnessConfiguration{Name: harnessselection.BundledACPHarnessName},
		nil,
		harnessselection.ToolCatalogEndpoint{URL: "http://" + listener.Addr().String(), Resolver: resolver},
		harnessselection.SandboxProcessBoundary{},
		harnessselection.WithBundledACPFactory(bundledACPFactory),
	)
}

func newSessionToken() string {
	sessionToken := make([]byte, 16)
	_, _ = rand.Read(sessionToken)
	return hex.EncodeToString(sessionToken)
}
