package connectors

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

const approvalInteractionKind = "approval"

func heldCallInteraction(holdID string, choices []holdrecord.Choice, heldToolInput json.RawMessage) *AskInteraction {
	if strings.TrimSpace(holdID) == "" {
		return nil
	}
	if approvalgate.TakesOwnWords(choices) {
		return &AskInteraction{InteractionID: strings.TrimSpace(holdID), Kind: agentcontract.AskKindInput, Question: approvalgate.AskedQuestion(heldToolInput), Options: answerOptionsOf(choices)}
	}
	if len(choices) > 0 {
		return nil
	}
	return &AskInteraction{InteractionID: strings.TrimSpace(holdID), Kind: approvalInteractionKind, Options: []AskChoiceOption{{Key: ApproveOptionID}, {Key: RejectOptionID}}}
}

func answerOptionsOf(choices []holdrecord.Choice) []AskChoiceOption {
	options := []AskChoiceOption{}
	for _, choice := range choices {
		options = append(options, AskChoiceOption{Key: strings.TrimSpace(choice.Key), Label: strings.TrimSpace(choice.Label)})
	}
	return options
}

func (connectorRuntime *ConnectorRuntime) addressedInteraction(interaction *AskInteraction, taskRunID string, requesterPersonID string, turn *inboundTurn) *AskInteraction {
	if interaction == nil {
		return nil
	}
	addressed := *interaction
	addressed.TaskRunID = taskRunID
	addressed.TargetPlatformUserID = connectorRuntime.requesterPlatformUserID(turn, requesterPersonID)
	return &addressed
}

func (connectorRuntime *ConnectorRuntime) requesterPlatformUserID(turn *inboundTurn, requesterPersonID string) string {
	senderID := strings.TrimSpace(turn.event.SenderID)
	if senderID == "" {
		return ""
	}
	personID, isFound := connectorRuntime.identityService.ResolvePersonIDByPlatformAccount(turn.platform, senderID)
	if !isFound || personID != requesterPersonID {
		return ""
	}
	return senderID
}
