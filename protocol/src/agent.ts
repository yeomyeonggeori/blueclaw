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

export const completionEvidenceReferenceSchema = z.looseObject({
  observationID: z.string(),
  toolName: z.string(),
  attachmentIndex: nonNegativeIntegerSchema.optional(),
});

export const qualityReviewItemSchema = z.looseObject({
  id: z.string().optional(),
  passed: z.boolean().optional(),
  evidenceIDs: z.array(z.string()).optional(),
  evidence: z.array(completionEvidenceReferenceSchema).optional(),
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

const actionStateSchema = z.strictObject({
  message: z.string().optional(),
  reason: z.string().optional(),
  goalStatus: z.string().optional(),
  goalSatisfied: z.boolean().optional(),
  remainingWork: z.string().optional(),
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

export const finishActionSchema = actionStateSchema.extend({
  action: z.literal('finish'),
  message: z.string(),
  goalSatisfied: z.boolean(),
  failureResolution: z.string().optional(),
  goalStatus: z.literal('satisfied'),
  completionEvidenceIDs: z.array(z.string()),
  completionEvidence: z.array(completionEvidenceReferenceSchema).optional(),
  qualityReview: z.array(qualityReviewItemSchema),
});

export const failActionSchema = actionStateSchema.extend({
  action: z.literal('fail'),
  reason: z.string(),
  goalSatisfied: z.boolean(),
  failureResolution: z.string().optional(),
  goalStatus: z.literal('blocked'),
  usedFailureFacts: failureReportFactsSchema.optional(),
});

export const agentActionSchema = z.discriminatedUnion('action', [
  continueActionSchema,
  setQualityCriteriaActionSchema,
  finishActionSchema,
  failActionSchema,
]);

export type AgentAction = z.infer<typeof agentActionSchema>;
export type AgentMessage = z.infer<typeof agentMessageSchema>;
