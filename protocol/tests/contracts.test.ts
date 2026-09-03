import { describe, expect, test } from 'bun:test';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

import {
  askInteractionSchema,
  ledgerEventNameSchema,
  TaskEventName,
  ToolTaskEventSuffix,
  toolTaskEventPrefix,
  ChatCompletionFinishReason,
  chatCompletionMessageSchema,
  chatCompletionRequestSchema,
  chatCompletionResponseSchema,
  capabilityDescriptorSchema,
  languageModelMessagePartSchema,
  languageModelMessageSchema,
  capabilityRegistryResponseSchema,
  protocolIdentitySchema,
  protocolSchemas,
  structuredResponseSchema,
  StructuredOutputDiagnosticCategory,
  StructuredOutputRepairStatus,
  StructuredOutputValidationCode,
  structuredOutputDiagnosticSchema,
  structuredOutputSchemaSchema,
  toolInvokeRequestSchema,
  toolInvokeResponseSchema,
  type ProtocolSchemaName,
} from '../src/index.ts';

const fixturesDirectory = fileURLToPath(new URL('../fixtures/', import.meta.url));

const fixtureSchemaNames = {
  'agent-action': 'agent-action',
  'agent-message': 'agent-message',
  'approval-target': 'approval-target',
  'capability-descriptor': 'capability-descriptor',
  'capability-registry-response': 'capability-registry-response',
  'chat-completion-request': 'chat-completion-request',
  'chat-completion-response': 'chat-completion-response',
  'connector-runtime-result': 'connector-runtime-result',
  'platform-auto-resume-event': 'platform-inbound-event',
  'platform-inbound-event': 'platform-inbound-event',
  'structured-response': 'structured-response',
  'structured-response-request': 'structured-response-request',
  'task-artifact': 'task-artifact',
  'task-attempt': 'task-attempt',
  'task-event': 'task-event',
  'task-run': 'task-run',
  'task-schedule': 'task-schedule',
  'task-schedule-interval': 'task-schedule',
  'task-schedule-once': 'task-schedule',
  'tool-invoke-request': 'tool-invoke-request',
  'tool-invoke-response': 'tool-invoke-response',
} satisfies Record<string, ProtocolSchemaName>;

