package agentruntime

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func TestTheHostDescribesItsOwnToolsAndNothingElse(t *testing.T) {
	instruction := hostInstructionForRequest(agentcontract.AgentTurnRequest{})

	for _, mine := range []string{"Approvals and user input:", "Recipients:", "Privacy boundary"} {
		if !strings.Contains(instruction, mine) {
			t.Fatalf("%q describes how this host's own tools behave, so this host is where it comes from", mine)
		}
	}
	for _, notMine := range []string{"Checkpoint messages", "Bare mentions", "Delivery and artifacts", "Language:"} {
		if strings.Contains(instruction, notMine) {
			t.Fatalf("%q is how one product decided to behave, and a host that hardcodes it stops being usable by another", notMine)
		}
	}
	for _, oneHarnessesWord := range []string{"expectsAnswer", "ask.confirm", "approvalRequired", "message field"} {
		if strings.Contains(instruction, oneHarnessesWord) {
			t.Fatalf("every harness reads this instruction, and %q names a field or tool only one of them has", oneHarnessesWord)
		}
	}
}
