package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/app"
	"github.com/yeomyeonggeori/blueclaw/internal/buildrevision"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/enrollment"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == mcpserver.StdioBridgeCommand {
		runToolCatalogBridge()
		return
	}
	home := enrollment.ResolveHome()
	runtimeConfigurationPath := flag.String("runtime", home.RuntimeConfigurationPath(), "runtime configuration path")
	policyPath := flag.String("policy", home.PolicyPath(), "policy document path")
	acpSocketPath := flag.String("acp-socket", "", "unix socket to serve the acp agent on; unset serves none")
	inboundPath := flag.String("inbound", app.InboundPathConnectors, "which path admits an inbound message: connectors or acp")
	shouldPrintRevision := flag.Bool("version", false, "print the revision this binary was built from and exit")
	flag.Parse()

	if *shouldPrintRevision {
		fmt.Println(revisionLine())
		return
	}

	runtimeConfiguration, errorValue := config.LoadRuntimeConfiguration(*runtimeConfigurationPath)
	if errorValue != nil {
		log.Fatalf("%v\n\nThis install has no configuration at %s yet. Run blueclaw-cli to set one up.", errorValue, *runtimeConfigurationPath)
	}

	if errorValue := ensureManagedDatabase(home, runtimeConfiguration); errorValue != nil {
		log.Fatal(errorValue)
	}

	application := app.NewApplication(runtimeConfiguration, *policyPath, bundledHarnessFactory(), app.InboundOptions{ACPSocketPath: *acpSocketPath, InboundPath: *inboundPath})
	log.Fatal(application.Start())
}

func revisionLine() string {
	if revision := buildrevision.Revision(); revision != "" {
		return revision
	}
	return "unknown"
}

func runToolCatalogBridge() {
	errorValue := mcpserver.RunStdioCatalogBridge(
		context.Background(),
		os.Getenv(mcpserver.CatalogEndpointEnvironmentName),
		os.Getenv(mcpserver.CatalogTokenEnvironmentName),
	)
	if errorValue != nil {
		log.Fatal(errorValue)
	}
}

func ensureManagedDatabase(home enrollment.Home, runtimeConfiguration config.RuntimeConfiguration) error {
	managedPostgres := enrollment.NewManagedPostgres(home)
	if strings.TrimSpace(runtimeConfiguration.Database.ConnectionString) != managedPostgres.ConnectionString() {
		return nil
	}
	return managedPostgres.EnsureRunning(context.Background())
}
