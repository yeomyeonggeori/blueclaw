package mcpserver

import (
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

// ToolAudience says which tools a catalog publishes. A harness that brings its
// own generic tools should not be handed ours as well; one that brings none
// needs them.
type ToolAudience string

const (
	// ToolAudienceSelfEquipped is a harness with its own shell and file tools.
	ToolAudienceSelfEquipped ToolAudience = "self_equipped"
	// ToolAudienceBare is a harness with no tools of its own. It is offered
	// every tool it may call, the hidden ones the loop calls on the model's
	// behalf included.
	ToolAudienceBare ToolAudience = "bare"
)

func isPublishedToAudience(toolDescriptor toolcontract.ToolDescriptor, audience ToolAudience) bool {
	if audience != ToolAudienceSelfEquipped {
		return true
	}
	return !toolDescriptor.IsBroughtByEveryHarness
}
