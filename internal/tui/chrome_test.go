package tui

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	lipgloss "charm.land/lipgloss/v2"
)

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestTheStatusBarKeepsItsShapeWhenTheConnectionFails(testInstance *testing.T) {
	connected := Model{screen: screenTasks, width: 100}
	disconnected := Model{screen: screenTasks, width: 100, taskRunsError: errors.New("dial tcp 127.0.0.1:8080: connection refused")}

	for name, footer := range map[string]string{"connected": connected.renderFooter(), "disconnected": disconnected.renderFooter()} {
		if lineCount := strings.Count(footer, "\n") + 1; lineCount != 1 {
			testInstance.Fatalf("%s footer takes %d lines; a second line shifts the layout on every poll", name, lineCount)
		}
		if width := lipgloss.Width(footer); width != 100 {
			testInstance.Fatalf("%s footer is %d columns wide, expected the full 100", name, width)
		}
	}
}

func TestTheActiveTabHighlightSurvivesTheDimmedDigit(testInstance *testing.T) {
	activeTab := renderTab("1", "Tasks", true)

	if unhighlighted := textRunsWithoutBackground(activeTab, "48;2;46;125;196"); len(unhighlighted) > 0 {
		testInstance.Fatalf("the active tab highlight breaks after the digit; these runs carry no background: %q in %q", unhighlighted, activeTab)
	}
}

func TestTheTableWindowAlwaysContainsTheSelectedRow(testInstance *testing.T) {
	const rowCount, availableHeight = 40, 15
	for selectedRow := 0; selectedRow < rowCount; selectedRow++ {
		window := tableWindowFor(availableHeight, rowCount, selectedRow)
		if !window.isScrolling() {
			testInstance.Fatalf("40 rows in %d lines should scroll, got %+v", availableHeight, window)
		}
		if window.firstRow < 0 || window.firstRow+window.visibleRows > rowCount {
			testInstance.Fatalf("row %d produced the window %+v, which runs off the list of %d", selectedRow, window, rowCount)
		}
		if selectedRow < window.firstRow || selectedRow >= window.firstRow+window.visibleRows {
			testInstance.Fatalf("row %d is outside its own window %+v", selectedRow, window)
		}
	}
}

func TestATableThatFitsIsNotScrolled(testInstance *testing.T) {
	if window := tableWindowFor(40, 5, 0); window.isScrolling() {
		testInstance.Fatalf("five rows in a 40-line body asked for the window %+v; it should render whole", window)
	}
}

func TestEveryTimelineEntryHasTheSameShape(testInstance *testing.T) {
	entryTime := time.Date(2026, 8, 5, 14, 3, 9, 0, time.UTC)
	entries := []TimelineEntry{
		{Kind: TimelineEntryToolCall, Time: entryTime, ToolName: "shell"},
		{Kind: TimelineEntryToolCall, Time: entryTime, ToolName: "shell", HasResult: true, ResultSummary: "done"},
		{Kind: TimelineEntryToolCall, Time: entryTime, ToolName: "shell", HasResult: true, ResultIsFailure: true, ResultSummary: "exit 1"},
		{Kind: TimelineEntryAgentMessage, Time: entryTime, Message: "reading the ledger"},
		{Kind: TimelineEntryApprovalPending, Time: entryTime, Message: "send the summary"},
		{Kind: TimelineEntryApprovalExecuted, Time: entryTime, ToolName: "message_send"},
		{Time: entryTime, RawEventName: "task.started"},
	}
	shape := regexp.MustCompile(`^14:03:09 \S `)
	for _, entry := range entries {
		plainText := ansiEscape.ReplaceAllString(renderTimelineEntry(entry), "")
		if !shape.MatchString(plainText) {
			testInstance.Fatalf("timeline entry %v does not start with a timestamp and a one-rune symbol: %q", entry.Kind, plainText)
		}
	}
}