describe('closed protocol values', () => {
  test('requires canonical protocol identity on registry responses', () => {
    const identity = {
      protocolVersion: '0.4.0',
      aggregateProtocolHash: 'a'.repeat(64),
    };
    const registryResponse = {
      ...identity,
      localOnly: true,
      routingCandidates: null,
    };

    expect(protocolIdentitySchema.safeParse(identity).success).toBe(true);
    expect(capabilityRegistryResponseSchema.safeParse(registryResponse).success).toBe(true);
    expect(protocolIdentitySchema.safeParse({ ...identity, protocolVersion: '   ' }).success).toBe(false);
    expect(protocolIdentitySchema.safeParse({ ...identity, aggregateProtocolHash: 'A'.repeat(64) }).success).toBe(false);
    expect(protocolIdentitySchema.safeParse({ ...identity, extra: true }).success).toBe(false);
    expect(capabilityRegistryResponseSchema.safeParse({ ...registryResponse, protocolVersion: undefined }).success).toBe(false);
  });

  test('requires strict structured output schema requests', () => {
    const closedRequest = {
      name: 'structured.enum-result',
      document: {
        type: 'object',
        properties: { state: { enum: ['ready', 'blocked'] } },
        required: ['state'],
        additionalProperties: false,
      },
      isStrictlyEnforced: true,
    };

    expect(structuredOutputSchemaSchema.safeParse(closedRequest).success).toBe(true);
    expect(structuredOutputSchemaSchema.safeParse({ ...closedRequest, name: 'structured result' }).success).toBe(false);
    expect(structuredOutputSchemaSchema.safeParse({ ...closedRequest, name: 'structured/result' }).success).toBe(false);
    expect(structuredOutputSchemaSchema.safeParse({ ...closedRequest, isStrictlyEnforced: false }).success).toBe(false);
    expect(structuredOutputSchemaSchema.safeParse({
      ...closedRequest,
      document: { type: 'object', properties: {} },
    }).success).toBe(false);
  });

  test('keeps trusted site source transport outside model input', () => {
    const request = {
      toolName: 'site_serve',
      input: { title: 'Site 1', sourceWorkspacePath: '~/sites/site-1', mode: 'publish' },
      transport: {
        siteSourceBundle: {
          workspacePath: '~/sites/site-1',
          contentBase64: 'YnVuZGxl',
          format: 'tar.gz',
          sha256: 'a'.repeat(64),
        },
      },
    };

    expect(toolInvokeRequestSchema.safeParse(request).success).toBe(true);
    expect(toolInvokeRequestSchema.safeParse({
      ...request,
      transport: {
        siteSourceBundle: {
          ...request.transport.siteSourceBundle,
          format: 'zip',
        },
      },
    }).success).toBe(false);
  });

  test('keeps structured output diagnostics closed and content-free', () => {
    expect(structuredOutputDiagnosticSchema.parse({
      category: StructuredOutputDiagnosticCategory.SchemaValidation,
      toolName: 'task_add',
      validationIssues: [
        { fieldPath: '/prompt', code: StructuredOutputValidationCode.Required },
        { fieldPath: '/', code: StructuredOutputValidationCode.AdditionalProperty },
      ],
      repairStatus: StructuredOutputRepairStatus.Failed,
    })).toEqual({
      category: StructuredOutputDiagnosticCategory.SchemaValidation,
      toolName: 'task_add',
      validationIssues: [
        { fieldPath: '/prompt', code: StructuredOutputValidationCode.Required },
        { fieldPath: '/', code: StructuredOutputValidationCode.AdditionalProperty },
      ],
      repairStatus: StructuredOutputRepairStatus.Failed,
    });
    expect(structuredOutputDiagnosticSchema.safeParse({
      category: StructuredOutputDiagnosticCategory.FinishReason,
      finishReason: ChatCompletionFinishReason.Length,
    }).success).toBe(true);
    expect(structuredOutputDiagnosticSchema.safeParse({ category: 'provider_message' }).success).toBe(false);
    expect(structuredOutputDiagnosticSchema.safeParse({
      category: StructuredOutputDiagnosticCategory.SchemaValidation,
      finishReason: 'stop',
    }).success).toBe(false);
    expect(structuredOutputDiagnosticSchema.safeParse({
      category: StructuredOutputDiagnosticCategory.JSONParse,
      content: 'generated text',
    }).success).toBe(false);
    expect(structuredOutputDiagnosticSchema.safeParse({
      category: StructuredOutputDiagnosticCategory.SchemaValidation,
      validationIssues: [{ fieldPath: '/prompt', code: 'raw_provider_message' }],
    }).success).toBe(false);
    expect(structuredOutputDiagnosticSchema.safeParse({
      category: StructuredOutputDiagnosticCategory.SchemaValidation,
      validationIssues: [{ fieldPath: '/prompt contains user text', code: StructuredOutputValidationCode.Required }],
    }).success).toBe(false);
    expect(structuredOutputDiagnosticSchema.safeParse({
      category: StructuredOutputDiagnosticCategory.SchemaValidation,
      toolName: 'task_add with user content',
    }).success).toBe(false);
    expect(structuredOutputDiagnosticSchema.safeParse({
      category: StructuredOutputDiagnosticCategory.SchemaValidation,
      repairStatus: 'retried_with_fallback',
    }).success).toBe(false);
  });

  test('rejects values outside canonical enums', () => {
    expect(languageModelMessageSchema.safeParse({ role: 'developer' }).success).toBe(false);
    expect(languageModelMessagePartSchema.safeParse({ type: 'audio' }).success).toBe(false);
    expect(chatCompletionMessageSchema.safeParse({ role: 'developer' }).success).toBe(false);
    expect(chatCompletionMessageSchema.safeParse({
      role: 'assistant',
      toolCalls: [{
        id: 'call-1',
        type: 'custom',
        function: { name: 'lookup', arguments: '{}' },
      }],
    }).success).toBe(false);
    expect(chatCompletionRequestSchema.safeParse({
      executionMode: 'invalid',
      messages: [],
      parallelToolCalls: false,
    }).success).toBe(false);
    expect(chatCompletionRequestSchema.safeParse({
      executionMode: 'auto',
      messages: [],
      parallelToolCalls: false,
      generationOptions: { seed: 41, temperature: 0.2, maxTokens: 256 },
    }).success).toBe(true);
    expect(chatCompletionRequestSchema.safeParse({
      executionMode: 'auto',
      messages: [],
      parallelToolCalls: false,
      generationOptions: { maxTokens: -1 },
    }).success).toBe(false);
    expect(chatCompletionResponseSchema.safeParse({
      finishReason: 'paused',
      provider: 'provider',
      model: 'model',
      message: { role: 'assistant', content: 'done' },
      selectedBackend: 'remote',
    }).success).toBe(false);
    expect(chatCompletionResponseSchema.safeParse({
      finishReason: 'stop',
      provider: '',
      model: 'model',
      message: { role: 'assistant', content: 'done' },
      selectedBackend: 'remote',
    }).success).toBe(false);
    expect(chatCompletionResponseSchema.safeParse({
      finishReason: 'stop',
      provider: 'provider',
      model: 'model',
      message: { role: 'assistant', content: 'done' },
      selectedBackend: 'companion',
    }).success).toBe(false);
    expect(structuredResponseSchema.safeParse({
      provider: 'openrouter',
      model: 'model',
      content: '{}',
      selectedBackend: 'remote',
      finishReason: 'stop',
      constraintMode: 'unknown',
    }).success).toBe(false);
    expect(structuredResponseSchema.safeParse({
      provider: '',
      model: '',
      content: '',
      selectedBackend: '',
      finishReason: 'length',
    }).success).toBe(false);
    expect(askInteractionSchema.safeParse({ interactionID: 'ask-1', taskRunID: 'task-1', kind: 'choice' }).success).toBe(false);
    expect(askInteractionSchema.safeParse({ interactionID: 'ask-1', taskRunID: 'task-1', kind: 'ask_choice_single' }).success).toBe(false);
    expect(askInteractionSchema.safeParse({ interactionID: 'ask-1', taskRunID: 'task-1', kind: 'ask_choice_multiple' }).success).toBe(false);
    expect(askInteractionSchema.safeParse({
      interactionID: 'ask-1',
      taskRunID: 'task-1',
      kind: 'ask_input',
      selectionMode: 'any',
    }).success).toBe(false);
    expect(capabilityDescriptorSchema.safeParse({
      name: 'calendar_add',
      version: '1',
      privacyClass: 'workspace_calendar',
      estimatedLatency: 'instant',
      requiresUserPresence: false,
      worksOffline: false,
    }).success).toBe(false);
    expect(toolInvokeResponseSchema.safeParse({
      provider: 'internkim',
      selectedBackend: 'device',
      toolName: 'task_add',
      outcome: 'complete',
      result: { taskID: 'task-1' },
    }).success).toBe(false);
    expect(toolInvokeResponseSchema.safeParse({
      provider: 'internkim',
      selectedBackend: 'device',
      toolName: 'task_add',
      outcome: 'succeeded',
      effects: [{ objectType: 'task', effect: 'created' }],
      result: { taskID: 'task-1' },
    }).success).toBe(false);
  });

  test('keeps capability schemas recursively strict', async () => {
    const fixtures = await readFixtureBundle('valid');
    const descriptor = capabilityDescriptorSchema.parse(fixtures['capability-descriptor']?.[0]);
    expect(capabilityDescriptorSchema.safeParse({ ...descriptor, name: '' }).success).toBe(false);
    expect(capabilityDescriptorSchema.safeParse({ ...descriptor, canonicalName: ` ${descriptor.canonicalName}` }).success).toBe(false);
    expect(capabilityDescriptorSchema.safeParse({ ...descriptor, availability: { state: 'not_ready', reason: 'connection pending' } }).success).toBe(true);
    expect(capabilityDescriptorSchema.safeParse({ ...descriptor, inputSchemaStrict: false }).success).toBe(false);
    expect(capabilityDescriptorSchema.safeParse({
      ...descriptor,
      inputSchema: {
        type: 'object',
        properties: {
          nested: { type: 'object', properties: {} },
        },
        additionalProperties: false,
      },
    }).success).toBe(false);
    expect(capabilityDescriptorSchema.safeParse({
      ...descriptor,
      inputIntentSchema: undefined,
    }).success).toBe(false);
    expect(capabilityDescriptorSchema.safeParse({
      ...descriptor,
      inputIntentSchema: descriptor.inputSchema,
    }).success).toBe(false);
    expect(capabilityDescriptorSchema.safeParse({
      ...descriptor,
      inputIntentSchema: {
        type: 'object',
        properties: {
          nested: { type: 'object', properties: {} },
        },
        additionalProperties: false,
      },
    }).success).toBe(false);
    expect(capabilityDescriptorSchema.safeParse({
      ...descriptor,
      inputIntentSchema: {
        type: 'object',
        properties: {
          unexpected: { type: 'string' },
        },
        additionalProperties: false,
      },
    }).success).toBe(false);
    expect(capabilityDescriptorSchema.safeParse({
      ...descriptor,
      sideEffectClass: 'read',
      sideEffect: 'read',
      inputIntentSchema: undefined,
    }).success).toBe(true);
    expect(capabilityDescriptorSchema.safeParse({
      ...descriptor,
      resultContract: {
        schema: {
          type: 'object',
          properties: { taskID: { type: 'string' } },
          required: ['taskID'],
          additionalProperties: false,
        },
        effects: [{
          objectType: 'task',
          effect: 'created',
          resultField: 'taskID',
          effectIdentity: 'id',
        }],
      },
    }).success).toBe(true);
    expect(capabilityDescriptorSchema.safeParse({
      ...descriptor,
      resultContract: {
        schema: {
          type: 'object',
          properties: {
            paths: {
              type: 'array',
              items: { type: 'string' },
              minItems: 1,
              uniqueItems: true,
            },
          },
          required: ['paths'],
          additionalProperties: false,
        },
        effects: [{
          objectType: 'file',
          effect: 'updated',
          resultField: 'paths',
          effectIdentity: 'path',
        }],
      },
    }).success).toBe(true);
    for (const identityProperty of [
      { type: 'array', items: { type: 'string' }, uniqueItems: true },
      { type: 'array', items: { type: 'string' }, minItems: 1 },
      { type: 'array', items: { type: 'number' }, minItems: 1, uniqueItems: true },
      { type: 'array', items: { type: 'string' }, minItems: '1', uniqueItems: true },
    ]) {
      expect(capabilityDescriptorSchema.safeParse({
        ...descriptor,
        resultContract: {
          schema: {
            type: 'object',
            properties: { paths: identityProperty },
            required: ['paths'],
            additionalProperties: false,
          },
          effects: [{
            objectType: 'file',
            effect: 'updated',
            resultField: 'paths',
            effectIdentity: 'path',
          }],
        },
      }).success).toBe(false);
    }
    expect(capabilityDescriptorSchema.safeParse({
      ...descriptor,
      resultContract: {
        schema: {
          type: 'object',
          properties: { taskID: { type: 'string' } },
          required: ['taskID'],
          additionalProperties: false,
        },
        effects: [{
          objectType: 'task',
          effect: 'created',
          resultField: 'missingID',
          effectIdentity: 'id',
        }],
      },
    }).success).toBe(false);
    const reviewResultContract = {
      schema: {
        type: 'object',
        properties: { passed: { type: 'boolean' } },
        required: ['passed'],
        additionalProperties: false,
      },
      effects: [],
      evidenceCondition: { resultField: 'passed', equals: true },
    };
    expect(capabilityDescriptorSchema.safeParse({
      ...descriptor,
      resultContract: reviewResultContract,
    }).success).toBe(true);
    expect(capabilityDescriptorSchema.safeParse({
      ...descriptor,
      resultContract: {
        ...reviewResultContract,
        evidenceCondition: { resultField: 'missing', equals: true },
      },
    }).success).toBe(false);
    expect(capabilityDescriptorSchema.safeParse({
      ...descriptor,
      resultContract: {
        ...reviewResultContract,
        evidenceCondition: { resultField: 'passed', equals: 'true' },
      },
    }).success).toBe(false);
    expect(capabilityDescriptorSchema.safeParse({
      ...descriptor,
      resultContract: {
        ...reviewResultContract,
        schema: {
          ...reviewResultContract.schema,
          required: [],
        },
      },
    }).success).toBe(false);
  });

  test('enforces chat completion response semantics', () => {
    const toolCall = {
      id: 'call-1',
      type: 'function',
      function: { name: 'lookup', arguments: '{"city":"Seoul"}' },
    };
    const responseDocument = {
      provider: 'openrouter',
      model: 'model',
      message: { role: 'assistant', content: 'done' },
      selectedBackend: 'remote',
    };
    for (const finishReason of ['stop', 'length', 'content_filter', 'error', 'other', 'unknown']) {
      expect(chatCompletionResponseSchema.safeParse({ ...responseDocument, finishReason }).success).toBe(true);
    }
    expect(chatCompletionResponseSchema.safeParse({
      ...responseDocument,
      finishReason: 'tool_calls',
      message: { ...responseDocument.message, content: '', toolCalls: [toolCall] },
    }).success).toBe(true);
    expect(chatCompletionResponseSchema.safeParse({
      ...responseDocument,
      message: { ...responseDocument.message, role: 'tool' },
    }).success).toBe(false);
    expect(chatCompletionResponseSchema.safeParse({
      ...responseDocument,
      finishReason: 'tool_calls',
    }).success).toBe(false);
    expect(chatCompletionResponseSchema.safeParse({
      ...responseDocument,
      message: {
        ...responseDocument.message,
        toolCalls: [{ ...toolCall, id: ' ' }],
      },
    }).success).toBe(false);
    expect(chatCompletionResponseSchema.safeParse({
      ...responseDocument,
      message: {
        ...responseDocument.message,
        toolCalls: [{ ...toolCall, function: { ...toolCall.function, name: '' } }],
      },
    }).success).toBe(false);
    expect(chatCompletionResponseSchema.safeParse({
      ...responseDocument,
      message: {
        ...responseDocument.message,
        toolCalls: [{ ...toolCall, function: { ...toolCall.function, arguments: '[]' } }],
      },
    }).success).toBe(false);
    expect(chatCompletionResponseSchema.safeParse({
      ...responseDocument,
      message: {
        ...responseDocument.message,
        toolCalls: [{ ...toolCall, function: { ...toolCall.function, arguments: '{invalid' } }],
      },
    }).success).toBe(false);
    expect(chatCompletionResponseSchema.safeParse({ ...responseDocument, finishReason: 'paused' }).success).toBe(false);
  });

  test('rejects chat identity collisions at the protocol boundary', () => {
    const requestDocument = {
      executionMode: 'auto',
      messages: [{ role: 'user', content: 'Use a tool.' }],
      parallelToolCalls: false,
    };
    const tool = {
      type: 'function',
      function: { name: 'lookup', parameters: { type: 'object' } },
    };
    expect(chatCompletionRequestSchema.safeParse({
      ...requestDocument,
      tools: [tool, { ...tool, function: { ...tool.function, name: ' lookup ' } }],
    }).success).toBe(false);
    expect(chatCompletionRequestSchema.safeParse({
      ...requestDocument,
      tools: [{ ...tool, function: { ...tool.function, name: ' ' } }],
    }).success).toBe(false);
    expect(chatCompletionRequestSchema.safeParse({
      ...requestDocument,
      messages: [
        { role: 'assistant', toolCalls: [{ id: 'call-1', type: 'function', function: { name: 'lookup', arguments: '{}' } }] },
        { role: 'assistant', toolCalls: [{ id: 'call-1', type: 'function', function: { name: 'other', arguments: '{}' } }] },
      ],
    }).success).toBe(false);
    expect(chatCompletionRequestSchema.safeParse({
      ...requestDocument,
      messages: [{ role: 'tool', toolCallId: ' ', content: 'result' }],
    }).success).toBe(false);

    expect(chatCompletionResponseSchema.safeParse({
      finishReason: 'tool_calls',
      provider: 'provider',
      model: 'model',
      message: {
        role: 'assistant',
        toolCalls: [
          { id: 'call-1', type: 'function', function: { name: 'lookup', arguments: '{}' } },
          { id: 'call-1', type: 'function', function: { name: 'other', arguments: '{}' } },
        ],
      },
      selectedBackend: 'remote',
    }).success).toBe(false);
  });
});

