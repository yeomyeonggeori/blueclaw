//go:build !nobundledharness

package defaultharness

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/bluecollar/acpagent"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type recordingLLMCallRepository struct{}

func (recordingLLMCallRepository) InsertLLMCall(agentcontract.TaskEvent, agentcontract.LLMCallRecord) error {
	return nil
}

var _ taskstate.LLMCallRepository = recordingLLMCallRepository{}

func toolSetWithOneGatedTool(t *testing.T) *toolcontract.ToolSet {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{"event_add", "event_list"})
	toolSet.AllowTestReplacement()
	for toolName, requiresApproval := range map[string]bool{"event_add": true, "event_list": false} {
		errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
			ID: "test:" + toolName, Name: toolName, Description: toolName, Visibility: toolcontract.ToolVisibilityModel,
			RequiresApproval: requiresApproval,
			InputSchema:      json.RawMessage(`{"type":"object"}`),
			ResultContract:   &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object"}`)},
		}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			return toolcontract.ToolSuccess("ok"), nil
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	return toolSet
}

func TestThePromptMetaNamesTheRunAndHandsOverTheRequestWithoutWhatTheAgentOwns(t *testing.T) {
	request := agentcontract.AgentTurnRequest{
		ExistingTaskRunID: "run-7",
		ToolSet:           toolSetWithOneGatedTool(t),
		HostInstruction:   "Ask the user only when their choice is required.",
		MemoryFacts:       []agentcontract.MemoryFact{{Content: "prefers short replies"}},
		VisibleContext:    agentcontract.VisibleContext{CurrentMaterials: []agentcontract.VisibleContextMaterial{{Filename: "notes.csv"}}},
		InputParts: []agentcontract.AgentPart{
			{Type: agentcontract.AgentPartTypeFile, File: &agentcontract.AgentFilePart{Filename: "notes.csv"}},
			{Type: agentcontract.AgentPartTypeImage, Image: &agentcontract.AgentImagePart{MimeType: "image/png", DataBase64: "aGVsbG8="}},
		},
	}

	meta := promptMetaFor(harnessdriver.Dependencies{})(request)

	if meta[acpagent.TaskRunMetaKey] != "run-7" {
		t.Fatalf("the agent adopts the run the host named, got %+v", meta)
	}
	handedOver, isRequest := meta[acpagent.TurnRequestMetaKey].(agentcontract.AgentTurnRequest)
	if !isRequest || len(handedOver.VisibleContext.CurrentMaterials) != 1 {
		t.Fatalf("the context the host shows people has to go with the turn, got %+v", meta[acpagent.TurnRequestMetaKey])
	}
	if len(handedOver.InputParts) != 1 || handedOver.InputParts[0].Type != agentcontract.AgentPartTypeFile {
		t.Fatalf("the files go in the turn request and the images in the prompt blocks, got %+v", handedOver.InputParts)
	}
	if handedOver.ToolSet != nil || handedOver.HostInstruction != "" || len(handedOver.MemoryFacts) != 0 {
		t.Fatalf("what the preamble says and what the agent owns are not said a second time, got %+v", handedOver)
	}
}

func TestATurnWithNoRunNamesNoneToTheAgent(t *testing.T) {
	if _, isNamed := promptMetaFor(harnessdriver.Dependencies{})(agentcontract.AgentTurnRequest{})[acpagent.TaskRunMetaKey]; isNamed {
		t.Fatal("an empty run name would make the agent adopt a run called nothing")
	}
}

func TestTheCallsTableIsMirroredOnlyWhenTheAgentDoesNotWriteItsOwn(t *testing.T) {
	withoutRepository := skippedLedgerEventNames(harnessdriver.Dependencies{})
	withRepository := skippedLedgerEventNames(harnessdriver.Dependencies{LLMCallRepository: recordingLLMCallRepository{}})

	if slices.Contains(withoutRepository, agentcontract.TaskEventLLMCall) {
		t.Fatal("with no llm_call table the ledger is the only place a model call is recorded")
	}
	if !slices.Contains(withRepository, agentcontract.TaskEventLLMCall) {
		t.Fatal("the agent writes its model calls to llm_call itself, and mirroring them as events shows each twice")
	}
	if slices.Contains(withoutRepository, agentcontract.TaskEventAgentSteerReceived) {
		t.Fatal("the agent's record that it received a steer is its own fact and reaches the run beside the host's record of the request")
	}
}
