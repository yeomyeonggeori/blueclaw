import { z } from 'zod';

import {
  ExecutionMode,
  jsonValueSchema,
  nonNegativeIntegerSchema,
  protocolIdentitySchema,
  resourceScopeSchema,
} from './common.ts';
import { registerCanonicalClosedJSONSchema } from './json_schema.ts';

const nonBlankStringSchema = z.string().trim().min(1);
const unpaddedStringSchema = z.string().min(1).refine(value => value === value.trim(), {
  message: 'value must not have surrounding whitespace',
});

const strictObjectJsonSchema = registerCanonicalClosedJSONSchema(
  z.looseObject({
    type: z.literal('object'),
    additionalProperties: z.union([z.literal(false), z.record(z.string(), jsonValueSchema)]),
  }).superRefine((schema, context) => {
    if (!schemaObjectsAreClosed(schema)) {
      context.addIssue({ code: 'custom', message: 'every object schema must disable additional properties' });
    }
  }).meta({ id: 'canonical-strict-object-json-schema' }),
  'strict_object',
);

function schemaObjectsAreClosed(value: unknown): boolean {
  if (Array.isArray(value)) return value.every(schemaObjectsAreClosed);
  if (!value || typeof value !== 'object') return true;
  const entries = Object.entries(value);
  const schemaType = entries.find(([key]) => key === 'type')?.[1];
  const additionalProperties = entries.find(([key]) => key === 'additionalProperties');
  if (schemaType === 'object' && (!additionalProperties || additionalProperties[1] === true)) return false;
  return entries.every(([, child]) => schemaObjectsAreClosed(child));
}

function objectField(value: unknown, fieldName: string): unknown {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
  return Object.entries(value).find(([key]) => key === fieldName)?.[1];
}

function schemaRequiresEffectIdentityField(schema: unknown, fieldName: string): boolean {
  const requiredFields = objectField(schema, 'required');
  if (!Array.isArray(requiredFields) || !requiredFields.includes(fieldName)) return false;
  return schemaDefinesEffectIdentityField(schema, fieldName);
}

function schemaDefinesEffectIdentityField(schema: unknown, fieldName: string): boolean {
  const property = objectField(objectField(schema, 'properties'), fieldName);
  if (objectField(property, 'type') === 'string') return true;
  const minimumItems = objectField(property, 'minItems');
  return objectField(property, 'type') === 'array'
    && objectField(objectField(property, 'items'), 'type') === 'string'
    && typeof minimumItems === 'number'
    && Number.isInteger(minimumItems)
    && minimumItems >= 1
    && objectField(property, 'uniqueItems') === true;
}

