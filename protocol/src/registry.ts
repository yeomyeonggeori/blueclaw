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
import { connectorPlatformSchema, messengerPlatformSchema } from './platform.ts';
import {
  ledgerEventNameSchema,
  taskArtifactSchema,
  taskAttemptSchema,
  taskEventSchema,
  taskRunSchema,
  taskScheduleSchema,
  toolTaskEventSuffixSchema,
} from './task.ts';

export const protocolVersion = '0.4.0';

export const protocolSchemas = {
  'agent-action': agentActionSchema,
  'agent-message': agentMessageSchema,
  'approval-target': approvalTargetSchema,
  'capability-descriptor': capabilityDescriptorSchema,
  'capability-registry-response': capabilityRegistryResponseSchema,
  'chat-completion-request': chatCompletionRequestSchema,
  'chat-completion-response': chatCompletionResponseSchema,
  'connector-platform': connectorPlatformSchema,
  'connector-runtime-result': connectorRuntimeResultSchema,
  'messenger-platform': messengerPlatformSchema,
  'platform-inbound-event': platformInboundEventSchema,
  'structured-response': structuredResponseSchema,
  'structured-response-request': structuredResponseRequestSchema,
  'task-artifact': taskArtifactSchema,
  'task-attempt': taskAttemptSchema,
  'task-event': taskEventSchema,
  'task-event-name': ledgerEventNameSchema,
  'task-run': taskRunSchema,
  'task-schedule': taskScheduleSchema,
  'tool-invoke-request': toolInvokeRequestSchema,
  'tool-task-event-suffix': toolTaskEventSuffixSchema,
  'tool-invoke-response': toolInvokeResponseSchema,
} satisfies Record<string, z.ZodType>;

export type ProtocolSchemaName = keyof typeof protocolSchemas;
