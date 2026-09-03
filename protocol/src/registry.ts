import type { z } from 'zod';

import { agentActionSchema, agentMessageSchema } from './agent.ts';
import {
  approvalTargetSchema,
  capabilityDescriptorSchema,
  capabilityRegistryResponseSchema,
  toolInvokeRequestSchema,
  toolInvokeResponseSchema,
} from './capability.ts';
import { connectorRuntimeResultSchema, platformInboundEventSchema } from './chat.ts';
import {
  chatCompletionRequestSchema,
  chatCompletionResponseSchema,
  structuredResponseRequestSchema,
  structuredResponseSchema,
} from './llm.ts';
import { taskArtifactSchema, taskAttemptSchema, taskEventSchema, taskRunSchema, taskScheduleSchema } from './task.ts';

export const protocolVersion = '0.4.0';

export const protocolSchemas = {
  'agent-action': agentActionSchema,
  'agent-message': agentMessageSchema,
  'approval-target': approvalTargetSchema,
  'capability-descriptor': capabilityDescriptorSchema,
  'capability-registry-response': capabilityRegistryResponseSchema,
  'chat-completion-request': chatCompletionRequestSchema,
  'chat-completion-response': chatCompletionResponseSchema,
  'connector-runtime-result': connectorRuntimeResultSchema,
  'platform-inbound-event': platformInboundEventSchema,
  'structured-response': structuredResponseSchema,
  'structured-response-request': structuredResponseRequestSchema,
  'task-artifact': taskArtifactSchema,
  'task-attempt': taskAttemptSchema,
  'task-event': taskEventSchema,
  'task-run': taskRunSchema,
  'task-schedule': taskScheduleSchema,
  'tool-invoke-request': toolInvokeRequestSchema,
  'tool-invoke-response': toolInvokeResponseSchema,
} satisfies Record<string, z.ZodType>;

export type ProtocolSchemaName = keyof typeof protocolSchemas;
