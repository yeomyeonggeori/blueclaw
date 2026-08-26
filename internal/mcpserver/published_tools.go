package mcpserver

import (
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

// harnessOwnedToolNames are the tools every agent harness already brings: a
// shell and a way to read, write and edit files. Publishing our versions
// alongside theirs offers the same job twice, and the harness is better at
// its own. The kernel, not this list, is what keeps either version inside the
// requester's identity.
var harnessOwnedToolNames = map[string]bool{
	toolcontract.ShellToolName:       true,
	toolcontract.FileReadToolName:    true,
	toolcontract.FileWriteToolName:   true,
	toolcontract.FileEditToolName:    true,
	toolcontract.FilePreviewToolName: true,
	toolcontract.ImageReadToolName:   true,
	toolcontract.PlanUpdateToolName:  true,
	toolcontract.SkillSearchToolName: true,
}

// ToolAudience says which tools a catalog publishes. A harness that brings its
// own generic tools should not be handed ours as well; one that brings none
// needs them.
type ToolAudience string

const (
	// ToolAudienceSelfEquipped is a harness with its own shell and file tools.
	ToolAudienceSelfEquipped ToolAudience = "self_equipped"
	// ToolAudienceBare is a harness with no tools of its own.
	ToolAudienceBare ToolAudience = "bare"
)

func isPublishedToAudience(toolDescriptor toolcontract.ToolDescriptor, audience ToolAudience) bool {
	if audience != ToolAudienceSelfEquipped {
		return true
	}
	return !harnessOwnedToolNames[toolDescriptor.Name]
}
