package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/taskstate"
	"github.com/yeomyeonggeori/bluememo"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

const ExtractionContextEventName = "memory.extraction_context"

type ExtractionContext struct {
	RequesterName     string   `json:"requesterName,omitempty"`
	ActiveCircleID    string   `json:"activeCircleID,omitempty"`
	SecurityLevelRank int      `json:"securityLevelRank"`
	RequiredClasses   []string `json:"requiredClasses"`
	Platform          string   `json:"platform,omitempty"`
}

type TaskRunReader interface {
	FindTaskRun(taskRunID string) (taskstate.TaskRun, bool)
	ListTaskEvent(taskRunID string) []taskstate.TaskEvent
	AppendTaskEvent(taskRunID string, name string, body string)
}

type TaskStepReader interface {
	ListTaskStep(taskRunID string) []taskstate.TaskStep
}

type PersonAccessResolver interface {
	ResolvePersonAccess(personID string) policy.PersonAccess
	ContainedCircles() map[string][]string
}

type ExtractJobHandler struct {
	Ingester bluememo.Ingester
	TaskRuns TaskRunReader
	Steps    TaskStepReader
	Access   PersonAccessResolver
}

func (handler ExtractJobHandler) Handle(ctx context.Context, job bluememo.Job) error {
	if handler.TaskRuns == nil || handler.Access == nil {
		return errors.New("memory extraction has no task run reader or access resolver")
	}
	taskRun, isFound := handler.TaskRuns.FindTaskRun(job.SubjectID)
	if !isFound {
		return bluememo.TerminalJobError{Cause: fmt.Errorf("task run %s no longer exists", job.SubjectID)}
	}
	if strings.TrimSpace(taskRun.RequesterPersonID) == "" {
		return bluememo.TerminalJobError{Cause: fmt.Errorf("task run %s has no requester", job.SubjectID)}
	}
	extractionContext, hasContext := findExtractionContext(handler.TaskRuns.ListTaskEvent(taskRun.TaskRunID))
	personAccess := handler.Access.ResolvePersonAccess(taskRun.RequesterPersonID)
	request := bluememo.IngestRequest{
		Episode: bluememo.Episode{
			EpisodeID:         bluememo.NewIdentifier(),
			SourceKind:        bluememo.EpisodeSourceKindTaskRun,
			SourceID:          taskRun.TaskRunID,
			RequesterPersonID: taskRun.RequesterPersonID,
			ConversationID:    taskRun.OriginConversationID,
			Content:           bluememo.RenderTranscript(TaskTranscript(taskRun, handler.steps(taskRun.TaskRunID))),
			OccurredAt:        firstNonZeroTime(taskRun.UpdatedAt, taskRun.CreatedAt, time.Now().UTC()),
		},
		Reader:        ReaderForAccess(personAccess, handler.Access.ContainedCircles()),
		RequesterName: extractionContext.RequesterName,
		Label:         LabelForAccess(personAccess),
	}
	if hasContext {
		request.ActiveCircleID = extractionContext.ActiveCircleID
		request.Label = bluememo.SecurityLabel{SecurityLevelRank: extractionContext.SecurityLevelRank, RequiredClasses: extractionContext.RequiredClasses}
	}
	result, errorValue := handler.Ingester.Ingest(ctx, request)
	if errorValue != nil {
		handler.recordFailure(taskRun.TaskRunID, job, errorValue)
		return errorValue
	}
	handler.TaskRuns.AppendTaskEvent(taskRun.TaskRunID, "memory.extraction_completed", marshalEventBody(map[string]any{
		"jobID":          job.JobID,
		"episodeID":      result.EpisodeID,
		"factCount":      len(result.Facts),
		"supersededIDs":  result.SupersededFactIDs,
		"reinforcedIDs":  result.ReinforcedFactIDs,
		"candidateCount": result.CandidateCount,
	}))
	return nil
}

func (handler ExtractJobHandler) recordFailure(taskRunID string, job bluememo.Job, errorValue error) {
	var terminalError bluememo.TerminalJobError
	isTerminal := errors.As(errorValue, &terminalError)
	if !isTerminal && job.Attempts < bluememo.DefaultJobMaxAttempts {
		return
	}
	handler.TaskRuns.AppendTaskEvent(taskRunID, "memory.extraction_failed", marshalEventBody(map[string]any{
		"jobID":    job.JobID,
		"attempts": job.Attempts,
		"terminal": isTerminal,
		"error":    errorValue.Error(),
	}))
}

func (handler ExtractJobHandler) steps(taskRunID string) []taskstate.TaskStep {
	if handler.Steps == nil {
		return nil
	}
	return handler.Steps.ListTaskStep(taskRunID)
}

func TaskTranscript(taskRun taskstate.TaskRun, steps []taskstate.TaskStep) bluememo.Transcript {
	transcript := bluememo.Transcript{
		Prompt:        taskRun.Prompt,
		Result:        taskRun.Result,
		Outcome:       string(taskRun.Status),
		FailureReason: taskRun.FailureReason,
	}
	for _, step := range steps {
		transcript.Steps = append(transcript.Steps, bluememo.TranscriptStep{Instruction: step.Instruction, Status: string(step.Status), Output: step.Output})
	}
	return transcript
}

func findExtractionContext(events []taskstate.TaskEvent) (ExtractionContext, bool) {
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

type TaskRunTransitionObserver struct {
	Store  bluememo.Store
	Logger *slog.Logger
}

func (observer TaskRunTransitionObserver) Observe(taskRun taskstate.TaskRun) {
	switch taskRun.Status {
	case taskstate.TaskStatusCompleted, taskstate.TaskStatusFailed, taskstate.TaskStatusCancelled:
	default:
		return
	}
	if strings.TrimSpace(taskRun.RequesterPersonID) == "" || strings.TrimSpace(taskRun.Prompt) == "" {
		return
	}
	job, isCreated, errorValue := observer.Store.EnqueueExtraction(context.Background(), taskRun.TaskRunID)
	if errorValue != nil {
		observer.logger().Warn("memory.extraction.enqueue_failed", "taskRunID", taskRun.TaskRunID, "error", errorValue.Error())
		return
	}
	if isCreated {
		observer.logger().Info("memory.extraction.queued", "taskRunID", taskRun.TaskRunID, "jobID", job.JobID)
	}
}

func (observer TaskRunTransitionObserver) logger() *slog.Logger {
	if observer.Logger != nil {
		return observer.Logger
	}
	return slog.Default()
}

func marshalEventBody(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(document)
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value.UTC()
		}
	}
	return time.Time{}
}
