package inboundengagement

import (
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const visibleContextMessageBudget = 8

type decisionState struct {
	Agent                decisionAgent            `json:"agent"`
	Company              decisionCompany          `json:"company"`
	Placement            string                   `json:"placement"`
	Now                  string                   `json:"now,omitempty"`
	Context              []decisionContextMessage `json:"context,omitempty"`
	Messages             []decisionMessage        `json:"messages"`
	StandingDuties       []decisionDuty           `json:"standingDuties,omitempty"`
	ActiveTask           *TaskFacts               `json:"activeTask,omitempty"`
	RecentlyFinishedTask *TaskFacts               `json:"recentlyFinishedTask,omitempty"`
}

type decisionAgent struct {
	Name    string `json:"name"`
	Mention string `json:"mention,omitempty"`
}

type decisionCompany struct {
	Name     string `json:"name,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
}

type decisionContextMessage struct {
	At      string `json:"at,omitempty"`
	Speaker string `json:"speaker,omitempty"`
	Text    string `json:"text,omitempty"`
}

type decisionMessage struct {
	ID                string                               `json:"id"`
	Sender            string                               `json:"sender,omitempty"`
	Handle            string                               `json:"handle,omitempty"`
	BotMentioned      bool                                 `json:"botMentioned"`
	At                string                               `json:"at,omitempty"`
	Text              string                               `json:"text"`
	Attachments       []agentcontract.IntakeAttachmentFact `json:"attachments,omitempty"`
	IsAttachmentsOnly bool                                 `json:"isAttachmentsOnly,omitempty"`
}

type decisionDuty struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func newDecisionState(facts Facts) decisionState {
	state := decisionState{
		Agent:      decisionAgent{Name: facts.AgentIdentity.DisplayName(), Mention: facts.AgentIdentity.MentionExample()},
		Company:    decisionCompany{Name: facts.Company.Name, TimeZone: facts.Company.TimeZone},
		Placement:  placementOf(facts),
		Now:        agentcontract.FormatContextTimestamp(facts.EnvironmentNow, facts.Company.TimeZone),
		Context:    decisionContextMessages(facts),
		Messages:   decisionMessages(facts),
		ActiveTask: facts.OpenTask,
	}
	if facts.OpenTask == nil {
		state.RecentlyFinishedTask = facts.FinishedTask
	}
	if asksAnyDuty(facts) {
		state.StandingDuties = decisionDuties(facts.Duties)
	}
	return state
}

func placementOf(facts Facts) string {
	if isChannel(facts) {
		return "channel"
	}
	return "direct"
}

func decisionContextMessages(facts Facts) []decisionContextMessage {
	messages := facts.VisibleContext.Messages
	if len(messages) > visibleContextMessageBudget {
		messages = messages[len(messages)-visibleContextMessageBudget:]
	}
	contextMessages := make([]decisionContextMessage, 0, len(messages))
	for _, message := range messages {
		contextMessages = append(contextMessages, decisionContextMessage{
			At:      agentcontract.FormatContextTimestamp(message.SentAt, facts.Company.TimeZone),
			Speaker: firstNonEmpty(message.SpeakerCallingName, message.Speaker, message.SpeakerHandle, "unknown"),
			Text:    strings.TrimSpace(message.Text),
		})
	}
	return contextMessages
}

func decisionMessages(facts Facts) []decisionMessage {
	messages := make([]decisionMessage, 0, len(facts.Messages))
	for index, message := range facts.Messages {
		messages = append(messages, decisionMessage{
			ID:                messageKey(index),
			Sender:            strings.TrimSpace(message.SenderName),
			Handle:            strings.TrimSpace(message.SenderHandle),
			BotMentioned:      message.BotMentioned,
			At:                agentcontract.FormatContextTimestamp(message.SentAt, facts.Company.TimeZone),
			Text:              strings.TrimSpace(message.Prompt),
			Attachments:       message.Attachments,
			IsAttachmentsOnly: message.IsAttachmentsOnly,
		})
	}
	return messages
}

func decisionDuties(duties []agentcontract.StandingDuty) []decisionDuty {
	described := make([]decisionDuty, 0, len(duties))
	for _, duty := range duties {
		described = append(described, decisionDuty{Name: duty.Name, Description: duty.Description})
	}
	return described
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmedValue := strings.TrimSpace(value); trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}
