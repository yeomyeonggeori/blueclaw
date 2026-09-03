package task

import (
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

type (
	ErrIllegalTransition           = taskstate.ErrIllegalTransition
	InterruptedTaskResumeSelection = taskstate.InterruptedTaskResumeSelection
	RawTurnEvent                   = taskstate.RawTurnEvent
	TaskArtifact                   = taskstate.TaskArtifact
	TaskArtifactRepository         = taskstate.TaskArtifactRepository
	TaskArtifactService            = taskstate.TaskArtifactService
	TaskArtifactStore              = taskstate.TaskArtifactStore
	TaskAttempt                    = agentcontract.TaskAttempt
	TaskAttemptStatus              = agentcontract.TaskAttemptStatus
	TaskEvent                      = agentcontract.TaskEvent
	TaskEventRepository            = taskstate.TaskEventRepository
	TaskEventService               = taskstate.TaskEventService
	TaskRun                        = agentcontract.TaskRun
	TaskRunCancelRequest           = taskstate.TaskRunCancelRequest
	TaskRunOrigin                  = taskstate.TaskRunOrigin
	TaskRunRepository              = taskstate.TaskRunRepository
	TaskRunService                 = taskstate.TaskRunService
	TaskRunStore                   = taskstate.TaskRunStore
	TaskRunTransition              = agentcontract.TaskRunTransition
	TaskSchedule                   = taskstate.TaskSchedule
	TaskScheduleExecutionMode      = taskstate.TaskScheduleExecutionMode
	TaskScheduleKind               = taskstate.TaskScheduleKind
	TaskSession                    = taskstate.TaskSession
	TaskStatus                     = agentcontract.TaskStatus
	TaskStep                       = taskstate.TaskStep
	TaskStepRepository             = taskstate.TaskStepRepository
	TaskStepService                = taskstate.TaskStepService
	TaskStepStore                  = taskstate.TaskStepStore
	TaskWaitToken                  = taskstate.TaskWaitToken
)

const (
	TaskAttemptStatusCancelled         = agentcontract.TaskAttemptStatusCancelled
	TaskAttemptStatusCompleted         = agentcontract.TaskAttemptStatusCompleted
	TaskAttemptStatusFailed            = agentcontract.TaskAttemptStatusFailed
	TaskAttemptStatusInterrupted       = agentcontract.TaskAttemptStatusInterrupted
	TaskAttemptStatusRunning           = agentcontract.TaskAttemptStatusRunning
	TaskAttemptStatusStarting          = agentcontract.TaskAttemptStatusStarting
	TaskInterruptReasonPlannedShutdown = agentcontract.TaskInterruptReasonPlannedShutdown
	TaskInterruptReasonRuntimeRestart  = agentcontract.TaskInterruptReasonRuntimeRestart
	TaskScheduleExecutionModeAgent     = taskstate.TaskScheduleExecutionModeAgent
	TaskScheduleExecutionModeMessage   = taskstate.TaskScheduleExecutionModeMessage
	TaskScheduleKindCron               = taskstate.TaskScheduleKindCron
	TaskScheduleKindInterval           = taskstate.TaskScheduleKindInterval
	TaskScheduleKindOnce               = taskstate.TaskScheduleKindOnce
	TaskStatusBlocked                  = agentcontract.TaskStatusBlocked
	TaskStatusCancelled                = agentcontract.TaskStatusCancelled
	TaskStatusCompleted                = agentcontract.TaskStatusCompleted
	TaskStatusFailed                   = agentcontract.TaskStatusFailed
	TaskStatusInterrupted              = agentcontract.TaskStatusInterrupted
	TaskStatusPlanned                  = agentcontract.TaskStatusPlanned
	TaskStatusRunning                  = agentcontract.TaskStatusRunning
	TaskStatusWaitingApproval          = agentcontract.TaskStatusWaitingApproval
	TaskStatusWaitingUserInput         = agentcontract.TaskStatusWaitingUserInput
)

var (
	ErrTaskRunAccessDenied                = taskstate.ErrTaskRunAccessDenied
	ErrTaskRunNotDeletable                = taskstate.ErrTaskRunNotDeletable
	ErrTaskRunNotFound                    = taskstate.ErrTaskRunNotFound
	NewIdentifier                         = taskstate.NewIdentifier
	NewTaskArtifactService                = taskstate.NewTaskArtifactService
	NewTaskEventService                   = taskstate.NewTaskEventService
	NewTaskRunService                     = taskstate.NewTaskRunService
	NewTaskStepService                    = taskstate.NewTaskStepService
	StaleUnattendedTaskRunReason          = taskstate.StaleUnattendedTaskRunReason
	TaskRunWasInterruptedByRuntimeRestart = agentcontract.TaskRunWasInterruptedByRuntimeRestart
)
