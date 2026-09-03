package tui

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type TimelineEntryKind string

const (
	TimelineEntryToolCall         TimelineEntryKind = "tool_call"
	TimelineEntryAgentMessage     TimelineEntryKind = "agent_message"
	TimelineEntryApprovalPending  TimelineEntryKind = "approval_pending"
	TimelineEntryApprovalExecuted TimelineEntryKind = "approval_executed"
	TimelineEntryOther            TimelineEntryKind = "other"
)

// TimelineEntry is a display-friendly grouping of one or more raw task
// events. Tool calls fold their "requested" and "result" events into a
// single entry so the timeline reads as a sequence of actions rather than a
// raw event log.
type TimelineEntry struct {
	Kind            TimelineEntryKind
	Time            time.Time
	RawEventName    string
	ToolName        string
	RequestedInput  string
	ResultSummary   string
	ResultIsFailure bool
	HasResult       bool
	Message         string
}

type toolRequestedEventBody struct {
	ObservationID string          `json:"observationID"`
	ToolName      string          `json:"toolName"`
	Input         json.RawMessage `json:"input"`
}

type toolResultEventBody struct {
	ObservationID string `json:"observationID"`
	Summary       string `json:"summary"`
	Failure       *struct {
		UserSafeSummary string `json:"userSafeSummary"`
		Code            string `json:"code"`
	} `json:"failure"`
	Output struct {
		Content string `json:"content"`
	} `json:"output"`
}

type approvalExecutedEventBody struct {
	ToolName string `json:"toolName"`
}

type checkpointSentEventBody struct {
	ToolName string `json:"toolName"`
	Message  string `json:"message"`
}

// BuildTimeline converts a task run's raw event ledger into an ordered,
// human-readable timeline. It is pure and safe to unit test without a
// terminal or a running blueclaw process.
func BuildTimeline(taskEvents []TaskEvent) []TimelineEntry {
	orderedEvents := append([]TaskEvent{}, taskEvents...)
	sort.SliceStable(orderedEvents, func(leftIndex int, rightIndex int) bool {
		return orderedEvents[leftIndex].CreatedAt.Before(orderedEvents[rightIndex].CreatedAt)
	})

	timelineEntries := []TimelineEntry{}
	pendingCallIndexByToolName := map[string][]int{}

	for _, taskEvent := range orderedEvents {
		toolName, phase, isToolEvent := parseToolEventName(taskEvent.Name)
		switch {
		case isToolEvent && phase == "requested":
			requestedBody := decodeEventBody[toolRequestedEventBody](taskEvent.Body)
			timelineEntries = append(timelineEntries, TimelineEntry{
				Kind:           TimelineEntryToolCall,
				Time:           taskEvent.CreatedAt,
				RawEventName:   taskEvent.Name,
				ToolName:       firstNonEmpty(requestedBody.ToolName, toolName),
				RequestedInput: string(requestedBody.Input),
			})
			pendingCallIndexByToolName[toolName] = append(pendingCallIndexByToolName[toolName], len(timelineEntries)-1)

		case isToolEvent && phase == "result":
			resultBody := decodeEventBody[toolResultEventBody](taskEvent.Body)
			entryIndex, hasPendingCall := popPendingCallIndex(pendingCallIndexByToolName, toolName)
			if !hasPendingCall {
				timelineEntries = append(timelineEntries, TimelineEntry{Kind: TimelineEntryToolCall, Time: taskEvent.CreatedAt, RawEventName: taskEvent.Name, ToolName: toolName})
				entryIndex = len(timelineEntries) - 1
			}
			timelineEntries[entryIndex].HasResult = true
			timelineEntries[entryIndex].ResultSummary = toolResultSummary(resultBody)
			timelineEntries[entryIndex].ResultIsFailure = resultBody.Failure != nil

		case taskEvent.Name == "agent.checkpoint.sent":
			checkpointBody := decodeEventBody[checkpointSentEventBody](taskEvent.Body)
			timelineEntries = append(timelineEntries, TimelineEntry{
				Kind:         TimelineEntryAgentMessage,
				Time:         taskEvent.CreatedAt,
				RawEventName: taskEvent.Name,
				ToolName:     checkpointBody.ToolName,
				Message:      checkpointBody.Message,
			})

		case taskEvent.Name == "approval.pending_call":
			pendingBody := decodeEventBody[agentcontract.HeldCall](taskEvent.Body)
			timelineEntries = append(timelineEntries, TimelineEntry{
				Kind:         TimelineEntryApprovalPending,
				Time:         taskEvent.CreatedAt,
				RawEventName: taskEvent.Name,
				ToolName:     pendingBody.ToolName,
				Message:      pendingBody.Confirmation,
			})

		case taskEvent.Name == "approval.executed":
			executedBody := decodeEventBody[approvalExecutedEventBody](taskEvent.Body)
			timelineEntries = append(timelineEntries, TimelineEntry{
				Kind:         TimelineEntryApprovalExecuted,
				Time:         taskEvent.CreatedAt,
				RawEventName: taskEvent.Name,
				ToolName:     executedBody.ToolName,
			})

		default:
			timelineEntries = append(timelineEntries, TimelineEntry{
				Kind:         TimelineEntryOther,
				Time:         taskEvent.CreatedAt,
				RawEventName: taskEvent.Name,
				Message:      taskEvent.Body,
			})
		}
	}

	return timelineEntries
}

// LatestApprovalQuestion returns the confirmation wording from the most
// recent unresolved approval.pending_call event, if any.
func LatestApprovalQuestion(taskEvents []TaskEvent) (string, bool) {
	timelineEntries := BuildTimeline(taskEvents)
	for entryIndex := len(timelineEntries) - 1; entryIndex >= 0; entryIndex-- {
		entry := timelineEntries[entryIndex]
		if entry.Kind == TimelineEntryApprovalExecuted {
			return "", false
		}
		if entry.Kind == TimelineEntryApprovalPending {
			return entry.Message, true
		}
	}
	return "", false
}

func parseToolEventName(eventName string) (toolName string, phase string, isToolEvent bool) {
	if !strings.HasPrefix(eventName, "tool.") {
		return "", "", false
	}
	remainder := strings.TrimPrefix(eventName, "tool.")
	switch {
	case strings.HasSuffix(remainder, ".requested"):
		return strings.TrimSuffix(remainder, ".requested"), "requested", true
	case strings.HasSuffix(remainder, ".result"):
		return strings.TrimSuffix(remainder, ".result"), "result", true
	default:
		return "", "", false
	}
}

func popPendingCallIndex(pendingCallIndexByToolName map[string][]int, toolName string) (int, bool) {
	indices := pendingCallIndexByToolName[toolName]
	if len(indices) == 0 {
		return 0, false
	}
	entryIndex := indices[0]
	pendingCallIndexByToolName[toolName] = indices[1:]
	return entryIndex, true
}

func toolResultSummary(resultBody toolResultEventBody) string {
	if resultBody.Failure != nil && resultBody.Failure.UserSafeSummary != "" {
		return resultBody.Failure.UserSafeSummary
	}
	if resultBody.Summary != "" {
		return resultBody.Summary
	}
	return resultBody.Output.Content
}

func decodeEventBody[bodyType any](rawBody string) bodyType {
	var decodedBody bodyType
	json.Unmarshal([]byte(rawBody), &decodedBody)
	return decodedBody
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
