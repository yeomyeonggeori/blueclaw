package tui

import (
	"testing"
	"time"
)

func TestBuildTimelinePairsToolRequestedAndResult(testInstance *testing.T) {
	baseTime := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	taskEvents := []TaskEvent{
		{Name: "tool.file_read.requested", Body: `{"observationID":"obs-1","toolName":"file_read","input":{"path":"a.txt"}}`, CreatedAt: baseTime},
		{Name: "tool.file_read.result", Body: `{"observationID":"obs-1","summary":"read 12 lines"}`, CreatedAt: baseTime.Add(time.Second)},
	}

	timelineEntries := BuildTimeline(taskEvents)

	if len(timelineEntries) != 1 {
		testInstance.Fatalf("expected a single merged entry, got %d: %+v", len(timelineEntries), timelineEntries)
	}
	entry := timelineEntries[0]
	if entry.Kind != TimelineEntryToolCall || entry.ToolName != "file_read" {
		testInstance.Fatalf("unexpected entry: %+v", entry)
	}
	if !entry.HasResult || entry.ResultSummary != "read 12 lines" || entry.ResultIsFailure {
		testInstance.Fatalf("unexpected result merge: %+v", entry)
	}
}

func TestBuildTimelineMarksFailedToolResult(testInstance *testing.T) {
	taskEvents := []TaskEvent{
		{Name: "tool.shell.requested", Body: `{"toolName":"shell"}`, CreatedAt: time.Unix(0, 0)},
		{Name: "tool.shell.result", Body: `{"failure":{"userSafeSummary":"permission denied","code":"operation_failed"}}`, CreatedAt: time.Unix(1, 0)},
	}

	timelineEntries := BuildTimeline(taskEvents)

	if len(timelineEntries) != 1 || !timelineEntries[0].ResultIsFailure {
		testInstance.Fatalf("expected a failed merged entry, got %+v", timelineEntries)
	}
	if timelineEntries[0].ResultSummary != "permission denied" {
		testInstance.Fatalf("unexpected summary: %q", timelineEntries[0].ResultSummary)
	}
}

func TestBuildTimelineKeepsUnmatchedResultAsOwnEntry(testInstance *testing.T) {
	taskEvents := []TaskEvent{
		{Name: "tool.file_read.result", Body: `{"summary":"orphaned result"}`, CreatedAt: time.Unix(0, 0)},
	}

	timelineEntries := BuildTimeline(taskEvents)

	if len(timelineEntries) != 1 || timelineEntries[0].ToolName != "file_read" || !timelineEntries[0].HasResult {
		testInstance.Fatalf("unexpected entries: %+v", timelineEntries)
	}
}

func TestBuildTimelinePairsCallsInFIFOOrderPerToolName(testInstance *testing.T) {
	taskEvents := []TaskEvent{
		{Name: "tool.file_read.requested", Body: `{"toolName":"file_read","input":"a.txt"}`, CreatedAt: time.Unix(0, 0)},
		{Name: "tool.file_read.requested", Body: `{"toolName":"file_read","input":"b.txt"}`, CreatedAt: time.Unix(1, 0)},
		{Name: "tool.file_read.result", Body: `{"summary":"read a"}`, CreatedAt: time.Unix(2, 0)},
		{Name: "tool.file_read.result", Body: `{"summary":"read b"}`, CreatedAt: time.Unix(3, 0)},
	}

	timelineEntries := BuildTimeline(taskEvents)

	if len(timelineEntries) != 2 {
		testInstance.Fatalf("expected two merged entries, got %d: %+v", len(timelineEntries), timelineEntries)
	}
	if timelineEntries[0].RequestedInput != `"a.txt"` || timelineEntries[0].ResultSummary != "read a" {
		testInstance.Fatalf("unexpected first entry: %+v", timelineEntries[0])
	}
	if timelineEntries[1].RequestedInput != `"b.txt"` || timelineEntries[1].ResultSummary != "read b" {
		testInstance.Fatalf("unexpected second entry: %+v", timelineEntries[1])
	}
}

