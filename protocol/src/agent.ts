import { z } from 'zod';

import { jsonValueSchema, nonNegativeIntegerSchema } from './common.ts';

export const agentPartSourceSchema = z.looseObject({
  platform: z.string().optional(),
  messageID: z.string().optional(),
  fileID: z.string().optional(),
  observationID: z.string().optional(),
  toolName: z.string().optional(),
});

export const agentImagePartSchema = z.looseObject({
  mimeType: z.string().optional(),
  dataBase64: z.string().optional(),
  path: z.string().optional(),
  filename: z.string().optional(),
  width: nonNegativeIntegerSchema.optional(),
  height: nonNegativeIntegerSchema.optional(),
});

export const agentFilePartSchema = z.looseObject({
  path: z.string().optional(),
  filename: z.string().optional(),
  contentType: z.string().optional(),
  sizeBytes: nonNegativeIntegerSchema.optional(),
  markdownPreview: z.string().optional(),
  conversionStatus: z.string().optional(),
  conversionMessage: z.string().optional(),
});

const agentPartMetadataSchema = z.looseObject({
  source: agentPartSourceSchema.optional(),
  visibility: z.string().optional(),
});

export const agentPartSchema = z.discriminatedUnion('type', [
  agentPartMetadataSchema.extend({
    type: z.literal('text'),
    text: z.string(),
  }),
  agentPartMetadataSchema.extend({
    type: z.literal('image'),
    image: agentImagePartSchema,
    file: agentFilePartSchema.optional(),
  }),
  agentPartMetadataSchema.extend({
    type: z.literal('file'),
    file: agentFilePartSchema,
  }),
]);

export const agentMessageSchema = z.looseObject({
  role: z.string(),
  parts: z.array(agentPartSchema).optional(),
});

export const executionStateSchema = z.looseObject({
  goal: z.string().optional(),
  workspace: z.string().optional(),
  knownFacts: z.array(z.string()).optional(),
  triedAndFailed: z.array(z.string()).optional(),
  currentBlocker: z.string().optional(),
  nextPlan: z.string().optional(),
  wasCompacted: z.boolean().optional(),
});

export const qualityReviewItemSchema = z.looseObject({
  id: z.string().optional(),
  passed: z.boolean().optional(),
  evidenceIDs: z.array(z.string()).optional(),
  notes: z.string().optional(),
});

export const failureReportAttemptSchema = z.looseObject({
  toolName: z.string().optional(),
  inputSummary: z.string().optional(),
  errorCode: z.string().optional(),
  failureStage: z.string().optional(),
  message: z.string().optional(),
});

export const failureReportFactsSchema = z.looseObject({
  attempts: z.array(failureReportAttemptSchema).optional(),
  budgetState: z.string().optional(),
});

export const replyAttachmentSchema = z.strictObject({
  path: z.string(),
  filename: z.string().optional(),
});

const actionStateSchema = z.strictObject({
  message: z.string().optional(),
  reason: z.string().optional(),
  goalStatus: z.string().optional(),
  goalSatisfied: z.boolean().optional(),
  hasRemainingWork: z.boolean().optional(),
  executionStateUpdate: executionStateSchema,
});

export const continueActionSchema = actionStateSchema.extend({
  action: z.literal('continue'),
  toolName: z.string(),
  toolInput: jsonValueSchema,
});

export const setQualityCriteriaActionSchema = actionStateSchema.extend({
  action: z.literal('set_quality_criteria'),
  qualityCriteria: z.array(z.string()),
});

export const replyActionSchema = actionStateSchema.extend({
  action: z.literal('reply'),
  message: z.string(),
  attachments: z.array(replyAttachmentSchema).optional(),
  choices: z.array(z.string()).optional(),
  expectsAnswer: z.boolean().optional(),
  final: z.boolean(),
  goalSatisfied: z.boolean(),
  hasRemainingWork: z.boolean(),
  failureResolution: z.string().optional(),
  goalStatus: z.enum(['satisfied', 'in_progress']),
  completionEvidenceIDs: z.array(z.string()),
  qualityReview: z.array(qualityReviewItemSchema),
});

export const failActionSchema = actionStateSchema.extend({
  action: z.literal('fail'),
  reason: z.string(),
  failureResolution: z.string().optional(),
  goalStatus: z.literal('blocked'),
  usedFailureFacts: failureReportFactsSchema.optional(),
});

export const agentActionSchema = z.discriminatedUnion('action', [
  continueActionSchema,
  setQualityCriteriaActionSchema,
  replyActionSchema,
  failActionSchema,
]);

export type AgentAction = z.infer<typeof agentActionSchema>;
export type AgentMessage = z.infer<typeof agentMessageSchema>;
