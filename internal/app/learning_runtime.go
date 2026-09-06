package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/learning"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func newLearningCoordinator(runtimeConfiguration config.RuntimeConfiguration, store *learning.Store, languageModel model.LanguageModelProvider, logger *slog.Logger) (*learning.Coordinator, error) {
	if store == nil || strings.TrimSpace(runtimeConfiguration.Terminal.WorkspaceRootPath) == "" {
		return nil, nil
	}
	coordinator, errorValue := learning.NewCoordinator(
		runtimeConfiguration.Terminal.WorkspaceRootPath,
		store,
		learning.Reviewer{Model: languageModel},
		nil,
	)
	if coordinator != nil && logger != nil {
		coordinator.Report = func(reviewError error) {
			logger.Error("learning.review_failed", "error", reviewError.Error())
		}
	}
	return coordinator, errorValue
}

func learningTaskObserver(coordinator *learning.Coordinator, taskRunService *task.TaskRunService) func(agentruntime.TaskLaunchRequest) func(agentruntime.TaskLaunchResult, error) {
	if coordinator == nil || taskRunService == nil {
		return func(agentruntime.TaskLaunchRequest) func(agentruntime.TaskLaunchResult, error) {
			return func(agentruntime.TaskLaunchResult, error) {}
		}
	}
	return func(request agentruntime.TaskLaunchRequest) func(agentruntime.TaskLaunchResult, error) {
		if !coordinator.IsEnabled() {
			return func(agentruntime.TaskLaunchResult, error) {}
		}
		finishTask := coordinator.BeginTask()
		return func(result agentruntime.TaskLaunchResult, launchError error) {
			defer finishTask()
			experience, isComplete := learningExperienceFromTask(request, result, taskRunService, launchError)
			if isComplete {
				if errorValue := coordinator.Observe(experience); errorValue != nil && coordinator.Report != nil {
					coordinator.Report(errorValue)
				}
			}
		}
	}
}

type learningTaskOutcome struct {
	Status        agentcontract.TaskStatus `json:"status"`
	Result        learningText             `json:"result"`
	FailureReason learningText             `json:"failureReason"`
	FinishMessage learningText             `json:"finishMessage"`
	ToolNames     []string                 `json:"toolNames,omitempty"`
	Events        []learningEvidenceEvent  `json:"events"`
	TotalEvents   int                      `json:"totalEventCount"`
	OmittedEvents int                      `json:"omittedEventCount"`
	Incomplete    bool                     `json:"incompleteEvidence"`
	Error         string                   `json:"error,omitempty"`
}

type learningText struct {
	Value      string `json:"value,omitempty"`
	ByteLength int    `json:"byteLength"`
	SHA256     string `json:"sha256"`
	Omitted    bool   `json:"omitted"`
}

type learningEvidenceEvent struct {
	EventID     string    `json:"eventID"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"createdAt"`
	ByteLength  int       `json:"byteLength"`
	SHA256      string    `json:"sha256"`
	Body        string    `json:"body,omitempty"`
	BodyOmitted bool      `json:"bodyOmitted"`
}

const (
	maximumLearningEvents     = 64
	maximumLearningBody       = 2048
	maximumLearningBodyBudget = 4096
)

func learningExperienceOutcome(taskRunService *task.TaskRunService, result agentruntime.TaskLaunchResult, launchError error) json.RawMessage {
	taskRun := result.TurnResult.TaskRun
	outcome := learningTaskOutcome{
		Status:        taskRun.Status,
		Result:        learningTextOf(taskRun.Result, maximumLearningBody),
		FailureReason: learningTextOf(taskRun.FailureReason, maximumLearningBody),
		FinishMessage: learningTextOf(result.TurnResult.FinishMessage, maximumLearningBody),
		ToolNames:     append([]string{}, result.ToolNames...),
	}
	allEvents := taskRunService.ListTaskEvent(taskRun.TaskRunID)
	outcome.TotalEvents = len(allEvents)
	outcome.Events, outcome.OmittedEvents = learningEvidenceEvents(allEvents)
	outcome.Incomplete = outcome.OmittedEvents > 0 || outcome.Result.Omitted || outcome.FailureReason.Omitted || outcome.FinishMessage.Omitted
	if launchError != nil {
		outcome.Error = launchError.Error()
	}
	document, errorValue := json.Marshal(outcome)
	if errorValue != nil {
		return json.RawMessage(`{"error":"could not serialize task outcome"}`)
	}
	return document
}

func learningTextOf(value string, maximumBytes int) learningText {
	document := []byte(value)
	digest := sha256.Sum256(document)
	text := learningText{ByteLength: len(document), SHA256: fmt.Sprintf("%x", digest)}
	if len(document) > maximumBytes {
		text.Omitted = true
		return text
	}
	text.Value = value
	return text
}

func learningEvidenceEvents(events []agentcontract.TaskEvent) ([]learningEvidenceEvent, int) {
	totalOmitted := 0
	if len(events) > maximumLearningEvents {
		totalOmitted += len(events) - maximumLearningEvents
		events = events[len(events)-maximumLearningEvents:]
	}
	bounded := make([]learningEvidenceEvent, len(events))
	bodyBudget := maximumLearningBodyBudget
	for index, event := range events {
		body := learningTextOf(event.Body, maximumLearningBody)
		if !body.Omitted && len(body.Value) > bodyBudget {
			body.Value = ""
			body.Omitted = true
		}
		if !body.Omitted {
			bodyBudget -= len(body.Value)
		}
		bounded[index] = learningEvidenceEvent{
			EventID: event.TaskEventID, Name: event.Name, CreatedAt: event.CreatedAt,
			ByteLength: body.ByteLength, SHA256: body.SHA256, Body: body.Value, BodyOmitted: body.Omitted,
		}
		if body.Omitted {
			totalOmitted++
		}
	}
	return bounded, totalOmitted
}

func learningExperienceFromTask(request agentruntime.TaskLaunchRequest, result agentruntime.TaskLaunchResult, taskRunService *task.TaskRunService, launchError error) (learning.Experience, bool) {
	taskRun := result.TurnResult.TaskRun
	if strings.TrimSpace(taskRun.TaskRunID) == "" || strings.TrimSpace(request.RequesterPersonID) == "" || !learningTaskRunEnded(taskRun.Status) {
		return learning.Experience{}, false
	}
	return learning.Experience{
		TaskID:     taskRun.TaskRunID,
		Audience:   "person:" + strings.TrimSpace(request.RequesterPersonID),
		RecordedAt: time.Now().UTC(),
		Request:    request.Prompt,
		Outcome:    learningExperienceOutcome(taskRunService, result, launchError),
		Tools:      append([]string{}, result.ToolNames...),
	}, true
}

func learningTaskRunEnded(status agentcontract.TaskStatus) bool {
	return status == agentcontract.TaskStatusCompleted || status == agentcontract.TaskStatusFailed || status == agentcontract.TaskStatusCancelled
}
