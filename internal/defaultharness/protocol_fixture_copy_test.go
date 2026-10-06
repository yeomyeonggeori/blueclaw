package defaultharness

import (
	"bytes"
	"os"
	"testing"
)

func TestTheLoopsCopyOfTheProtocolFixturesIsTheProtocolSource(t *testing.T) {
	protocolBytes, errorValue := os.ReadFile("../../protocol/fixtures/valid.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	loopBytes, errorValue := os.ReadFile("../../.dependency/bluecollar/loop/testdata/protocol-fixtures.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !bytes.Equal(protocolBytes, loopBytes) {
		t.Fatal("bluecollar's loop/testdata/protocol-fixtures.json drifted from protocol/fixtures/valid.json: copy the protocol file into bluecollar")
	}
}
