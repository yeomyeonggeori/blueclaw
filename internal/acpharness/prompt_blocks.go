package acpharness

import (
	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func (harness *Harness) promptBlocksForTurn(request agentcontract.AgentTurnRequest, agentAcceptsImages bool) []acp.ContentBlock {
	promptBlocks := []acp.ContentBlock{acp.TextBlock(harness.promptForTurn(request))}
	if !agentAcceptsImages {
		return promptBlocks
	}
	for _, inputPart := range request.InputParts {
		if imageBlock, isImage := imageBlockOf(inputPart); isImage {
			promptBlocks = append(promptBlocks, imageBlock)
		}
	}
	return promptBlocks
}

func imageBlockOf(inputPart agentcontract.AgentPart) (acp.ContentBlock, bool) {
	if inputPart.Type != agentcontract.AgentPartTypeImage || inputPart.Image == nil || inputPart.Image.DataBase64 == "" {
		return acp.ContentBlock{}, false
	}
	return acp.ImageBlock(inputPart.Image.DataBase64, inputPart.Image.MimeType), true
}
