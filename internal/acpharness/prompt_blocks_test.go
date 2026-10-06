package acpharness

import (
	"context"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func runTurnWithImage(t *testing.T, agent *externalAgent) {
	t.Helper()
	executed := []daemonExecutedTool{}
	harness := New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), nil)
	if _, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		Prompt:            "이 사진 봐줘",
		InputParts: []agentcontract.AgentPart{
			{Type: agentcontract.AgentPartTypeImage, Image: &agentcontract.AgentImagePart{MimeType: "image/png", DataBase64: "aGVsbG8="}},
			{Type: agentcontract.AgentPartTypeImage, Image: &agentcontract.AgentImagePart{MimeType: "image/png", Path: "/workspace/only-a-path.png"}},
		},
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	}); errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}
}

func imageBlocksOf(contentBlocks []acp.ContentBlock) []acp.ContentBlock {
	imageBlocks := []acp.ContentBlock{}
	for _, contentBlock := range contentBlocks {
		if contentBlock.Image != nil {
			imageBlocks = append(imageBlocks, contentBlock)
		}
	}
	return imageBlocks
}

func TestAnImageThePersonSentReachesAnAgentThatTakesImages(t *testing.T) {
	agent := &externalAgent{declaresImages: true}

	runTurnWithImage(t, agent)

	imageBlocks := imageBlocksOf(agent.observedPromptBlocks)
	if len(imageBlocks) != 1 || imageBlocks[0].Image.Data != "aGVsbG8=" || imageBlocks[0].Image.MimeType != "image/png" {
		t.Fatalf("expected the one image that carries bytes, got %+v", agent.observedPromptBlocks)
	}
}

func TestAnImageIsNotSentToAnAgentThatDoesNotDeclareImages(t *testing.T) {
	agent := &externalAgent{}

	runTurnWithImage(t, agent)

	if imageBlocks := imageBlocksOf(agent.observedPromptBlocks); len(imageBlocks) != 0 {
		t.Fatalf("an agent that never said it takes images may reject the whole prompt, got %+v", imageBlocks)
	}
}

func TestTheHostInstructionLeadsTheRequestOnlyWhenTheHarnessIsToldToSendIt(t *testing.T) {
	for _, includesHostInstruction := range []bool{true, false} {
		agent := &externalAgent{}
		executed := []daemonExecutedTool{}
		harness := New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), nil)
		if includesHostInstruction {
			harness.UseHostInstruction()
		}

		harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
			RequesterPersonID: "person-1",
			HostInstruction:   "Ask the user only when their choice is required.\n\nThe requester prefers terse answers.",
			Prompt:            "회의록 정리해줘",
			WorkspaceRootPath: t.TempDir(),
			ToolSet:           requesterToolSet(t, "person-1", &executed),
		})

		hostInstructionIndex := strings.Index(agent.observedPrompt, "The requester prefers terse answers.")
		promptIndex := strings.Index(agent.observedPrompt, "회의록 정리해줘")
		if includesHostInstruction && (hostInstructionIndex < 0 || hostInstructionIndex > promptIndex) {
			t.Fatalf("the host instruction and the requester persona it carries come before the request, got:\n%s", agent.observedPrompt)
		}
		if !includesHostInstruction && hostInstructionIndex >= 0 {
			t.Fatalf("an agent that has its own instructions is not handed the in-process loop's, got:\n%s", agent.observedPrompt)
		}
	}
}

func TestAHarnessOfferedTheBareAudienceSaysSoToTheCatalog(t *testing.T) {
	executed := []daemonExecutedTool{}
	toolCatalog := newPublishedToolCatalog(t)
	harness := New(&inProcessAgentProcess{agent: &externalAgent{}}, toolCatalog, nil)
	harness.UseToolAudience(mcpserver.ToolAudienceBare)

	harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	})

	if toolCatalog.publishedToolSet.ToolAudience != mcpserver.ToolAudienceBare {
		t.Fatalf("an agent with no tools of its own needs every one of ours, got audience %q", toolCatalog.publishedToolSet.ToolAudience)
	}
}

func TestAPromptMetaTheHostAddsReachesTheAgentBesideTheCarriedOutCalls(t *testing.T) {
	executed := []daemonExecutedTool{}
	agent := &externalAgent{}
	harness := New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), nil)
	harness.UsePromptMeta(func(request agentcontract.AgentTurnRequest) map[string]any {
		return map[string]any{"example.com/run": request.ExistingTaskRunID}
	})

	harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		ExistingTaskRunID: "run-7",
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	})

	if agent.observedPromptMeta["example.com/run"] != "run-7" {
		t.Fatalf("expected the host's meta on the prompt, got %+v", agent.observedPromptMeta)
	}
}
