package agentruntime

import (
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestTheAllowedBaselineNamesEachToolOnce(t *testing.T) {
	timesNamed := map[string]int{}
	for _, toolName := range DefaultAllowedToolNames() {
		timesNamed[toolName]++
	}
	for toolName, count := range timesNamed {
		if count > 1 {
			t.Fatalf("the baseline names %s %d times, so something it already inherits is being added again", toolName, count)
		}
	}
	if timesNamed[toolcontract.AskInputToolName] == 0 {
		t.Fatalf("expected the baseline to carry ask_input, which the runtime invokes and the model never calls, got %v", DefaultAllowedToolNames())
	}
}
