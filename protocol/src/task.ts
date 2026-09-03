import { z } from 'zod';

import { nonNegativeIntegerSchema } from './common.ts';

export enum TaskStatus {
  Planned = 'planned',
  Running = 'running',
  WaitingUserInput = 'waiting_user_input',
  WaitingApproval = 'waiting_approval',
  Blocked = 'blocked',
  Interrupted = 'interrupted',
  Completed = 'completed',
  Failed = 'failed',
  Cancelled = 'cancelled',
}

export const taskStatusSchema = z.enum(TaskStatus);

export enum TaskAttemptStatus {
  Starting = 'starting',
  Running = 'running',
  Completed = 'completed',
  Failed = 'failed',
  Cancelled = 'cancelled',
  Interrupted = 'interrupted',
}

export const taskAttemptStatusSchema = z.enum(TaskAttemptStatus);

export enum TaskScheduleExecutionMode {
  Agent = 'agent',
  Message = 'message',
}

export const taskRunSchema = z.looseObject({
  taskRunID: z.string(),
  requesterPersonID: z.string(),
  originConversationID: z.string(),
  originReplyTargetID: z.string().optional(),
  originIsThread: z.boolean().optional(),
  currentAttemptID: z.string().optional(),
  currentAgentProfileName: z.string(),
  status: taskStatusSchema,
  prompt: z.string(),
  result: z.string(),
  failureReason: z.string(),
  createdAt: z.iso.datetime({ offset: true }),
  updatedAt: z.iso.datetime({ offset: true }),
});

export const taskAttemptSchema = z.looseObject({
  taskAttemptID: z.string(),
  taskRunID: z.string(),
  runnerID: z.string(),
  status: taskAttemptStatusSchema,
  startedAt: z.iso.datetime({ offset: true }),
  finishedAt: z.iso.datetime({ offset: true }).nullable().optional(),
  failureReason: z.string().optional(),
});

