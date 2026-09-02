package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/taskstate"

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
}

type ExtractJobHandler struct {
	Ingester Ingester
	TaskRuns TaskRunReader
	Steps    TaskStepReader
	Access   PersonAccessResolver
}

func (handler ExtractJobHandler) Handle(ctx context.Context, job Job) error {
	if handler.TaskRuns == nil || handler.Access == nil {
		return errors.New("memory extraction has no task run reader or access resolver")
	}
	taskRun, isFound := handler.TaskRuns.FindTaskRun(job.SubjectID)
	if !isFound {
		return TerminalJobError{Cause: fmt.Errorf("task run %s no longer exists", job.SubjectID)}
	}
	if strings.TrimSpace(taskRun.RequesterPersonID) == "" {
		return TerminalJobError{Cause: fmt.Errorf("task run %s has no requester", job.SubjectID)}
	}
	extractionContext, hasContext := findExtractionContext(handler.TaskRuns.ListTaskEvent(taskRun.TaskRunID))
	personAccess := handler.Access.ResolvePersonAccess(taskRun.RequesterPersonID)
	request := IngestRequest{
		Episode: Episode{
			EpisodeID:         taskstate.NewIdentifier(),
			SourceKind:        EpisodeSourceKindTaskRun,
			SourceID:          taskRun.TaskRunID,
			RequesterPersonID: taskRun.RequesterPersonID,
			ConversationID:    taskRun.OriginConversationID,
			Content:           RenderTaskTranscript(taskRun, handler.steps(taskRun.TaskRunID)),
			OccurredAt:        firstNonZeroTime(taskRun.UpdatedAt, taskRun.CreatedAt, handler.Ingester.now()),
		},
		Reader:        ReaderFromPersonAccess(personAccess),
		RequesterName: extractionContext.RequesterName,
		Label:         SecurityLabel{SecurityLevelRank: personAccess.SecurityLevelRank, RequiredClasses: personAccess.GrantedClasses},
	}
	if hasContext {
		request.ActiveCircleID = extractionContext.ActiveCircleID
		request.Label = SecurityLabel{SecurityLevelRank: extractionContext.SecurityLevelRank, RequiredClasses: extractionContext.RequiredClasses}
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

func (handler ExtractJobHandler) recordFailure(taskRunID string, job Job, errorValue error) {
	var terminalError TerminalJobError
	isTerminal := errors.As(errorValue, &terminalError)
	if !isTerminal && job.Attempts < DefaultJobMaxAttempts {
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

type ProfileJobHandler struct {
	Builder ProfileBuilder
}

func (handler ProfileJobHandler) Handle(ctx context.Context, job Job) error {
	_, errorValue := handler.Builder.Rebuild(ctx, job.SubjectID)
	return errorValue
}

type TaskRunTransitionObserver struct {
	Store Store
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
		observer.Store.logger().Warn("memory.extraction.enqueue_failed", "taskRunID", taskRun.TaskRunID, "error", errorValue.Error())
		return
	}
	if isCreated {
		observer.Store.logger().Info("memory.extraction.queued", "taskRunID", taskRun.TaskRunID, "jobID", job.JobID)
	}
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