func TestBuildTimelineRendersCheckpointAndApprovalEvents(testInstance *testing.T) {
	taskEvents := []TaskEvent{
		{Name: "agent.checkpoint.sent", Body: `{"toolName":"shell","message":"running the build"}`, CreatedAt: time.Unix(0, 0)},
		{Name: "approval.pending_call", Body: `{"toolName":"message_send","confirmation":"Send this message to the team?"}`, CreatedAt: time.Unix(1, 0)},
		{Name: "approval.executed", Body: `{"toolName":"message_send"}`, CreatedAt: time.Unix(2, 0)},
	}

	timelineEntries := BuildTimeline(taskEvents)

	if len(timelineEntries) != 3 {
		testInstance.Fatalf("expected three entries, got %d: %+v", len(timelineEntries), timelineEntries)
	}
	if timelineEntries[0].Kind != TimelineEntryAgentMessage || timelineEntries[0].Message != "running the build" {
		testInstance.Fatalf("unexpected checkpoint entry: %+v", timelineEntries[0])
	}
	if timelineEntries[1].Kind != TimelineEntryApprovalPending || timelineEntries[1].Message != "Send this message to the team?" {
		testInstance.Fatalf("unexpected approval pending entry: %+v", timelineEntries[1])
	}
	if timelineEntries[2].Kind != TimelineEntryApprovalExecuted || timelineEntries[2].ToolName != "message_send" {
		testInstance.Fatalf("unexpected approval executed entry: %+v", timelineEntries[2])
	}
}

func TestBuildTimelineKeepsUnknownEventsAsOther(testInstance *testing.T) {
	taskEvents := []TaskEvent{
		{Name: "agent.intake", Body: `{"decision":"engage"}`, CreatedAt: time.Unix(0, 0)},
	}

	timelineEntries := BuildTimeline(taskEvents)

	if len(timelineEntries) != 1 || timelineEntries[0].Kind != TimelineEntryOther {
		testInstance.Fatalf("unexpected entries: %+v", timelineEntries)
	}
	if timelineEntries[0].RawEventName != "agent.intake" {
		testInstance.Fatalf("unexpected raw event name: %+v", timelineEntries[0])
	}
}

func TestBuildTimelineOrdersEventsByCreatedAtRegardlessOfInputOrder(testInstance *testing.T) {
	taskEvents := []TaskEvent{
		{Name: "agent.checkpoint.sent", Body: `{"message":"second"}`, CreatedAt: time.Unix(5, 0)},
		{Name: "agent.checkpoint.sent", Body: `{"message":"first"}`, CreatedAt: time.Unix(1, 0)},
	}

	timelineEntries := BuildTimeline(taskEvents)

	if len(timelineEntries) != 2 || timelineEntries[0].Message != "first" || timelineEntries[1].Message != "second" {
		testInstance.Fatalf("unexpected order: %+v", timelineEntries)
	}
}

func TestLatestApprovalQuestionReturnsMostRecentUnresolvedPendingCall(testInstance *testing.T) {
	taskEvents := []TaskEvent{
		{Name: "approval.pending_call", Body: `{"confirmation":"resolved one, ignore"}`, CreatedAt: time.Unix(0, 0)},
		{Name: "approval.executed", Body: `{}`, CreatedAt: time.Unix(1, 0)},
		{Name: "approval.pending_call", Body: `{"confirmation":"delete the report file?"}`, CreatedAt: time.Unix(2, 0)},
	}

	question, hasQuestion := LatestApprovalQuestion(taskEvents)

	if !hasQuestion || question != "delete the report file?" {
		testInstance.Fatalf("unexpected question: %q hasQuestion=%v", question, hasQuestion)
	}
}

func TestLatestApprovalQuestionReturnsFalseWhenNoneOutstanding(testInstance *testing.T) {
	taskEvents := []TaskEvent{
		{Name: "approval.pending_call", Body: `{"confirmation":"resolved"}`, CreatedAt: time.Unix(0, 0)},
		{Name: "approval.executed", Body: `{}`, CreatedAt: time.Unix(1, 0)},
	}

	_, hasQuestion := LatestApprovalQuestion(taskEvents)

	if hasQuestion {
		testInstance.Fatal("expected no outstanding question")
	}
}