export enum TaskEventName {
  AgentAction = 'agent.action',
  AgentAmbientDutyLaunch = 'agent.ambient_duty_launch',
  AgentApprovalUserFacingMessageMissing = 'agent.approval_user_facing_message_missing',
  AgentArtifactAttachRejected = 'agent.artifact_attach_rejected',
  AgentBudgetExtendedOneLevel = 'agent.budget_extended_one_level',
  AgentBudgetUpdateSent = 'agent.budget_update_sent',
  AgentCheckpointFailed = 'agent.checkpoint.failed',
  AgentCheckpointSent = 'agent.checkpoint.sent',
  AgentCheckpointSkipped = 'agent.checkpoint.skipped',
  AgentCompanyTimeZoneFallback = 'agent.company_time_zone_fallback',
  AgentCompletionPersistFailed = 'agent.completion_persist_failed',
  AgentCompletionReplyFailed = 'agent.completion_reply_failed',
  AgentCompletionRequired = 'agent.completion_required',
  AgentCompletionStateBestEffort = 'agent.completion_state_best_effort',
  AgentCompletionStateFinalized = 'agent.completion_state_finalized',
  AgentCompletionStateRejected = 'agent.completion_state_rejected',
  AgentCompletionStateTransition = 'agent.completion_state_transition',
  AgentConfirmationPlanDegraded = 'agent.confirmation_plan_degraded',
  AgentConsumed = 'agent.consumed',
  AgentContextCompactionFreedNothing = 'agent.context_compaction_freed_nothing',
  AgentContextSummary = 'agent.context_summary',
  AgentContractArbitrationDegraded = 'agent.contract_arbitration_degraded',
  AgentConversationBudget = 'agent.conversation_budget',
  AgentConversationScope = 'agent.conversation_scope',
  AgentDuplicateToolCallRejected = 'agent.duplicate_tool_call_rejected',
  AgentEvidenceMissing = 'agent.evidence_missing',
  AgentExecutionState = 'agent.execution_state',
  AgentExternalSendIntentRejected = 'agent.external_send_intent_rejected',
  AgentExternalSendRepeatRejected = 'agent.external_send_repeat_rejected',
  AgentFailedFingerprintRejected = 'agent.failed_fingerprint_rejected',
  AgentFailureDebtCreated = 'agent.failure_debt_created',
  AgentFailureReply = 'agent.failure_reply',
  AgentFailureReport = 'agent.failure_report',
  AgentFailureReportFactsUsed = 'agent.failure_report_facts_used',
  AgentFailureReportRejected = 'agent.failure_report_rejected',
  AgentFileReadCacheHit = 'agent.file_read_cache_hit',
  AgentFinalizerAction = 'agent.finalizer_action',
  AgentFinalizerEvidenceSupplied = 'agent.finalizer_evidence_supplied',
  AgentFinalizerFailed = 'agent.finalizer_failed',
  AgentFinalizerRejected = 'agent.finalizer_rejected',
  AgentGoalBlocked = 'agent.goal.blocked',
  AgentGoalCompleted = 'agent.goal.completed',
  AgentGoalCreated = 'agent.goal.created',
  AgentGoalUpdated = 'agent.goal.updated',
  AgentGoalWaitingApproval = 'agent.goal.waiting_approval',
  AgentGoalWaitingUserInput = 'agent.goal.waiting_user_input',
  AgentIdenticalOutput = 'agent.identical_output',
  AgentInstructionsLoaded = 'agent.instructions_loaded',
  AgentIntake = 'agent.intake',
  AgentLaunchStepError = 'agent.launch_step.error',
  AgentLaunchStepResult = 'agent.launch_step.result',
  AgentLimitCompletedFromEvidence = 'agent.limit_completed_from_evidence',
  AgentLimitPressure = 'agent.limit_pressure',
  AgentLimitReply = 'agent.limit_reply',
  AgentLimitStop = 'agent.limit_stop',
  AgentLLMUnavailable = 'agent.llm_unavailable',
  AgentNoProgressLoopPaused = 'agent.no_progress_loop_paused',
  AgentNoProgressLoopStopped = 'agent.no_progress_loop_stopped',
  AgentNonRetryableToolRefused = 'agent.non_retryable_tool_refused',
  AgentPlanNudged = 'agent.plan.nudged',
  AgentPlanSized = 'agent.plan.sized',
  AgentPlanUpdated = 'agent.plan.updated',
  AgentQualityCriteria = 'agent.quality_criteria',
  AgentQualityReview = 'agent.quality_review',
  AgentRecoverableFailRejected = 'agent.recoverable_fail_rejected',
  AgentRecoveryAttempt = 'agent.recovery_attempt',
  AgentRecoveryBudgetExhausted = 'agent.recovery_budget_exhausted',
  AgentRecoveryGenerationFailed = 'agent.recovery_generation_failed',
  AgentRecoveryGuidance = 'agent.recovery_guidance',
  AgentRepeatedToolCall = 'agent.repeated_tool_call',
  AgentStallBlockedReply = 'agent.stall_blocked_reply',
  AgentStallExitDirective = 'agent.stall_exit_directive',
  AgentStallPauseReply = 'agent.stall_pause_reply',
  AgentStallRecoveryDirective = 'agent.stall_recovery_directive',
  AgentStepWorkingSet = 'agent.step_working_set',
  AgentSuggestedNextToolDirective = 'agent.suggested_next_tool_directive',
  AgentTaskLaunched = 'agent.task_launched',
  AgentTaskSource = 'agent.task_source',
  AgentTerminalNoToolsAction = 'agent.terminal_no_tools_action',
  AgentTerminalNoToolsRejected = 'agent.terminal_no_tools_rejected',
  AgentToolInputMalformed = 'agent.tool_input_malformed',
  AgentToolResultImagesRestored = 'agent.tool_result_images_restored',
  AgentToolResultsPruned = 'agent.tool_results_pruned',
  AgentTurnAnchorClamped = 'agent.turn_anchor_clamped',
  AgentUnchangedResult = 'agent.unchanged_result',
  AgentUnreadableAction = 'agent.unreadable_action',
  AgentValidityReview = 'agent.validity_review',
  ApprovalContinued = 'approval.continued',
  ApprovalDecided = 'approval.decided',
  ApprovalExecuted = 'approval.executed',
  ApprovalHeldCall = 'approval.held_call',
  ApprovalPendingCall = 'approval.pending_call',
  ApprovalScopeGranted = 'approval.scope_granted',
  ApprovalUnheldCallCarriedOut = 'approval.unheld_call_carried_out',
  AskReplyClassified = 'ask.reply_classified',
  AskRequested = 'ask.requested',
  AskResolved = 'ask.resolved',
  AskSupersededByMessage = 'ask.superseded_by_message',
  BlueclawTaskExecutionDuration = 'blueclaw.task.execution_duration',
  CompletionJudgeDegraded = 'completion_judge.degraded',
  CompletionJudgeExpanded = 'completion_judge.expanded',
  CompletionJudgeStandingVerdict = 'completion_judge.standing_verdict',
  CompletionJudgeVerdict = 'completion_judge.verdict',
  ConfirmationClarificationRequested = 'confirmation.clarification_requested',
  ConfirmationPlanCreated = 'confirmation.plan_created',
  ConfirmationPolicyDecision = 'confirmation.policy_decision',
  ConfirmationRejected = 'confirmation.rejected',
  ConfirmationReplaced = 'confirmation.replaced',
  ConfirmationReplyClassified = 'confirmation.reply_classified',
  ConfirmationRequested = 'confirmation.requested',
  ConnectorDuplicateSourceSuppressed = 'connector.duplicate_source_suppressed',
  ConnectorReactionFailed = 'connector.reaction.failed',
  ConnectorReactionSent = 'connector.reaction.sent',
  ConnectorReactionSkipped = 'connector.reaction.skipped',
  ConnectorReplyEnqueued = 'connector.reply.enqueued',
  ConnectorReplyFailed = 'connector.reply.failed',
  ConnectorReplySent = 'connector.reply.sent',
  ConnectorReplySuppressed = 'connector.reply.suppressed',
  DelegateFinished = 'delegate.finished',
  DelegateLaunched = 'delegate.launched',
  HarnessToolPermitted = 'harness.tool_permitted',
  HarnessToolRefused = 'harness.tool_refused',
  LLMCall = 'llm.call',
  MemoryPinnedLoadFailed = 'memory.pinned_load_failed',
  MemoryPinnedLoadSucceeded = 'memory.pinned_load_succeeded',
  ReplySuppressedDuplicate = 'reply.suppressed_duplicate',
  ScheduleCancelled = 'schedule.cancelled',
  ScheduleCreated = 'schedule.created',
  ScheduleUpdated = 'schedule.updated',
  TaskAbandonedByTurn = 'task.abandoned_by_turn',
  TaskAutoResumeAbandoned = 'task.auto_resume_abandoned',
  TaskAutoResumeAttempted = 'task.auto_resume_attempted',
  TaskAutoResumeLaunchFailed = 'task.auto_resume_launch_failed',
  TaskAutoResumeReplyUnavailable = 'task.auto_resume_reply_unavailable',
  TaskAutoResumeSkipped = 'task.auto_resume_skipped',
  TaskBlocked = 'task.blocked',
  TaskBusyMessageAfterFinish = 'task.busy_message.after_finish',
  TaskBusyMessageRouted = 'task.busy_message.routed',
  TaskCancelRequested = 'task.cancel.requested',
  TaskCancelled = 'task.cancelled',
  TaskCompleted = 'task.completed',
  TaskCreated = 'task.created',
  TaskFailed = 'task.failed',
  TaskInterrupted = 'task.interrupted',
  TaskModelVisibleContext = 'task.model_visible_context',
  TaskObserverCrashed = 'task.observer_crashed',
  TaskPaused = 'task.paused',
  TaskReplaced = 'task.replaced',
  TaskRunning = 'task.running',
  TaskStaleExpired = 'task.stale_expired',
  TaskStatusRequested = 'task.status.requested',
  TaskSteerApplied = 'task.steer.applied',
  TaskSteerRequested = 'task.steer.requested',
  TaskSteerResumeUnavailable = 'task.steer.resume_unavailable',
  TaskStopCancelled = 'task.stop.cancelled',
  TaskStopClassified = 'task.stop.classified',
  TaskStopOutboxSuppressed = 'task.stop.outbox_suppressed',
  TaskStopRequested = 'task.stop.requested',
  TaskSupersededByMessage = 'task.superseded_by_message',
  TaskWaitPersistFailed = 'task.wait.persist_failed',
  TaskWaitCancelled = 'task.wait_cancelled',
  TaskScheduleDeliveryEnqueued = 'task_schedule.delivery.enqueued',
  TaskScheduleDeliveryFailed = 'task_schedule.delivery.failed',
  TerminalRunHeartbeat = 'terminal.run.heartbeat',
  ToolCrashed = 'tool.crashed',
  ToolResultSpillFailed = 'tool.result_spill_failed',
  ToolResultSpilled = 'tool.result_spilled',
}