function isJSONSchema(value: unknown): value is Parameters<typeof z.fromJSONSchema>[0] {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

function schemaAcceptsEvidenceCondition(
  schema: unknown,
  condition: z.infer<typeof evidenceConditionSchema>,
): boolean {
  const requiredFields = objectField(schema, 'required');
  const properties = objectField(schema, 'properties');
  const property = objectField(properties, condition.resultField);
  if (!Array.isArray(requiredFields) || !requiredFields.includes(condition.resultField) || !isJSONSchema(property)) {
    return false;
  }
  try {
    return z.fromJSONSchema(property).safeParse(condition.equals).success;
  } catch {
    return false;
  }
}

function schemaAcceptsEmptyObject(schema: unknown): boolean {
  if (!isJSONSchema(schema)) return false;
  try {
    return z.fromJSONSchema(schema).safeParse({}).success;
  } catch {
    return false;
  }
}

function schemaPropertiesAreSubset(inputSchema: unknown, inputIntentSchema: unknown): boolean {
  const inputProperties = objectField(inputSchema, 'properties');
  const intentProperties = objectField(inputIntentSchema, 'properties');
  if (!intentProperties || typeof intentProperties !== 'object' || Array.isArray(intentProperties)) return true;
  return Object.keys(intentProperties).every(propertyName => objectField(inputProperties, propertyName) !== undefined);
}

function descriptorRequiresInputIntentSchema(descriptor: {
  modelVisibility: CapabilityModelVisibility;
  sideEffectClass: CapabilitySideEffect;
}): boolean {
  if (descriptor.modelVisibility !== CapabilityModelVisibility.Visible) return false;
  return descriptor.sideEffectClass !== CapabilitySideEffect.Read
    && descriptor.sideEffectClass !== CapabilitySideEffect.Computation;
}

export enum CapabilityEstimatedLatency {
  Low = 'low',
  Medium = 'medium',
  High = 'high',
  Interactive = 'interactive',
}

export enum CapabilityAnsweredBy {
  Record = 'record',
  Company = 'company',
  Local = 'local',
}

export enum CapabilityModelVisibility {
  Visible = 'visible',
  Hidden = 'hidden',
}

export enum CapabilityAvailabilityState {
  Available = 'ok',
  NotAllowed = 'not_allowed',
  NotConnected = 'not_connected',
  NotReady = 'not_ready',
}

export enum CapabilitySideEffect {
  Approval = 'approval',
  Computation = 'computation',
  Connect = 'connect',
  Destructive = 'destructive',
  ExternalPublish = 'external_publish',
  ExternalSend = 'external_send',
  ExternalWrite = 'external_write',
  LocalFile = 'local_file',
  PlatformReply = 'platform_reply',
  Read = 'read',
  SitePublish = 'site_publish',
  WorkspaceWrite = 'workspace_write',
}

export enum ToolOutcome {
  Succeeded = 'succeeded',
  Failed = 'failed',
  Denied = 'denied',
}

export enum ResourceEffectIdentity {
  ID = 'id',
  Path = 'path',
  URL = 'url',
}

export const completionEvidenceDescriptorSchema = z.strictObject({
  mode: z.string().optional(),
  action: z.string().optional(),
  targetKind: z.string().optional(),
});

export const evidenceConditionSchema = z.strictObject({
  resultField: z.string().trim().min(1),
  equals: jsonValueSchema,
});

export const resourceEffectContractSchema = z.strictObject({
  objectType: z.string().trim().min(1),
  effect: z.string().trim().min(1),
  resultField: z.string().trim().min(1),
  effectIdentity: z.enum(ResourceEffectIdentity),
  when: evidenceConditionSchema.optional(),
});

export const resourceEffectSchema = z.strictObject({
  objectType: z.string().trim().min(1),
  effect: z.string().trim().min(1),
  id: z.string().optional(),
  path: z.string().optional(),
  url: z.string().optional(),
  visibility: z.string().optional(),
  durability: z.string().optional(),
  filename: z.string().optional(),
  contentType: z.string().optional(),
  summary: z.string().optional(),
}).refine(effect => Boolean(effect.id?.trim() || effect.path?.trim() || effect.url?.trim()), {
  message: 'resource effect requires id, path, or url',
});

export const toolResultContractSchema = z.strictObject({
  schema: strictObjectJsonSchema,
  effects: z.array(resourceEffectContractSchema).optional(),
  evidenceCondition: evidenceConditionSchema.optional(),
}).superRefine((contract, context) => {
  const keys = contract.effects?.map(effect => `${effect.objectType}\u0000${effect.effect}\u0000${effect.effectIdentity}`) ?? [];
  if (new Set(keys).size !== keys.length) {
    context.addIssue({ code: 'custom', message: 'effects must be unique' });
  }
  for (const effect of contract.effects ?? []) {
    if (effect.when === undefined) {
      if (!schemaRequiresEffectIdentityField(contract.schema, effect.resultField)) {
        context.addIssue({ code: 'custom', message: 'resultField must name a required string or nonempty unique string array property' });
      }
      continue;
    }
    if (!schemaDefinesEffectIdentityField(contract.schema, effect.resultField)) {
      context.addIssue({ code: 'custom', message: 'conditional effect resultField must name a string or nonempty unique string array property' });
    }
    if (!schemaAcceptsEvidenceCondition(contract.schema, effect.when)) {
      context.addIssue({ code: 'custom', message: 'effect when condition must match a required result property' });
    }
  }
  if (contract.evidenceCondition && !schemaAcceptsEvidenceCondition(contract.schema, contract.evidenceCondition)) {
    context.addIssue({ code: 'custom', message: 'evidenceCondition must match a required result property' });
  }
});

export const capabilityAvailabilitySchema = z.strictObject({
  state: z.enum(CapabilityAvailabilityState),
  reason: z.string().optional(),
});

export const capabilityIdempotencySchema = z.strictObject({
  supported: z.boolean(),
  required: z.boolean(),
  scope: z.string().trim().min(1),
});

export const capabilityDescriptorSchema = z.strictObject({
  name: unpaddedStringSchema,
  canonicalName: unpaddedStringSchema,
  namespace: unpaddedStringSchema,
  modelName: unpaddedStringSchema,
  modelVisibility: z.enum(CapabilityModelVisibility),
  modelVisible: z.boolean(),
  description: nonBlankStringSchema,
  version: nonBlankStringSchema,
  privacyClass: nonBlankStringSchema,
  estimatedLatency: z.enum(CapabilityEstimatedLatency),
  answeredBy: z.enum(CapabilityAnsweredBy).describe(
    'Which of three things holds the answer: the record, a service on the company machine, or a runtime beside the agent. Not whose machine, which requiresRequesterDevice states. A caller with no runtime of its own, a public API among them, refuses local rather than forwarding it.',
  ),
  requiresUserPresence: z.boolean(),
  requiresRequesterDevice: z.boolean().optional(),
  requiresCompanionBrowser: z.boolean().optional(),
  worksOffline: z.boolean(),
  inputSchema: strictObjectJsonSchema,
  inputIntentSchema: strictObjectJsonSchema.optional(),
  outputSchema: strictObjectJsonSchema,
  inputSchemaStrict: z.literal(true),
  outputSchemaStrict: z.literal(true),
  resultContract: toolResultContractSchema.optional(),
  policyResource: nonBlankStringSchema,
  sideEffectClass: z.enum(CapabilitySideEffect),
  sideEffect: z.enum(CapabilitySideEffect),
  requiresApproval: z.boolean().optional(),
  approvalScope: z.string().optional(),
  completionEvidence: completionEvidenceDescriptorSchema.optional(),
  availability: capabilityAvailabilitySchema,
  idempotency: capabilityIdempotencySchema,
}).refine(descriptor => descriptor.modelVisible === (descriptor.modelVisibility === CapabilityModelVisibility.Visible), {
  message: 'modelVisible must match modelVisibility',
}).refine(descriptor => descriptor.sideEffectClass === descriptor.sideEffect, {
  message: 'sideEffectClass must match sideEffect',
}).superRefine((descriptor, context) => {
  if (descriptorRequiresInputIntentSchema(descriptor) && descriptor.inputIntentSchema === undefined) {
    context.addIssue({ code: 'custom', path: ['inputIntentSchema'], message: 'model-visible state-changing capabilities require inputIntentSchema' });
  }
  if (descriptor.inputIntentSchema !== undefined && !schemaAcceptsEmptyObject(descriptor.inputIntentSchema)) {
    context.addIssue({ code: 'custom', path: ['inputIntentSchema'], message: 'inputIntentSchema must accept an empty object' });
  }
  if (descriptor.inputIntentSchema !== undefined && !schemaPropertiesAreSubset(descriptor.inputSchema, descriptor.inputIntentSchema)) {
    context.addIssue({ code: 'custom', path: ['inputIntentSchema'], message: 'inputIntentSchema properties must exist in inputSchema' });
  }
});

export const capabilityRegistryResponseSchema = protocolIdentitySchema.extend({
  localOnly: z.boolean(),
  routingCandidates: z.array(z.string()).nullable(),
  deviceCapabilities: z.array(capabilityDescriptorSchema).optional(),
  companionStatus: z.string().optional(),
  companionCapabilities: z.array(capabilityDescriptorSchema).optional(),
  capabilities: z.array(capabilityDescriptorSchema).optional(),
});

export const toolInvokeContextSchema = z.looseObject({
  requesterPersonID: z.string().optional(),
  requesterEmail: z.string().optional(),
  requesterName: z.string().optional(),
  requesterPlatformUserID: z.string().optional(),
  taskSource: z.string().optional(),
  isScheduledRun: z.boolean().optional(),
  isApprovalContinuation: z.boolean().optional(),
  conversationID: z.string().optional(),
  conversationType: z.string().optional(),
  channelID: z.string().optional(),
  channelName: z.string().optional(),
  replyTargetID: z.string().optional(),
  platform: z.string().optional(),
});

export const actorContextSchema = z.looseObject({
  personID: z.string().optional(),
  email: z.string().optional(),
  displayName: z.string().optional(),
  source: z.string().optional(),
  scopes: z.array(z.string()).optional(),
  isAdmin: z.boolean().optional(),
});

export const siteSourceBundleSchema = z.strictObject({
  workspacePath: unpaddedStringSchema,
  contentBase64: nonBlankStringSchema,
  format: z.literal('tar.gz'),
  sha256: z.string().regex(/^[a-f0-9]{64}$/),
});

export const toolInvokeTransportSchema = z.strictObject({
  siteSourceBundle: siteSourceBundleSchema.optional(),
});

export const toolInvokeRequestSchema = z.looseObject({
  toolName: z.string(),
  input: jsonValueSchema,
  idempotencyKey: z.string().optional(),
  context: toolInvokeContextSchema.optional(),
  actor: actorContextSchema.optional(),
  transport: toolInvokeTransportSchema.optional(),
  executionMode: z.enum(ExecutionMode).optional(),
  requiresUserPresence: z.boolean().optional(),
  privacyClass: z.string().optional(),
  sessionID: z.string().optional(),
  parentJobID: z.string().optional(),
  grantID: z.string().optional(),
  resourceScope: resourceScopeSchema.optional(),
  timeoutSecond: nonNegativeIntegerSchema.optional(),
});

export const toolInvokeResponseSchema = z.strictObject({
  provider: z.string(),
  selectedBackend: z.string(),
  toolName: z.string(),
  outcome: z.enum(ToolOutcome).optional(),
  effects: z.array(resourceEffectSchema).optional(),
  status: z.string().optional(),
  content: z.string().optional(),
  isError: z.boolean().optional(),
  message: z.string().optional(),
  errorCode: z.string().optional(),
  failureStage: z.string().optional(),
  retryable: z.boolean().optional(),
  safeRetry: z.boolean().optional(),
  result: jsonValueSchema,
});

export const approvalTargetSchema = z.strictObject({
  inputField: z.string().optional(),
  id: z.string().optional(),
  title: z.string().optional(),
  startsAt: z.string().optional(),
  preview: z.string().optional(),
});

export type ApprovalTarget = z.infer<typeof approvalTargetSchema>;
export type CapabilityDescriptor = z.infer<typeof capabilityDescriptorSchema>;
export type SiteSourceBundle = z.infer<typeof siteSourceBundleSchema>;
export type ToolInvokeTransport = z.infer<typeof toolInvokeTransportSchema>;
export type ToolInvokeRequest = z.infer<typeof toolInvokeRequestSchema>;
export type ToolInvokeResponse = z.infer<typeof toolInvokeResponseSchema>;
