package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
	"github.com/yeomyeonggeori/bluememo"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

const (
	ExtractionContextEventName = "memory.extraction_context"
	extractionTimeout          = 2 * time.Minute
)

type ExtractionContext struct {
	RequesterName  string `json:"requesterName,omitempty"`
	ActiveCircleID string `json:"activeCircleID,omitempty"`
	Platform       string `json:"platform,omitempty"`
}

type TaskRunReader interface {
	FindTaskRun(taskRunID string) (agentcontract.TaskRun, bool)
	ListTaskEvent(taskRunID string) []agentcontract.TaskEvent
	AppendTaskEvent(taskRunID string, name string, body string)
}

type TaskStepReader interface {
	ListTaskStep(taskRunID string) []taskstate.TaskStep
}

type PersonAccessResolver interface {
	ResolvePersonAccess(personID string) policy.PersonAccess
	ContainedCircles() map[string][]string
}

// Transcript is what a finished task run reads like. The memory store takes a
// note of what happened, so the shape of a task run is the host's to render.
type Transcript struct {
	Prompt        string
	Result        string
	Outcome       string
	FailureReason string
	Steps         []TranscriptStep
}

type TranscriptStep struct {
	Instruction string
	Status      string
	Output      string
}

func TaskTranscript(taskRun agentcontract.TaskRun, steps []taskstate.TaskStep) Transcript {
	transcript := Transcript{
		Prompt:        taskRun.Prompt,
		Result:        taskRun.Result,
		Outcome:       string(taskRun.Status),
		FailureReason: taskRun.FailureReason,
	}
	for _, step := range steps {
		transcript.Steps = append(transcript.Steps, TranscriptStep{Instruction: step.Instruction, Status: string(step.Status), Output: step.Output})
	}
	return transcript
}

func RenderTranscript(transcript Transcript) string {
	lines := []string{}
	appendLine := func(label string, value string) {
		if strings.TrimSpace(value) != "" {
			lines = append(lines, label+": "+strings.TrimSpace(value))
		}
	}
	appendLine("Asked", transcript.Prompt)
	for _, step := range transcript.Steps {
		appendLine("Did", step.Instruction)
		appendLine("Which gave", step.Output)
	}
	appendLine("Answered", transcript.Result)
	appendLine("Ended", transcript.Outcome)
	appendLine("Because", transcript.FailureReason)
	return strings.Join(lines, "\n")
}

// TaskRunTransitionObserver takes a note of a task run that has just ended.
// Settling it asks a model, so the note is taken off the transition rather
// than on it.
type TaskRunTransitionObserver struct {
	Stores   *Stores
	TaskRuns TaskRunReader
	Steps    TaskStepReader
	Access   PersonAccessResolver
	Logger   *slog.Logger
}

func (observer TaskRunTransitionObserver) Observe(taskRun agentcontract.TaskRun) {
	switch taskRun.Status {
	case agentcontract.TaskStatusCompleted, agentcontract.TaskStatusFailed, agentcontract.TaskStatusCancelled:
	default:
		return
	}
	if observer.Stores == nil || observer.TaskRuns == nil {
		return
	}
	if strings.TrimSpace(taskRun.RequesterPersonID) == "" || strings.TrimSpace(taskRun.Prompt) == "" {
		return
	}
	go observer.remember(taskRun)
}

func (observer TaskRunTransitionObserver) remember(taskRun agentcontract.TaskRun) {
	ctx, cancel := context.WithTimeout(context.Background(), extractionTimeout)
	defer cancel()
	events := observer.TaskRuns.ListTaskEvent(taskRun.TaskRunID)
	extractionContext, _ := findExtractionContext(events)
	stack := observer.stackFor(taskRun.RequesterPersonID, extractionContext.ActiveCircleID)
	scope := stack[0]
	note := bluememo.Note{
		GroupID:     taskRun.TaskRunID,
		Body:        RenderTranscript(TaskTranscript(taskRun, observer.taskSteps(taskRun.TaskRunID))),
		SpeakerName: extractionContext.RequesterName,
	}
	report, errorValue := observer.Stores.Remember(ctx, stack, note, answersOf(events))
	if errorValue != nil {
		observer.logger().Warn("memory.extraction.failed", "taskRunID", taskRun.TaskRunID, "scope", scope.Kind, "error", errorValue.Error())
		observer.TaskRuns.AppendTaskEvent(taskRun.TaskRunID, "memory.extraction_failed", marshalEventBody(map[string]any{
			"scope": scope.Kind,
			"error": errorValue.Error(),
		}))
		return
	}
	observer.TaskRuns.AppendTaskEvent(taskRun.TaskRunID, "memory.extraction_completed", marshalEventBody(map[string]any{
		"scope":      scope.Kind,
		"scopeID":    scope.ID,
		"inserted":   report.Inserted,
		"extended":   report.Extended,
		"superseded": report.Superseded,
		"reinforced": report.Reinforced,
	}))
}

func (observer TaskRunTransitionObserver) stackFor(personID string, activeCircleID string) []Scope {
	target := ScopeToRemember(personID, activeCircleID)
	if observer.Access == nil {
		return []Scope{target}
	}
	return StackToRemember(target, ScopesToSearch(observer.Access.ResolvePersonAccess(personID), observer.Access.ContainedCircles()))
}

func (observer TaskRunTransitionObserver) taskSteps(taskRunID string) []taskstate.TaskStep {
	if observer.Steps == nil {
		return nil
	}
	return observer.Steps.ListTaskStep(taskRunID)
}

func (observer TaskRunTransitionObserver) logger() *slog.Logger {
	if observer.Logger != nil {
		return observer.Logger
	}
	return slog.Default()
}

func findExtractionContext(events []agentcontract.TaskEvent) (ExtractionContext, bool) {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Name != ExtractionContextEventName {
			continue
		}
		var extractionContext ExtractionContext
		if errorValue := json.Unmarshal([]byte(events[index].Body), &extractionContext); errorValue != nil {
			return ExtractionContext{}, false
		}
		return extractionContext, true
	}
	return ExtractionContext{}, false
}

func marshalEventBody(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(document)
}
