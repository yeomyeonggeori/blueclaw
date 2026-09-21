package main

import (
	"os"
	"strings"
	"testing"
)

// The guest rootfs is not guaranteed to carry /usr/share/zoneinfo, and a
// company zone this binary cannot load is read as UTC instead.
func TestTheAgentCarriesItsOwnTimeZoneDatabase(t *testing.T) {
	source, errorValue := os.ReadFile("main.go")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.Contains(string(source), `_ "time/tzdata"`) {
		t.Fatal("the agent must embed the time zone database rather than trust the image it runs in")
	}
}