describe('ledger event names', () => {
  test('accept every declared fixed name', () => {
    for (const name of Object.values(TaskEventName)) {
      expect(ledgerEventNameSchema.safeParse(name).success).toBe(true);
    }
  });

  test('accept a tool event for any tool, so a new tool needs no protocol change', () => {
    for (const suffix of Object.values(ToolTaskEventSuffix)) {
      expect(ledgerEventNameSchema.safeParse(`${toolTaskEventPrefix}a_tool_nobody_has_written_yet${suffix}`).success).toBe(true);
    }
  });

  test('refuse a tool event whose tool name is not one segment or whose suffix is not declared', () => {
    expect(ledgerEventNameSchema.safeParse('tool.site.app.publish.result').success).toBe(false);
    expect(ledgerEventNameSchema.safeParse('tool.shell.finished').success).toBe(false);
    expect(ledgerEventNameSchema.safeParse('tool..result').success).toBe(false);
  });

  test('refuse a name no producer declares', () => {
    expect(ledgerEventNameSchema.safeParse('approval.granted').success).toBe(false);
  });
});

describe('protocol fixtures', () => {
  test('accepts every valid fixture', async () => {
    const fixtures = await readFixtureBundle('valid');
    expect(Object.keys(fixtures).sort(compareCodeUnits)).toEqual(Object.keys(fixtureSchemaNames).sort(compareCodeUnits));
    for (const [fixtureName, documents] of Object.entries(fixtures)) {
      const schema = schemaForFixture(fixtureName);
      for (const document of documents) expect(schema.safeParse(document).success).toBe(true);
    }
  });

  test('rejects every invalid fixture', async () => {
    const fixtures = await readFixtureBundle('invalid');
    for (const [fixtureName, documents] of Object.entries(fixtures)) {
      const schema = schemaForFixture(fixtureName);
      for (const document of documents) expect(schema.safeParse(document).success).toBe(false);
    }
  });
});

async function readFixtureBundle(kind: 'valid' | 'invalid'): Promise<Record<string, unknown[]>> {
  return JSON.parse(await readFile(`${fixturesDirectory}/${kind}.json`, 'utf8'));
}

function compareCodeUnits(left: string, right: string): number {
  if (left < right) return -1;
  if (left > right) return 1;
  return 0;
}

function schemaForFixture(fixtureName: string) {
  const schemaName = fixtureSchemaNames[fixtureName as keyof typeof fixtureSchemaNames];
  if (!schemaName) throw new Error(`Fixture does not name a protocol schema: ${fixtureName}`);
  return protocolSchemas[schemaName];
}
