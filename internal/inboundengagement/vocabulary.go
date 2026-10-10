package inboundengagement

import (
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

type Message struct {
	MessageID         string
	Prompt            string
	SenderName        string
	SenderHandle      string
	BotMentioned      bool
	SentAt            time.Time
	InputParts        []agentcontract.AgentPart
	Attachments       []agentcontract.IntakeAttachmentFact
	IsAttachmentsOnly bool
}

type AddressingTarget string

const (
	AddressingTargetBot     AddressingTarget = "bot"
	AddressingTargetHuman   AddressingTarget = "human"
	AddressingTargetAnyone  AddressingTarget = "anyone"
	AddressingTargetNone    AddressingTarget = "none"
	AddressingTargetUnclear AddressingTarget = "unclear"
)

type AddressingDecision struct {
	Target         AddressingTarget
	ShouldRespond  bool
	Work           agentcontract.Work
	ReactionEmoji  string
	DutyMatch      bool
	DutyName       string
	DutyConfidence float64
}

type BusyRoute string

const (
	BusyRouteStatus    BusyRoute = "status"
	BusyRouteSteer     BusyRoute = "steer"
	BusyRouteReplace   BusyRoute = "replace"
	BusyRouteCancel    BusyRoute = "cancel"
	BusyRouteNewTask   BusyRoute = "new_task"
	BusyRouteUnrelated BusyRoute = "unrelated"
)

var BusyRouteNames = []string{
	string(BusyRouteStatus), string(BusyRouteSteer), string(BusyRouteReplace),
	string(BusyRouteCancel), string(BusyRouteNewTask), string(BusyRouteUnrelated),
}

const (
	QuestionTarget              = "target"
	QuestionShouldRespond       = "shouldRespond"
	QuestionWork                = agentcontract.IntakeQuestionWork
	QuestionReaction            = "reaction"
	QuestionReactionEmoji       = "reactionEmoji"
	QuestionDuty                = "duty"
	QuestionRelatesToActiveTask = "relatesToActiveTask"
	QuestionBusyRoute           = "busyRoute"

	ReactionOptionNone  = "none"
	ReactionOptionReact = "react"
	DutyOptionNone      = "none"
)

type StandingDuty struct {
	Name        string
	Description string
	Instruction string
	ToolNames   []string
}

var standingDuties = []StandingDuty{
	{
		Name:        "calendar_upkeep",
		Description: "a specific meeting, deadline, or scheduled event that should be created or updated as a calendar event right now",
		Instruction: "Record the concrete meeting, deadline, or scheduled event the overheard message states as a calendar entry. List the existing entries around that date first and update the matching one instead of creating a duplicate. When the message states nothing concrete enough to put on a calendar, send a final reply without changing anything.",
		ToolNames:   []string{"event_list", "event_add", "event_update", "conversation_history", "memory_search"},
	},
	{
		Name:        "team_flow_update",
		Description: "a specific work task assigned to a person that should be added, or whose status or details should be updated or completed right now",
		Instruction: "Record the concrete work task the overheard message assigns, or update the existing task whose status or details it changes. List the existing tasks first and update the matching one instead of creating a duplicate. When the message assigns nothing concrete enough to track, send a final reply without changing anything.",
		ToolNames:   []string{"task_list", "task_add", "task_update", "conversation_history", "memory_search"},
	},
}

func StandingDuties() []StandingDuty {
	return append([]StandingDuty{}, standingDuties...)
}

func StandingDutyByName(dutyName string) (StandingDuty, bool) {
	trimmedDutyName := strings.TrimSpace(dutyName)
	for _, duty := range standingDuties {
		if duty.Name == trimmedDutyName {
			return duty, true
		}
	}
	return StandingDuty{}, false
}

type AmbientDutyContext struct {
	IsMatch    bool    `json:"isMatch"`
	Name       string  `json:"name,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

func (context AmbientDutyContext) Normalized() AmbientDutyContext {
	name := strings.TrimSpace(context.Name)
	if !context.IsMatch || name == "" {
		return AmbientDutyContext{}
	}
	if _, isKnownDuty := StandingDutyByName(name); !isKnownDuty {
		return AmbientDutyContext{}
	}
	return AmbientDutyContext{IsMatch: true, Name: name, Confidence: min(max(context.Confidence, 0), 1)}
}

const ambientDutyContextHeading = "Ambient duty context"

func AmbientDutyInstructionPrompt(duty StandingDuty, overheardMessage string, senderName string) string {
	return strings.Join([]string{
		ambientDutyContextHeading + " (" + duty.Name + ")",
		duty.Instruction,
		"",
		ambientDutyOverheardHeading(senderName),
		strings.TrimSpace(overheardMessage),
	}, "\n")
}

func ambientDutyOverheardHeading(senderName string) string {
	trimmedSenderName := strings.TrimSpace(senderName)
	if trimmedSenderName == "" {
		return "Overheard message:"
	}
	return "Overheard message from " + trimmedSenderName + ":"
}