export const taskEventNameSchema = z.enum(TaskEventName);

export const toolTaskEventPrefix = 'tool.';

export enum ToolTaskEventSuffix {
  Requested = '.requested',
  Result = '.result',
  Cancelled = '.cancelled',
}

export const toolTaskEventSuffixSchema = z.enum(ToolTaskEventSuffix);

export const toolTaskEventNamePattern = buildToolTaskEventNamePattern();

export const toolTaskEventNameSchema = z.string().regex(new RegExp(toolTaskEventNamePattern));

export const ledgerEventNameSchema = z.union([taskEventNameSchema, toolTaskEventNameSchema]);

function buildToolTaskEventNamePattern(): string {
  const suffixes = Object.values(ToolTaskEventSuffix).map(escapeRegularExpression).join('|');
  return `^${escapeRegularExpression(toolTaskEventPrefix)}[a-z0-9_]+(?:${suffixes})$`;
}

function escapeRegularExpression(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

export const taskEventSchema = z.looseObject({
  taskEventID: z.string(),
  taskRunID: z.string(),
  name: z.string(),
  body: z.string(),
  createdAt: z.iso.datetime({ offset: true }),
});

export const taskArtifactSchema = z.looseObject({
  taskArtifactID: z.string(),
  taskRunID: z.string(),
  name: z.string(),
  body: z.string(),
});

const taskScheduleCommonSchema = z.looseObject({
  taskScheduleID: z.string(),
  creatorPersonID: z.string(),
  name: z.string(),
  prompt: z.string(),
  executionMode: z.enum(TaskScheduleExecutionMode),
  agentProfileName: z.string(),
  platform: z.string(),
  conversationID: z.string(),
  replyTargetID: z.string(),
  timeZone: z.string(),
  maxRunCount: nonNegativeIntegerSchema.optional(),
  completedRunCount: nonNegativeIntegerSchema,
  expiresAt: z.iso.datetime({ offset: true }).nullable(),
  nextRunAt: z.iso.datetime({ offset: true }).nullable(),
  lastRunAt: z.iso.datetime({ offset: true }).nullable(),
  lastTaskRunID: z.string(),
  leaseOwner: z.string(),
  leasedUntil: z.iso.datetime({ offset: true }).nullable(),
  failureCount: nonNegativeIntegerSchema,
  lastError: z.string(),
  nextAttemptAt: z.iso.datetime({ offset: true }).nullable(),
  createdAt: z.iso.datetime({ offset: true }),
  updatedAt: z.iso.datetime({ offset: true }),
});

export const taskScheduleSchema = z.discriminatedUnion('kind', [
  taskScheduleCommonSchema.extend({
    kind: z.literal('once'),
    runAt: z.iso.datetime({ offset: true }),
    intervalSecond: nonNegativeIntegerSchema,
    cronExpression: z.string(),
  }),
  taskScheduleCommonSchema.extend({
    kind: z.literal('interval'),
    runAt: z.iso.datetime({ offset: true }).nullable(),
    intervalSecond: z.number().int().positive(),
    cronExpression: z.string(),
  }),
  taskScheduleCommonSchema.extend({
    kind: z.literal('cron'),
    runAt: z.iso.datetime({ offset: true }).nullable(),
    intervalSecond: nonNegativeIntegerSchema,
    cronExpression: z.string().min(1),
  }),
]);

export type TaskRun = z.infer<typeof taskRunSchema>;
export type TaskSchedule = z.infer<typeof taskScheduleSchema>;
