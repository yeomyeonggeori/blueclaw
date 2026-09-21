package config_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
)

// The share a device's agent is granted is decided in internkim and reaches
// this module only as a field name on the wire, which means the name is spelled
// once on each side of a boundary neither can import across. config/
// runtime.example.json is the committed shape a device is deployed with, and
// internkim's renderer is held to the same file, so a field renamed on either
// side stops matching here and in internkim's conformance test rather than on a
// box.
func TestTheDeviceExampleCarriesTheConnectionShareThisModuleReads(t *testing.T) {
	document, errorValue := os.ReadFile("../../config/runtime.example.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var runtimeConfiguration config.RuntimeConfiguration
	if errorValue := json.Unmarshal(document, &runtimeConfiguration); errorValue != nil {
		t.Fatal(errorValue)
	}
	if runtimeConfiguration.Database.MaxOpenConnections <= 0 {
		t.Fatal("the device example names no database.maxOpenConnections, so this module would ask the server for everything it allows instead of taking the share the host granted")
	}
}
