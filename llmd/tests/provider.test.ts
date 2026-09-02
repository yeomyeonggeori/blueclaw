import { describe, expect, spyOn, test } from 'bun:test';
import { createOpenAICompatible } from '@ai-sdk/openai-compatible';
import type { LanguageModelV3GenerateResult, LanguageModelV3StreamPart, LanguageModelV3Usage } from '@ai-sdk/provider';
import { APICallError, RetryError } from 'ai';
import { createOpenRouter } from '@openrouter/ai-sdk-provider';
import {
  ChatCompletionFinishReason,
  ChatCompletionMessageRole,
  ExecutionMode,
  LanguageModelBackend,
  LanguageModelMessageRole,
  StructuredOutputConstraintMode,
  StructuredOutputDiagnosticCategory,
  StructuredOutputRepairStatus,
  StructuredOutputValidationCode,
  chatCompletionRequestSchema,
  type ChatCompletionRequest,
  type StructuredResponseRequest,
} from '@blueclaw/protocol';
import { MockLanguageModelV3, simulateReadableStream } from 'ai/test';

import { LLMDStructuredOutputMode, LLMDAutoRoute, type LLMDConfiguration } from '../src/configuration.ts';
import {
  createChatCompletionGenerator,
  createStructuredResponseGenerator,
  type ProviderLanguageModelFactory,
} from '../src/provider.ts';

const structuredRequest: StructuredResponseRequest = {
  executionMode: ExecutionMode.Auto,
  model: 'remote-model',
  messages: [{ role: LanguageModelMessageRole.User, content: 'Return ok.' }],
  structuredOutputSchema: {
    name: 'provider_test_output',
    document: {
      type: 'object',
      properties: { ok: { type: 'boolean' } },
      required: ['ok'],
      additionalProperties: false,
    },
    isStrictlyEnforced: true,
  },
  generationOptions: {
    maxTokens: 128,
    seed: 7,
    temperature: 0,
  },
};

const chatRequest: ChatCompletionRequest = {
  executionMode: ExecutionMode.Auto,
  model: 'remote-model',
  messages: [
    { role: ChatCompletionMessageRole.System, content: 'You are concise.' },
    { role: ChatCompletionMessageRole.User, content: 'Look up the answer.' },
    {
      role: ChatCompletionMessageRole.Assistant,
      toolCalls: [{ id: 'call-1', type: 'function', function: { name: 'lookup', arguments: '{"key":"value"}' } }],
    },
    { role: ChatCompletionMessageRole.Tool, toolCallId: 'call-1', content: '{"answer":42}' },
  ],
  tools: [{
    type: 'function',
    function: {
      name: 'lookup',
      description: 'Look up a value.',
      parameters: { type: 'object', properties: { key: { type: 'string' } } },
    },
  }],
  toolChoice: { type: 'function', function: { name: 'lookup' } },
  parallelToolCalls: false,
  generationOptions: { maxTokens: 128, seed: 7, temperature: 0 },
};

describe('llmd provider adapter', () => {
  test('asks a schema-constrained provider for the object itself, with no tool', async () => {
    const localModel = jsonTextLanguageModel('unused-local-model', { ok: true });
    const remoteModel = jsonTextLanguageModel('served-remote-model', { ok: true });
    const generateStructuredResponse = createStructuredResponseGenerator(
      nativeStructuredConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(localModel, remoteModel),
    );

    const response = await generateStructuredResponse(structuredRequest);

    expect(response.content).toBe('{"ok":true}');
    expect(response.constraintMode).toBe(StructuredOutputConstraintMode.OpenAIJSONSchema);
    expect(remoteModel.doStreamCalls[0]?.tools ?? []).toHaveLength(0);
    expect(remoteModel.doStreamCalls[0]?.toolChoice).toBeUndefined();
  });

  test('falls back to a forced tool call when the provider rejects the response format', async () => {
    const localModel = jsonTextLanguageModel('unused-local-model', { ok: true });
    let remoteAttempt = 0;
    const remoteModel = new MockLanguageModelV3({
      modelId: 'served-remote-model',
      doGenerate: async () => successfulGeneration('served-remote-model', { ok: true }, defaultUsage()),
      doStream: async () => {
        remoteAttempt += 1;
        if (remoteAttempt === 1) throw new Error('response_format json_schema is not supported by this model');
        return streamResultFromGeneration(successfulGeneration('served-remote-model', { ok: true }, defaultUsage()));
      },
    });
    const generateStructuredResponse = createStructuredResponseGenerator(
      nativeStructuredConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(localModel, remoteModel),
    );

    const response = await generateStructuredResponse(structuredRequest);

    expect(response.content).toBe('{"ok":true}');
    expect(response.constraintMode).toBe(StructuredOutputConstraintMode.NativeToolCall);
    expect(remoteAttempt).toBe(2);
  });

  test('carries canonical tool names through the history unchanged', async () => {
    const remoteModel = toolCallLanguageModel('served-remote-model', [{
      toolName: 'task_add',
      input: '{"title":"second turn"}',
    }]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(chatLanguageModel('unused-local-model'), remoteModel),
    );
    const request = multipleToolChatRequest();
    request.messages = [
      ...request.messages,
      {
        role: ChatCompletionMessageRole.Assistant,
        content: '',
        toolCalls: [{ id: 'call-1', type: 'function', function: { name: 'task_add', arguments: '{"title":"first turn"}' } }],
      },
      { role: ChatCompletionMessageRole.Tool, toolCallId: 'call-1', content: 'created' },
    ];

    const response = await generateChatCompletion(request);

    // Tool names are already provider-legal, so nothing rewrites them on the way
    // out and nothing has to guess the way back on the way in.
    const sentPrompt = JSON.stringify(remoteModel.doStreamCalls[0]?.prompt);
    expect(sentPrompt).toContain('task_add');
    expect(response.message.toolCalls?.[0]?.function.name).toBe('task_add');
  });

  test('accepts a canonical strict tool schema and answers with the canonical name', async () => {
    const descriptor = {
      name: 'document_read',
      description: 'Read a document from the workspace.',
      inputSchema: {
        type: 'object',
        properties: { path: { type: 'string' } },
        required: ['path'],
        additionalProperties: false,
      },
    };
    const fallbackModel = chatLanguageModel('unused-local-model');
    const remoteModel = toolCallLanguageModel('served-remote-model', [{
      toolName: 'document_read',
      input: '{"path":"/workspace/documents/review.docx"}',
    }]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(fallbackModel, remoteModel),
    );

    const request = chatCompletionRequestSchema.parse({
      ...chatRequest,
      messages: [{ role: ChatCompletionMessageRole.User, content: 'Read the document.' }],
      tools: [{
        type: 'function',
        function: {
          name: descriptor.name,
          description: descriptor.description,
          parameters: descriptor.inputSchema,
        },
      }],
      toolChoice: 'required',
    });
    const response = await generateChatCompletion(request);

    // OpenAI-family endpoints reject a function name containing a dot, so the
    // namespace separator goes out sanitized and comes back canonical.
    expect(remoteModel.doStreamCalls[0]?.tools?.map(tool => tool.name)).toEqual(['document_read']);
    expect(response.message.toolCalls?.[0]?.function.name).toBe('document_read');
    expect(remoteModel.doStreamCalls).toHaveLength(1);
    expect(fallbackModel.doStreamCalls).toHaveLength(0);
  });

  test('generates chat completions with native tools and provider metadata', async () => {
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = chatLanguageModel('served-remote-model');
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateChatCompletion(chatRequest);
    const call = remoteModel.doStreamCalls[0];

    expect(response).toEqual({
      provider: 'openrouter',
      model: 'served-remote-model',
      message: {
        role: ChatCompletionMessageRole.Assistant,
        content: '',
        toolCalls: [{
          id: 'call-2',
          type: 'function',
          function: { name: 'lookup', arguments: '{"key":"result"}' },
        }],
      },
      selectedBackend: LanguageModelBackend.Remote,
      finishReason: ChatCompletionFinishReason.ToolCalls,
      usage: { promptTokens: 10, completionTokens: 5, totalTokens: 15 },
      providerMetadata: { openrouter: { trace: 'test' } },
    });
    expect(call?.tools?.map(tool => tool.name)).toEqual(['lookup']);
    expect(call?.toolChoice).toEqual({ type: 'tool', toolName: 'lookup' });
    expect(call?.providerOptions).toBeUndefined();
    expect(call?.maxOutputTokens).toBe(128);
    expect(call?.seed).toBe(7);
    expect(call?.temperature).toBe(0);
    expect(JSON.stringify(call?.prompt)).toContain('call-1');
    expect(JSON.stringify(call?.prompt)).toContain('answer');
    expect(llamaModel.doStreamCalls).toHaveLength(0);
  });

  test('marks the sole leading system message with a cache control breakpoint', async () => {
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = chatLanguageModel('served-remote-model');
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    await generateChatCompletion(chatRequest);
    const call = remoteModel.doStreamCalls[0];
    const systemPromptMessages = (call?.prompt ?? []).filter(message => message.role === 'system');

    expect(systemPromptMessages).toHaveLength(1);
    expect(systemPromptMessages[0]).toMatchObject({
      role: 'system',
      content: 'You are concise.',
      providerOptions: { openrouter: { cacheControl: { type: 'ephemeral' } } },
    });
  });

  test('caches only the last leading system message and still delivers trailing system content', async () => {
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = chatLanguageModel('served-remote-model');
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    await generateChatCompletion({
      ...chatRequest,
      messages: [
        { role: ChatCompletionMessageRole.System, content: 'Static base instruction.' },
        { role: ChatCompletionMessageRole.System, content: 'Static context text.' },
        { role: ChatCompletionMessageRole.User, content: 'Do the task.' },
        { role: ChatCompletionMessageRole.System, content: 'Trailing volatile note.' },
      ],
    });
    const call = remoteModel.doStreamCalls[0];
    const systemPromptMessages = (call?.prompt ?? []).filter(message => message.role === 'system');

    expect(systemPromptMessages).toHaveLength(3);
    expect(systemPromptMessages[0]).toMatchObject({ content: 'Static base instruction.' });
    expect(systemPromptMessages[0]?.providerOptions).toBeUndefined();
    expect(systemPromptMessages[1]).toMatchObject({
      content: 'Static context text.',
      providerOptions: { openrouter: { cacheControl: { type: 'ephemeral' } } },
    });
    expect(systemPromptMessages[2]).toMatchObject({ content: 'Trailing volatile note.' });
    expect(systemPromptMessages[2]?.providerOptions).toBeUndefined();
  });

  test('carries the structured route cache control breakpoint to the last leading system message', async () => {
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = sequencedStructuredToolCallLanguageModel('served-remote-model', ['{"ok":true}']);
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    await generateStructuredResponse({
      ...structuredRequest,
      messages: [
        { role: LanguageModelMessageRole.System, content: 'Static base instruction.' },
        { role: LanguageModelMessageRole.System, content: 'Static context text.' },
        { role: LanguageModelMessageRole.User, content: 'Do the task.' },
      ],
    });
    const call = remoteModel.doStreamCalls[0];
    const systemPromptMessages = (call?.prompt ?? []).filter(message => message.role === 'system');

    expect(systemPromptMessages).toHaveLength(2);
    expect(systemPromptMessages[0]?.providerOptions).toBeUndefined();
    expect(systemPromptMessages[1]).toMatchObject({
      content: 'Static context text.',
      providerOptions: { openrouter: { cacheControl: { type: 'ephemeral' } } },
    });
  });

  test('writes the cache control breakpoint on the wire request to the intended message only', async () => {
    const requestBodies: Array<Record<string, unknown>> = [];
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      wireLanguageModelFactory(requestBodies),
    );

    await generateChatCompletion({
      ...chatRequest,
      executionMode: ExecutionMode.Remote,
      messages: [
        { role: ChatCompletionMessageRole.System, content: 'Static base instruction.' },
        { role: ChatCompletionMessageRole.System, content: 'Static context text.' },
        { role: ChatCompletionMessageRole.User, content: 'Do the task.' },
      ],
      tools: [],
      toolChoice: 'auto',
    });

    expect(requestBodies).toHaveLength(1);
    const wireMessages = requestBodies[0]?.messages;
    if (!Array.isArray(wireMessages)) throw new Error('wire request messages must be an array');
    const wireSystemMessages = wireMessages.filter(isWireSystemMessage);

    expect(wireSystemMessages).toHaveLength(2);
    expect(wireSystemMessages[0]?.content?.[0]?.cache_control).toBeUndefined();
    expect(wireSystemMessages[1]?.content?.[0]?.cache_control).toEqual({ type: 'ephemeral' });
  });

  test('rejects text when a tool call is required without fallback', async () => {
    const fallbackModel = chatLanguageModel('unused-local-model');
    const remoteModel = textLanguageModel('served-remote-model');
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(fallbackModel, remoteModel),
    );

    await expect(generateChatCompletion({
      ...chatRequest,
      toolChoice: 'required',
    })).rejects.toMatchObject({
      code: 'structured_output_invalid',
      status: 422,
      allowLegacyFallback: false,
      diagnostic: {
        category: StructuredOutputDiagnosticCategory.FinishReason,
        finishReason: ChatCompletionFinishReason.Stop,
      },
    });
    expect(remoteModel.doStreamCalls).toHaveLength(1);
    expect(fallbackModel.doStreamCalls).toHaveLength(0);
  });

  test('reports an empty completion separately from a text finish reason', async () => {
    const fallbackModel = chatLanguageModel('unused-local-model');
    const remoteModel = emptyLanguageModel('served-remote-model');
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(fallbackModel, remoteModel),
    );

    await expect(generateChatCompletion({
      ...chatRequest,
      toolChoice: 'required',
    })).rejects.toMatchObject({
      code: 'structured_output_invalid',
      status: 422,
      allowLegacyFallback: false,
      diagnostic: {
        category: StructuredOutputDiagnosticCategory.EmptyCompletion,
        finishReason: ChatCompletionFinishReason.Stop,
      },
    });
    expect(remoteModel.doStreamCalls).toHaveLength(1);
  });

  test('sends user image parts through the native chat path', async () => {
    const fallbackModel = chatLanguageModel('unused-local-model');
    const remoteModel = chatLanguageModel('served-remote-model');
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(fallbackModel, remoteModel),
    );

    await generateChatCompletion({
      ...chatRequest,
      messages: [{
        role: ChatCompletionMessageRole.User,
        content: 'Read this image.',
        parts: [{ type: 'image', mimeType: 'image/png', dataBase64: 'aGVsbG8=' }],
      }],
    });

    const promptMessages = remoteModel.doStreamCalls[0]?.prompt ?? [];
    const userMessage = promptMessages.find(message => message.role === 'user');
    const userParts = Array.isArray(userMessage?.content) ? userMessage.content : [];
    expect(userParts.map(part => part.type)).toEqual(['text', 'file']);
    expect(userParts[0]).toMatchObject({ type: 'text', text: 'Read this image.' });
    expect(userParts[1]).toMatchObject({ type: 'file', mediaType: 'image/png' });
  });

  test('accepts the sole available tool when a tool call is required', async () => {
    const fallbackModel = chatLanguageModel('unused-local-model');
    const remoteModel = toolCallLanguageModel('served-remote-model', [{
      toolName: 'lookup',
      input: '{"key":"result"}',
    }]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(fallbackModel, remoteModel),
    );

    const response = await generateChatCompletion({
      ...chatRequest,
      toolChoice: 'required',
    });

    expect(response.message.toolCalls?.[0]?.function).toEqual({
      name: 'lookup',
      arguments: '{"key":"result"}',
    });
    expect(remoteModel.doStreamCalls[0]?.toolChoice).toEqual({ type: 'tool', toolName: 'lookup' });
    expect(fallbackModel.doStreamCalls).toHaveLength(0);
  });

  test('rejects a different tool when the sole available tool is required', async () => {
    const fallbackModel = chatLanguageModel('unused-local-model');
    const remoteModel = toolCallLanguageModel('wrong-tool-model', [{
      toolName: 'other',
      input: '{}',
    }]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(fallbackModel, remoteModel),
    );

    await expect(generateChatCompletion({
      ...chatRequest,
      toolChoice: 'required',
    })).rejects.toMatchObject({
      code: 'structured_output_invalid',
      status: 422,
      allowLegacyFallback: false,
      diagnostic: {
        category: StructuredOutputDiagnosticCategory.ToolCallContract,
      },
    });
    expect(remoteModel.doStreamCalls).toHaveLength(1);
    expect(fallbackModel.doStreamCalls).toHaveLength(0);
  });

  test('rejects named tool choice contract violations without fallback', async () => {
    const invalidModels = [
      textLanguageModel('text-model'),
      toolCallLanguageModel('wrong-tool-model', [{ toolName: 'other', input: '{}' }]),
      toolCallLanguageModel('multiple-tools-model', [
        { toolName: 'lookup', input: '{"key":"first"}' },
        { toolName: 'lookup', input: '{"key":"second"}' },
      ]),
    ];

    for (const remoteModel of invalidModels) {
      const fallbackModel = chatLanguageModel('unused-local-model');
      const generateChatCompletion = createChatCompletionGenerator(
        completeConfiguration(LLMDAutoRoute.RemoteFirst),
        languageModelFactory(fallbackModel, remoteModel),
      );

      await expect(generateChatCompletion(chatRequest)).rejects.toMatchObject({
        code: 'structured_output_invalid',
        status: 422,
        allowLegacyFallback: false,
      });
      expect(remoteModel.doStreamCalls).toHaveLength(1);
      expect(fallbackModel.doStreamCalls).toHaveLength(0);
    }
  });

  test('rejects schema-invalid native tool arguments without fallback', async () => {
    const request = multipleToolChatRequest();
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = toolCallLanguageModel('served-remote-model', [{ toolName: 'task_add', input: '{' }]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    try {
      await generateChatCompletion(request);
      throw new Error('expected invalid tool arguments');
    } catch (errorValue) {
      expect(errorValue).toMatchObject({
        code: 'provider_response_invalid',
        allowLegacyFallback: false,
        diagnostic: {
          category: StructuredOutputDiagnosticCategory.JSONParse,
          toolName: 'task_add',
          repairStatus: StructuredOutputRepairStatus.Failed,
        },
      });
    }
    expect(remoteModel.doStreamCalls).toHaveLength(1);
    expect(remoteModel.doGenerateCalls).toHaveLength(1);
    expect(remoteModel.doGenerateCalls[0]?.tools?.map(tool => tool.name)).toEqual(['task_add']);
    const repairPrompt = JSON.stringify(remoteModel.doGenerateCalls[0]?.prompt);
    expect(repairPrompt).toContain('Malformed arguments: {');
    expect(repairPrompt).toContain('Validation failure: tool arguments are not valid JSON');
    expect(repairPrompt).toContain('"input":{}');
    expect(repairPrompt).not.toContain('"input":"{"');
    expect(llamaModel.doGenerateCalls).toHaveLength(0);
    expect(llamaModel.doStreamCalls).toHaveLength(0);
  });

  test('repairs malformed native tool arguments with the failed tool only', async () => {
    const request = multipleToolChatRequest();
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = sequencedNamedToolCallLanguageModel('served-remote-model', 'task_add', [
      '{',
      '{"title":"repaired task"}',
    ]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateChatCompletion(request);

    expect(response.message.toolCalls?.[0]?.function).toEqual({
      name: 'task_add',
      arguments: '{"title":"repaired task"}',
    });
    expect(remoteModel.doStreamCalls).toHaveLength(1);
    expect(remoteModel.doGenerateCalls).toHaveLength(1);
    expect(remoteModel.doStreamCalls[0]?.tools?.map(tool => tool.name)).toEqual(['lookup', 'task_add']);
    expect(remoteModel.doGenerateCalls[0]?.tools?.map(tool => tool.name)).toEqual(['task_add']);
    const repairPrompt = JSON.stringify(remoteModel.doGenerateCalls[0]?.prompt);
    expect(repairPrompt).toContain('Malformed arguments: {');
    expect(repairPrompt).toContain('"input":{}');
    expect(repairPrompt).not.toContain('"input":"{"');
    expect(llamaModel.doGenerateCalls).toHaveLength(0);
    expect(llamaModel.doStreamCalls).toHaveLength(0);
  });

  test('repairs nested invalid native tool arguments once on the same route', async () => {
    const request = nestedChatRequest();
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = sequencedToolCallLanguageModel('served-remote-model', [
      '{"details":{"count":"wrong"}}',
      '{"details":{"count":2}}',
    ]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateChatCompletion(request);

    expect(response.message.toolCalls?.[0]?.function.arguments).toBe('{"details":{"count":2}}');
    expect(remoteModel.doStreamCalls).toHaveLength(1);
    expect(remoteModel.doGenerateCalls).toHaveLength(1);
    expect(remoteModel.doGenerateCalls[0]?.toolChoice).toEqual({ type: 'tool', toolName: 'lookup' });
    const repairPrompt = JSON.stringify(remoteModel.doGenerateCalls[0]?.prompt);
    expect(repairPrompt).toContain('"input":{"details":{"count":"wrong"}}');
    expect(repairPrompt).not.toContain('"input":"{');
    expect(repairPrompt).toContain(
      'Validation failure: data/details/count must be number',
    );
    expect(llamaModel.doGenerateCalls).toHaveLength(0);
    expect(llamaModel.doStreamCalls).toHaveLength(0);
  });

  test('ignores invalid later tool calls when parallel tool calls are disabled', async () => {
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = sequencedToolCallSetsLanguageModel('served-remote-model', [[
      { toolName: 'lookup', input: '{"key":"first"}' },
      { toolName: 'lookup', input: '{' },
    ]]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateChatCompletion({ ...chatRequest, toolChoice: 'required' });

    expect(response.message.toolCalls).toEqual([{
      id: 'call-0',
      type: 'function',
      function: { name: 'lookup', arguments: '{"key":"first"}' },
    }]);
    expect(remoteModel.doStreamCalls).toHaveLength(1);
    expect(llamaModel.doStreamCalls).toHaveLength(0);
  });

  test('repairs only the invalid first tool call when parallel tool calls are disabled', async () => {
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = sequencedToolCallSetsLanguageModel('served-remote-model', [
      [
        { toolName: 'lookup', input: '{' },
        { toolName: 'lookup', input: '{"key":"later"}' },
      ],
      [
        { toolName: 'lookup', input: '{"key":"repaired"}' },
        { toolName: 'lookup', input: '{' },
      ],
    ]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateChatCompletion({ ...chatRequest, toolChoice: 'required' });

    expect(response.message.toolCalls).toEqual([{
      id: 'call-0',
      type: 'function',
      function: { name: 'lookup', arguments: '{"key":"repaired"}' },
    }]);
    expect(remoteModel.doStreamCalls).toHaveLength(1);
    expect(remoteModel.doGenerateCalls).toHaveLength(1);
    expect(llamaModel.doGenerateCalls).toHaveLength(0);
    expect(llamaModel.doStreamCalls).toHaveLength(0);
  });

  test('warns naming the dropped and kept tool calls when a generate call returns more than one', async () => {
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = sequencedToolCallSetsLanguageModel('served-remote-model', [
      [{ toolName: 'lookup', input: '{' }],
      [
        { toolName: 'lookup', input: '{"key":"repaired"}' },
        { toolName: 'task_add', input: '{"title":"draft"}' },
      ],
    ]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );
    const warnSpy = spyOn(console, 'warn').mockImplementation(() => {});
    let warningMessage: unknown;

    try {
      await generateChatCompletion({ ...chatRequest, toolChoice: 'required' });
      expect(warnSpy).toHaveBeenCalledTimes(1);
      warningMessage = warnSpy.mock.calls[0]?.[0];
    } finally {
      warnSpy.mockRestore();
    }

    expect(warningMessage).toContain('task_add');
    expect(warningMessage).toContain('lookup');
  });

  test('preserves all valid tool calls when parallel tool calls are enabled', async () => {
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = sequencedToolCallSetsLanguageModel('served-remote-model', [[
      { toolName: 'lookup', input: '{"key":"first"}' },
      { toolName: 'lookup', input: '{"key":"second"}' },
    ]]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateChatCompletion({
      ...chatRequest,
      toolChoice: 'required',
      parallelToolCalls: true,
    });

    expect(response.message.toolCalls?.map(toolCall => toolCall.function.arguments)).toEqual([
      '{"key":"first"}',
      '{"key":"second"}',
    ]);
    expect(remoteModel.doStreamCalls).toHaveLength(1);
    expect(llamaModel.doStreamCalls).toHaveLength(0);
  });

  test('repairs unknown native tool arguments once without changing the provider schema', async () => {
    const request = openTaskChatRequest();
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = sequencedToolCallLanguageModel('served-remote-model', [
      '{"task":{"title":"ship","unexpected":{"malformed":true}},"items":[{"name":"first","unknown":["bad"]}],"optionalNote":"keep"}',
      '{"task":{"title":"ship","priority":2},"items":[{"name":"first","label":"primary"}],"optionalNote":"keep"}',
    ]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateChatCompletion(request);
    const providerTool = remoteModel.doStreamCalls[0]?.tools?.[0];
    const providerSchema = providerTool?.type === 'function' ? providerTool.inputSchema : undefined;

    expect(response.message.toolCalls?.[0]?.function.arguments).toBe(
      '{"task":{"title":"ship","priority":2},"items":[{"name":"first","label":"primary"}],"optionalNote":"keep"}',
    );
    expect(remoteModel.doStreamCalls).toHaveLength(1);
    expect(remoteModel.doGenerateCalls).toHaveLength(1);
    expect(llamaModel.doGenerateCalls).toHaveLength(0);
    expect(llamaModel.doStreamCalls).toHaveLength(0);
    expect(JSON.stringify(providerSchema)).toBe(JSON.stringify(request.tools?.[0]?.function.parameters));
    const repairProviderTool = remoteModel.doGenerateCalls[0]?.tools?.[0];
    const repairProviderSchema = repairProviderTool?.type === 'function' ? repairProviderTool.inputSchema : undefined;
    expect(JSON.stringify(repairProviderSchema)).toBe(JSON.stringify(request.tools?.[0]?.function.parameters));
    expect(providerSchema).not.toHaveProperty('additionalProperties');
    const repairPrompt = remoteModel.doGenerateCalls[0]?.prompt;
    expect(repairPrompt?.filter(message => message.role === 'user')).toEqual(
      remoteModel.doStreamCalls[0]?.prompt.filter(message => message.role === 'user'),
    );
    expect(repairPrompt?.at(-2)?.role).toBe('assistant');
    expect(repairPrompt?.at(-1)?.role).toBe('tool');
    const serializedRepairPrompt = JSON.stringify(repairPrompt);
    expect(serializedRepairPrompt).toContain('tool-call');
    expect(serializedRepairPrompt).toContain('error-text');
    expect(serializedRepairPrompt).toContain('unexpected');
    expect(serializedRepairPrompt).toContain('Validation category: schema_validation');
    expect(serializedRepairPrompt).toContain('Validation failure: data/task must NOT have additional properties');
    expect(serializedRepairPrompt).toContain('\\"additionalProperties\\":false');
    expect((serializedRepairPrompt.match(/additionalProperties/g) ?? []).length).toBeGreaterThanOrEqual(3);
  });

  test('removes optional non-nullable properties through nested objects and arrays', async () => {
    const request = nullNormalizationChatRequest();
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = sequencedToolCallLanguageModel('served-remote-model', [JSON.stringify({
      optionalText: null,
      requiredText: 'keep',
      nullableText: null,
      nested: { optionalCount: null, requiredCount: 2 },
      rows: [{ optionalLabel: null, requiredLabel: 'first', nullableLabel: null }],
    })]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateChatCompletion(request);

    expect(response.message.toolCalls?.[0]?.function.arguments).toBe(JSON.stringify({
      requiredText: 'keep',
      nullableText: null,
      nested: { requiredCount: 2 },
      rows: [{ requiredLabel: 'first', nullableLabel: null }],
    }));
    expect(remoteModel.doStreamCalls).toHaveLength(1);
    expect(llamaModel.doStreamCalls).toHaveLength(0);
  });

  test('keeps required nulls, array elements, additional property values, and unknown keys for validation', async () => {
    const request = nullNormalizationChatRequest();
    const invalidArguments = JSON.stringify({
      requiredText: null,
      nullableText: null,
      nested: { requiredCount: 2 },
      rows: [null],
      metadata: { extra: null },
      unknown: null,
    });
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = sequencedToolCallLanguageModel('served-remote-model', [invalidArguments, invalidArguments]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    await expect(generateChatCompletion(request)).rejects.toMatchObject({
      code: 'provider_response_invalid',
      diagnostic: {
        category: StructuredOutputDiagnosticCategory.SchemaValidation,
        toolName: 'lookup',
        validationIssues: expect.arrayContaining([
          { fieldPath: '/unknown', code: StructuredOutputValidationCode.AdditionalProperty },
          { fieldPath: '/requiredText', code: StructuredOutputValidationCode.Type },
          { fieldPath: '/rows/0', code: StructuredOutputValidationCode.Type },
          { fieldPath: '/metadata/extra', code: StructuredOutputValidationCode.Type },
        ]),
        repairStatus: StructuredOutputRepairStatus.Failed,
      },
    });

    const repairPrompt = JSON.stringify(remoteModel.doGenerateCalls[0]?.prompt);
    expect(repairPrompt).toContain('data must NOT have additional properties');
    expect(repairPrompt).toContain('data/requiredText must be string');
    expect(repairPrompt).toContain('data/rows/0 must be object');
    expect(repairPrompt).toContain('data/metadata/extra must be string');
    expect(remoteModel.doStreamCalls).toHaveLength(1);
    expect(remoteModel.doGenerateCalls).toHaveLength(1);
    expect(llamaModel.doGenerateCalls).toHaveLength(0);
    expect(llamaModel.doStreamCalls).toHaveLength(0);
  });

  test('fails closed for permanent unknown native tool arguments without an alternate route', async () => {
    const request = openTaskChatRequest();
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = sequencedToolCallLanguageModel('served-remote-model', [
      '{"task":{"title":"ship","unexpected":{"malformed":true}},"items":[{"name":"first"}]}',
      '{"task":{"title":"ship","unexpected":{"stillMalformed":true}},"items":[{"name":"first"}]}',
    ]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    await expect(generateChatCompletion(request)).rejects.toMatchObject({
      code: 'provider_response_invalid',
      allowLegacyFallback: false,
      diagnostic: {
        category: StructuredOutputDiagnosticCategory.SchemaValidation,
        toolName: 'lookup',
        validationIssues: [{ fieldPath: '/task/unexpected', code: StructuredOutputValidationCode.AdditionalProperty }],
        repairStatus: StructuredOutputRepairStatus.Failed,
      },
    });
    expect(remoteModel.doStreamCalls).toHaveLength(1);
    expect(remoteModel.doGenerateCalls).toHaveLength(1);
    expect(llamaModel.doGenerateCalls).toHaveLength(0);
    expect(llamaModel.doStreamCalls).toHaveLength(0);
  });

  test('preserves explicit open object properties while closing only omitted properties for repair', async () => {
    const request = explicitOpenChatRequest();
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = sequencedToolCallLanguageModel('served-remote-model', [
      '{"metadata":{"source":"model"},"labels":{"team":"blueclaw"},"unexpected":true}',
      '{"metadata":{"source":"model","extra":true},"labels":{"team":"blueclaw","owner":"llmd"}}',
    ]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateChatCompletion(request);

    const providerTool = remoteModel.doStreamCalls[0]?.tools?.[0];
    const providerSchema = providerTool?.type === 'function' ? providerTool.inputSchema : undefined;
    const repairProviderTool = remoteModel.doGenerateCalls[0]?.tools?.[0];
    const repairProviderSchema = repairProviderTool?.type === 'function' ? repairProviderTool.inputSchema : undefined;
    const repairPrompt = JSON.stringify(remoteModel.doGenerateCalls[0]?.prompt);
    expect(JSON.stringify(providerSchema)).toBe(JSON.stringify(request.tools?.[0]?.function.parameters));
    expect(JSON.stringify(repairProviderSchema)).toBe(JSON.stringify(request.tools?.[0]?.function.parameters));
    expect(repairPrompt).toContain('additionalProperties');
    expect(repairPrompt).toContain('true');
    expect(repairPrompt).toContain('string');
    expect(repairPrompt).toContain('false');
    expect(repairPrompt).toContain('\\"additionalProperties\\":true');
    expect(repairPrompt).toContain('\\"additionalProperties\\":{\\"type\\":\\"string\\"}');
    expect(repairPrompt).toContain('\\"additionalProperties\\":false');
    expect(response.message.toolCalls?.[0]?.function.arguments).toBe(
      '{"metadata":{"source":"model","extra":true},"labels":{"team":"blueclaw","owner":"llmd"}}',
    );
  });

  test('rejects permanently invalid native tool arguments without an alternate route', async () => {
    const request = nestedChatRequest();
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = sequencedToolCallLanguageModel('served-remote-model', [
      '{"details":{"count":"wrong"}}',
      '{"details":{"count":"still-wrong"}}',
    ]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    await expect(generateChatCompletion(request)).rejects.toMatchObject({
      code: 'provider_response_invalid',
      allowLegacyFallback: false,
      diagnostic: { category: StructuredOutputDiagnosticCategory.SchemaValidation },
    });
    expect(remoteModel.doStreamCalls).toHaveLength(1);
    expect(remoteModel.doGenerateCalls).toHaveLength(1);
    expect(llamaModel.doGenerateCalls).toHaveLength(0);
    expect(llamaModel.doStreamCalls).toHaveLength(0);
  });

  test('allows chat device routing without structured-output enablement', async () => {
    const llamaModel = chatLanguageModel('served-local-model');
    const remoteModel = successfulLanguageModel('unused-remote-model', { ok: true });
    const generateChatCompletion = createChatCompletionGenerator(
      { ...completeConfiguration(LLMDAutoRoute.RemoteFirst), llamaStructuredOutputsEnabled: false },
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateChatCompletion({ ...chatRequest, executionMode: ExecutionMode.Device });

    expect(response.selectedBackend).toBe(LanguageModelBackend.Device);
    expect(llamaModel.doStreamCalls).toHaveLength(1);
    expect(remoteModel.doStreamCalls).toHaveLength(0);
  });

  test('falls back for chat after a retryable provider failure', async () => {
    const routeAttempts: string[] = [];
    const llamaModel = apiFailingLanguageModel('llama.cpp', true, routeAttempts);
    const remoteModel = chatLanguageModel('served-remote-model');
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.LocalFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateChatCompletion({ ...chatRequest, executionMode: ExecutionMode.Auto });

    expect(routeAttempts).toEqual(['llama.cpp']);
    expect(response.selectedBackend).toBe(LanguageModelBackend.Remote);
    expect(remoteModel.doStreamCalls).toHaveLength(1);
  });

  test('keeps automatic chat routing local in local-only mode', async () => {
    const llamaModel = chatLanguageModel('served-local-model');
    const remoteModel = chatLanguageModel('unused-remote-model');
    const generateChatCompletion = createChatCompletionGenerator(
      { ...completeConfiguration(LLMDAutoRoute.RemoteFirst), localOnly: true },
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateChatCompletion({ ...chatRequest, executionMode: ExecutionMode.Auto });

    expect(response.selectedBackend).toBe(LanguageModelBackend.Device);
    expect(llamaModel.doStreamCalls).toHaveLength(1);
    expect(remoteModel.doStreamCalls).toHaveLength(0);
    await expect(generateChatCompletion({ ...chatRequest, executionMode: ExecutionMode.Remote })).rejects.toThrow(
      'remote routing is disabled by local-only mode',
    );
  });

  test('writes parallel tool calls using the provider wire field', async () => {
    const requestBodies: Array<Record<string, unknown>> = [];
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      wireLanguageModelFactory(requestBodies),
    );

    await generateChatCompletion({
      ...chatRequest,
      executionMode: ExecutionMode.Remote,
      toolChoice: 'auto',
      parallelToolCalls: false,
    });
    await generateChatCompletion({
      ...chatRequest,
      executionMode: ExecutionMode.Device,
      toolChoice: 'auto',
      parallelToolCalls: true,
    });
    const localOnlyGenerator = createChatCompletionGenerator(
      { ...completeConfiguration(LLMDAutoRoute.RemoteFirst), localOnly: true },
      wireLanguageModelFactory(requestBodies),
    );
    await localOnlyGenerator({
      ...chatRequest,
      executionMode: ExecutionMode.Auto,
      toolChoice: 'auto',
      parallelToolCalls: false,
    });

    expect(requestBodies).toHaveLength(3);
    expect(requestBodies[0]?.parallel_tool_calls).toBe(false);
    expect(requestBodies[0]?.parallelToolCalls).toBeUndefined();
    expect(requestBodies[1]?.parallel_tool_calls).toBe(true);
    expect(requestBodies[1]?.parallelToolCalls).toBeUndefined();
    expect(requestBodies[2]?.parallel_tool_calls).toBe(false);
    expect(requestBodies[2]?.parallelToolCalls).toBeUndefined();
  });

  test('disables parallel tool calls for structured routes and preserves chat values', async () => {
    const structuredCalls: ProviderFactoryCall[] = [];
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      recordingLanguageModelFactory(
        successfulLanguageModel('device-model', { ok: true }),
        successfulLanguageModel('remote-model', { ok: true }),
        structuredCalls,
      ),
    );

    await generateStructuredResponse({ ...structuredRequest, executionMode: ExecutionMode.Remote });
    await generateStructuredResponse({ ...structuredRequest, executionMode: ExecutionMode.Device });

    expect(structuredCalls).toEqual([
      { provider: 'openrouter', parallelToolCalls: false },
      { provider: 'llama.cpp', parallelToolCalls: false },
    ]);

    const chatCalls: ProviderFactoryCall[] = [];
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      recordingLanguageModelFactory(
        chatLanguageModel('device-model'),
        chatLanguageModel('remote-model'),
        chatCalls,
      ),
    );

    await generateChatCompletion({ ...chatRequest, executionMode: ExecutionMode.Remote, parallelToolCalls: true });
    await generateChatCompletion({ ...chatRequest, executionMode: ExecutionMode.Device, parallelToolCalls: false });

    expect(chatCalls).toEqual([
      { provider: 'openrouter', parallelToolCalls: true },
      { provider: 'llama.cpp', parallelToolCalls: false },
    ]);
  });

  test('passes cancellation to the model and does not fall back after abort', async () => {
    const abortController = new AbortController();
    const routeAttempts: string[] = [];
    let resolveStarted: (() => void) | undefined;
    const started = new Promise<void>(resolve => {
      resolveStarted = resolve;
    });
    const llamaModel = new MockLanguageModelV3({
      doStream: async options => {
        routeAttempts.push('llama.cpp');
        expect(options.abortSignal).toBeDefined();
        resolveStarted?.();
        return hangingStreamResult(options.abortSignal);
      },
    });
    const remoteModel = successfulLanguageModel('unused-remote-model', { ok: true });
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.LocalFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const responsePromise = generateChatCompletion({ ...chatRequest, executionMode: ExecutionMode.Auto }, abortController.signal);
    await started;
    abortController.abort();

    await expect(responsePromise).rejects.toThrow('aborted');
    expect(routeAttempts).toEqual(['llama.cpp']);
    expect(remoteModel.doGenerateCalls).toHaveLength(0);
    expect(remoteModel.doStreamCalls).toHaveLength(0);
  });

  test('cancels chat repair without using a fallback route', async () => {
    const abortController = new AbortController();
    let generationCount = 0;
    let resolveRepairStarted: (() => void) | undefined;
    const repairStarted = new Promise<void>(resolve => {
      resolveRepairStarted = resolve;
    });
    const model = new MockLanguageModelV3({
      modelId: 'repairing-model',
      doStream: async () => {
        generationCount += 1;
        return streamResultFromGeneration(toolCallGeneration('repairing-model', 'lookup', '{"details":{"count":"wrong"}}'));
      },
      doGenerate: async options => {
        generationCount += 1;
        expect(options.abortSignal).toBeDefined();
        resolveRepairStarted?.();
        return new Promise((_, reject) => {
          if (options.abortSignal?.aborted) {
            reject(new DOMException('The operation was aborted', 'AbortError'));
            return;
          }
          options.abortSignal?.addEventListener('abort', () => {
            reject(new DOMException('The operation was aborted', 'AbortError'));
          }, { once: true });
        });
      },
    });
    const fallbackModel = successfulLanguageModel('unused-model', { ok: true });
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.LocalFirst),
      languageModelFactory(model, fallbackModel),
    );

    const responsePromise = generateChatCompletion(nestedChatRequest(), abortController.signal);
    await repairStarted;
    abortController.abort();

    await expect(responsePromise).rejects.toThrow('aborted');
    expect(generationCount).toBe(2);
    expect(fallbackModel.doGenerateCalls).toHaveLength(0);
  });

  test('passes structured cancellation to the model and does not fall back after abort', async () => {
    const abortController = new AbortController();
    const routeAttempts: string[] = [];
    let resolveStarted: (() => void) | undefined;
    const started = new Promise<void>(resolve => {
      resolveStarted = resolve;
    });
    const llamaModel = new MockLanguageModelV3({
      doStream: async options => {
        routeAttempts.push('llama.cpp');
        expect(options.abortSignal).toBeDefined();
        resolveStarted?.();
        return hangingStreamResult(options.abortSignal);
      },
    });
    const remoteModel = successfulLanguageModel('unused-remote-model', { ok: true });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.LocalFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const responsePromise = generateStructuredResponse(
      { ...structuredRequest, executionMode: ExecutionMode.Auto },
      abortController.signal,
    );
    await started;
    abortController.abort();

    await expect(responsePromise).rejects.toThrow('aborted');
    expect(routeAttempts).toEqual(['llama.cpp']);
    expect(remoteModel.doGenerateCalls).toHaveLength(0);
    expect(remoteModel.doStreamCalls).toHaveLength(0);
  });

  test('rejects pre-aborted structured requests before route resolution', async () => {
    const abortController = new AbortController();
    abortController.abort();
    const model = successfulLanguageModel('unused-model', { ok: true });
    const generateStructuredResponse = createStructuredResponseGenerator(
      { ...completeConfiguration(LLMDAutoRoute.RemoteFirst), llamaBaseURL: undefined, llamaModel: undefined, openRouterAPIKey: undefined },
      languageModelFactory(model, model),
    );

    await expect(generateStructuredResponse(structuredRequest, abortController.signal)).rejects.toThrow('aborted');
    expect(model.doGenerateCalls).toHaveLength(0);
  });

  test('selects the requested device route and normalizes structured output and usage', async () => {
    const llamaModel = successfulLanguageModel('served-local-model', { ok: true }, {
      inputTokens: { total: 12.9, noCache: 8, cacheRead: 4.8, cacheWrite: -2 },
      outputTokens: { total: 5.7, text: 4, reasoning: 1.9 },
    });
    const remoteModel = successfulLanguageModel('unused-remote-model', { ok: false });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateStructuredResponse({ ...structuredRequest, executionMode: ExecutionMode.Device });

    expect(response).toEqual({
      provider: 'llama.cpp',
      model: 'served-local-model',
      content: '{"ok":true}',
      selectedBackend: LanguageModelBackend.Device,
      finishReason: 'stop',
      constraintMode: StructuredOutputConstraintMode.NativeToolCall,
      usage: {
        promptTokens: 12,
        completionTokens: 5,
        totalTokens: 18,
        cachedPromptTokens: 4,
        cacheWriteTokens: 0,
        reasoningTokens: 1,
      },
    });
    expect(llamaModel.doStreamCalls).toHaveLength(1);
    expect(remoteModel.doStreamCalls).toHaveLength(0);
    expect(llamaModel.doStreamCalls[0]?.tools?.map(tool => tool.name)).toEqual(['provider_test_output']);
    expect(llamaModel.doStreamCalls[0]?.toolChoice).toEqual({ type: 'tool', toolName: 'provider_test_output' });
    expect(llamaModel.doStreamCalls[0]?.maxOutputTokens).toBe(128);
    expect(llamaModel.doStreamCalls[0]?.seed).toBe(7);
    expect(llamaModel.doStreamCalls[0]?.temperature).toBe(0);
  });

  test('carries OpenRouter usage accounting cost into chat completion usage', async () => {
    const remoteModel = usageAccountingChatLanguageModel('served-remote-model');
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(successfulLanguageModel('unused-local-model', { ok: true }), remoteModel),
    );

    const response = await generateChatCompletion({ ...chatRequest, tools: undefined, toolChoice: undefined });

    expect(response.usage).toEqual({
      promptTokens: 10,
      completionTokens: 5,
      totalTokens: 15,
      costUSD: 0.0042,
      upstreamInferenceCostUSD: 0.0031,
    });
  });

  test('preserves closed dynamic enum schemas for structured output', async () => {
    const request: StructuredResponseRequest = {
      ...structuredRequest,
      structuredOutputSchema: {
        name: 'provider_enum_output',
        document: {
          type: 'object',
          properties: { state: { type: 'string', enum: ['ready', 'blocked'] } },
          required: ['state'],
          additionalProperties: false,
        },
        isStrictlyEnforced: true,
      },
    };
    const model = toolCallLanguageModel('served-remote-model', [{
      toolName: 'provider_enum_output',
      input: '{"state":"ready"}',
    }]);
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(successfulLanguageModel('unused-local-model', { ok: false }), model),
    );

    const response = await generateStructuredResponse(request);
    const providerTool = model.doStreamCalls[0]?.tools?.[0];

    expect(response.content).toBe('{"state":"ready"}');
    expect(JSON.stringify(providerTool?.type === 'function' ? providerTool.inputSchema : undefined)).toBe(
      JSON.stringify(request.structuredOutputSchema.document),
    );
    expect(model.doStreamCalls[0]?.toolChoice).toEqual({ type: 'tool', toolName: 'provider_enum_output' });
  });

  test('honors auto route order and falls back after a retryable provider failure', async () => {
    const routeAttempts: string[] = [];
    const llamaModel = apiFailingLanguageModel('llama.cpp', true, routeAttempts);
    const remoteModel = successfulLanguageModel('served-remote-model', { ok: true }, undefined, () => {
      routeAttempts.push('openrouter');
    });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.LocalFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateStructuredResponse(structuredRequest);

    expect(routeAttempts).toEqual(['llama.cpp', 'openrouter']);
    expect(response.provider).toBe('openrouter');
    expect(response.selectedBackend).toBe(LanguageModelBackend.Remote);
    expect(response.constraintMode).toBe(StructuredOutputConstraintMode.NativeToolCall);
  });

  test('falls back only after retryable provider failures', async () => {
    const routeAttempts: string[] = [];
    const llamaModel = apiFailingLanguageModel('llama.cpp', true, routeAttempts);
    const remoteModel = successfulLanguageModel('served-remote-model', { ok: true }, undefined, () => {
      routeAttempts.push('openrouter');
    });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.LocalFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateStructuredResponse(structuredRequest);

    expect(routeAttempts).toEqual(['llama.cpp', 'openrouter']);
    expect(response.provider).toBe('openrouter');
  });

  test('falls back when RetryError wraps a retryable provider failure', async () => {
    const routeAttempts: string[] = [];
    const llamaModel = retryFailingLanguageModel('llama.cpp', true, routeAttempts);
    const remoteModel = successfulLanguageModel('served-remote-model', { ok: true }, undefined, () => {
      routeAttempts.push('openrouter');
    });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.LocalFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateStructuredResponse(structuredRequest);

    expect(routeAttempts).toEqual(['llama.cpp', 'openrouter']);
    expect(response.provider).toBe('openrouter');
  });

  test('does not fall back when RetryError wraps a non-retryable provider failure', async () => {
    const routeAttempts: string[] = [];
    const llamaModel = retryFailingLanguageModel('llama.cpp', false, routeAttempts);
    const remoteModel = successfulLanguageModel('unused-remote-model', { ok: true });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.LocalFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    await expect(generateStructuredResponse(structuredRequest)).rejects.toThrow('provider request failed');
    expect(routeAttempts).toEqual(['llama.cpp']);
    expect(remoteModel.doGenerateCalls).toHaveLength(0);
  });

  test('does not fall back after non-retryable provider failures', async () => {
    const routeAttempts: string[] = [];
    const llamaModel = apiFailingLanguageModel('llama.cpp', false, routeAttempts);
    const remoteModel = successfulLanguageModel('unused-remote-model', { ok: true });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.LocalFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    await expect(generateStructuredResponse(structuredRequest)).rejects.toThrow('provider request failed');
    expect(routeAttempts).toEqual(['llama.cpp']);
    expect(remoteModel.doGenerateCalls).toHaveLength(0);
  });

  test('does not route or allow legacy fallback for a non-retryable 500 response', async () => {
    const routeAttempts: string[] = [];
    const llamaModel = apiFailingLanguageModel('llama.cpp', false, routeAttempts, 500);
    const remoteModel = successfulLanguageModel('unused-remote-model', { ok: true });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.LocalFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    try {
      await generateStructuredResponse(structuredRequest);
      throw new Error('expected provider failure');
    } catch (errorValue) {
      expect(errorValue).toMatchObject({
        code: 'provider_response_invalid',
        status: 502,
        allowLegacyFallback: false,
      });
    }
    expect(routeAttempts).toEqual(['llama.cpp']);
    expect(remoteModel.doGenerateCalls).toHaveLength(0);
  });

  test('stops after the first successful auto route', async () => {
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: false });
    const remoteModel = successfulLanguageModel('served-remote-model', { ok: true });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateStructuredResponse(structuredRequest);

    expect(response.provider).toBe('openrouter');
    expect(remoteModel.doStreamCalls).toHaveLength(1);
    expect(llamaModel.doStreamCalls).toHaveLength(0);
  });

  test('keeps auto and remote routing local in local-only mode', async () => {
    const llamaModel = successfulLanguageModel('local-model', { ok: true });
    const remoteModel = successfulLanguageModel('remote-model', { ok: true });
    const configuration = { ...completeConfiguration(LLMDAutoRoute.RemoteFirst), localOnly: true };
    const generateStructuredResponse = createStructuredResponseGenerator(
      configuration,
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateStructuredResponse(structuredRequest);

    expect(response.selectedBackend).toBe(LanguageModelBackend.Device);
    expect(remoteModel.doGenerateCalls).toHaveLength(0);
    await expect(generateStructuredResponse({ ...structuredRequest, executionMode: ExecutionMode.Remote })).rejects.toThrow(
      'remote routing is disabled by local-only mode',
    );
  });

  test('rejects provider output that violates the requested schema', async () => {
    const invalidModel = successfulLanguageModel('invalid-model', { ok: 'not-a-boolean' });
    const fallbackModel = successfulLanguageModel('fallback-model', { ok: true });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.LocalFirst),
      languageModelFactory(invalidModel, fallbackModel),
    );

    await expect(generateStructuredResponse(structuredRequest)).rejects.toMatchObject({
      code: 'structured_output_invalid',
      diagnostic: { category: StructuredOutputDiagnosticCategory.SchemaValidation },
    });
    expect(fallbackModel.doGenerateCalls).toHaveLength(0);
  });

  test('distinguishes malformed JSON from schema validation failures', async () => {
    const model = toolCallLanguageModel('invalid-model', [{
      toolName: 'provider_test_output',
      input: '{',
    }]);
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.LocalFirst),
      languageModelFactory(model, successfulLanguageModel('unused-model', { ok: true })),
    );

    await expect(generateStructuredResponse(structuredRequest)).rejects.toMatchObject({
      code: 'structured_output_invalid',
      diagnostic: { category: StructuredOutputDiagnosticCategory.JSONParse },
    });
  });

  test('repairs malformed structured output with one same-route generation', async () => {
    const model = malformedThenValidLanguageModel('repaired-model', { ok: true });
    const fallbackModel = successfulLanguageModel('unused-model', { ok: false });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.LocalFirst),
      languageModelFactory(model, fallbackModel),
    );

    const response = await generateStructuredResponse({ ...structuredRequest, executionMode: ExecutionMode.Device });

    expect(response.content).toBe('{"ok":true}');
    expect(model.doStreamCalls).toHaveLength(1);
    expect(model.doGenerateCalls).toHaveLength(1);
    expect(model.doStreamCalls[0]?.toolChoice).toEqual({ type: 'tool', toolName: 'provider_test_output' });
    expect(model.doGenerateCalls[0]?.toolChoice).toEqual({ type: 'tool', toolName: 'provider_test_output' });
    expect(model.doGenerateCalls[0]?.maxOutputTokens).toBe(128);
    expect(model.doGenerateCalls[0]?.seed).toBe(7);
    expect(model.doGenerateCalls[0]?.temperature).toBe(0);
    expect(fallbackModel.doGenerateCalls).toHaveLength(0);
    expect(fallbackModel.doStreamCalls).toHaveLength(0);
  });

  test('repairs structured output with the closed schema and validation category', async () => {
    const request = nestedStructuredRequest();
    const model = sequencedStructuredToolCallLanguageModel('repaired-model', [
      '{"details":{"count":"wrong"}}',
      '{"details":{"count":2}}',
    ]);
    const fallbackModel = successfulLanguageModel('unused-model', { ok: false });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.LocalFirst),
      languageModelFactory(model, fallbackModel),
    );

    const response = await generateStructuredResponse({ ...request, executionMode: ExecutionMode.Device });

    expect(response.content).toBe('{"details":{"count":2}}');
    expect(model.doStreamCalls).toHaveLength(1);
    expect(model.doGenerateCalls).toHaveLength(1);
    expect(model.doGenerateCalls[0]?.toolChoice).toEqual({ type: 'tool', toolName: 'provider_test_output' });
    const providerTool = model.doStreamCalls[0]?.tools?.[0];
    const providerSchema = providerTool?.type === 'function' ? providerTool.inputSchema : undefined;
    const repairProviderTool = model.doGenerateCalls[0]?.tools?.[0];
    const repairProviderSchema = repairProviderTool?.type === 'function' ? repairProviderTool.inputSchema : undefined;
    const repairPrompt = model.doGenerateCalls[0]?.prompt;
    expect(JSON.stringify(providerSchema)).toBe(JSON.stringify(request.structuredOutputSchema.document));
    expect(JSON.stringify(repairProviderSchema)).toBe(JSON.stringify(request.structuredOutputSchema.document));
    expect(repairPrompt?.at(-2)?.role).toBe('assistant');
    expect(repairPrompt?.at(-1)?.role).toBe('tool');
    const serializedRepairPrompt = JSON.stringify(repairPrompt);
    expect(serializedRepairPrompt).toContain('Closed JSON schema');
    expect(serializedRepairPrompt).toContain('Validation category: schema_validation');
    expect(serializedRepairPrompt).toContain('\\"additionalProperties\\":false');
    expect((serializedRepairPrompt.match(/additionalProperties/g) ?? []).length).toBeGreaterThanOrEqual(2);
    expect(fallbackModel.doGenerateCalls).toHaveLength(0);
    expect(fallbackModel.doStreamCalls).toHaveLength(0);
  });

  test('fails closed for permanently invalid structured output without an alternate route', async () => {
    const model = sequencedStructuredToolCallLanguageModel('invalid-model', [
      '{"details":{"count":"wrong"}}',
      '{"details":{"count":"still-wrong"}}',
    ]);
    const fallbackModel = successfulLanguageModel('unused-model', { ok: true });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.LocalFirst),
      languageModelFactory(model, fallbackModel),
    );

    await expect(generateStructuredResponse({ ...nestedStructuredRequest(), executionMode: ExecutionMode.Device })).rejects.toMatchObject({
      code: 'structured_output_invalid',
      diagnostic: { category: StructuredOutputDiagnosticCategory.SchemaValidation },
    });
    expect(model.doStreamCalls).toHaveLength(1);
    expect(model.doGenerateCalls).toHaveLength(1);
    expect(fallbackModel.doGenerateCalls).toHaveLength(0);
    expect(fallbackModel.doStreamCalls).toHaveLength(0);
  });

  test('rejects structured output without exactly one matching tool call', async () => {
    for (const toolCalls of [
      [],
      [{ toolName: 'other_output', input: '{}' }],
      [
        { toolName: 'provider_test_output', input: '{"ok":true}' },
        { toolName: 'provider_test_output', input: '{"ok":true}' },
      ],
    ]) {
      const model = toolCallLanguageModel('invalid-model', toolCalls);
      const generateStructuredResponse = createStructuredResponseGenerator(
        completeConfiguration(LLMDAutoRoute.LocalFirst),
        languageModelFactory(model, successfulLanguageModel('unused-model', { ok: true })),
      );

      await expect(generateStructuredResponse(structuredRequest)).rejects.toMatchObject({
        code: 'structured_output_invalid',
        diagnostic: { category: StructuredOutputDiagnosticCategory.ToolCallContract },
      });
      expect(model.doStreamCalls).toHaveLength(1);
    }
  });

  test('cancels structured repair without using a fallback route', async () => {
    const abortController = new AbortController();
    let generationCount = 0;
    let resolveRepairStarted: (() => void) | undefined;
    const repairStarted = new Promise<void>(resolve => {
      resolveRepairStarted = resolve;
    });
    const model = new MockLanguageModelV3({
      modelId: 'repairing-model',
      doStream: async () => {
        generationCount += 1;
        return streamResultFromGeneration(toolCallGeneration('repairing-model', 'provider_test_output', '{'));
      },
      doGenerate: async options => {
        generationCount += 1;
        expect(options.abortSignal).toBeDefined();
        resolveRepairStarted?.();
        return new Promise((_, reject) => {
          if (options.abortSignal?.aborted) {
            reject(new DOMException('The operation was aborted', 'AbortError'));
            return;
          }
          options.abortSignal?.addEventListener('abort', () => {
            reject(new DOMException('The operation was aborted', 'AbortError'));
          }, { once: true });
        });
      },
    });
    const fallbackModel = successfulLanguageModel('unused-model', { ok: true });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.LocalFirst),
      languageModelFactory(model, fallbackModel),
    );

    const responsePromise = generateStructuredResponse(
      { ...structuredRequest, executionMode: ExecutionMode.Auto },
      abortController.signal,
    );
    await repairStarted;
    abortController.abort();

    await expect(responsePromise).rejects.toThrow('aborted');
    expect(generationCount).toBe(2);
    expect(fallbackModel.doGenerateCalls).toHaveLength(0);
  });

  test('rejects undeclared quality review evidence fields', async () => {
    const qualityReviewRequest: StructuredResponseRequest = {
      ...structuredRequest,
      structuredOutputSchema: {
        ...structuredRequest.structuredOutputSchema,
        document: {
          type: 'object',
          properties: {
            qualityReview: {
              type: 'array',
              items: {
                type: 'object',
                properties: { evidenceIDs: { type: 'array', items: { type: 'string' } } },
                additionalProperties: false,
              },
            },
          },
          required: ['qualityReview'],
          additionalProperties: false,
        },
      },
    };
    const invalidModel = successfulLanguageModel('invalid-model', {
      qualityReview: [{ evidence: 'obs-1' }],
    });
    const fallbackModel = successfulLanguageModel('fallback-model', {
      qualityReview: [{ evidenceIDs: ['obs-1'] }],
    });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.LocalFirst),
      languageModelFactory(invalidModel, fallbackModel),
    );

    await expect(generateStructuredResponse(qualityReviewRequest)).rejects.toThrow();
    expect(fallbackModel.doGenerateCalls).toHaveLength(0);

    const generateValidStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.LocalFirst),
      languageModelFactory(fallbackModel, invalidModel),
    );
    const response = await generateValidStructuredResponse(qualityReviewRequest);

    expect(response.content).toBe('{"qualityReview":[{"evidenceIDs":["obs-1"]}]}');
  });

  test('returns the last provider failure after exhausting fallback routes', async () => {
    const routeAttempts: string[] = [];
    const llamaModel = apiFailingLanguageModel('llama.cpp', true, routeAttempts);
    const remoteModel = apiFailingLanguageModel('openrouter', true, routeAttempts);
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(LLMDAutoRoute.LocalFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    await expect(generateStructuredResponse(structuredRequest)).rejects.toThrow('provider request failed');
    expect(routeAttempts).toEqual(['llama.cpp', 'openrouter']);
  });

  test('fails before provider execution when the requested route is unavailable', async () => {
    const model = successfulLanguageModel('unused-model', { ok: true });
    const modelFactory = languageModelFactory(model, model);
    const configuration = completeConfiguration(LLMDAutoRoute.RemoteFirst);
    const noRouteConfiguration = { ...configuration, llamaBaseURL: undefined, llamaModel: undefined, openRouterAPIKey: undefined };
    const generateStructuredResponse = createStructuredResponseGenerator(noRouteConfiguration, modelFactory);

    await expect(generateStructuredResponse(structuredRequest)).rejects.toThrow(
      'auto routing requires an OpenRouter or llama.cpp configuration',
    );
    await expect(generateStructuredResponse({ ...structuredRequest, executionMode: ExecutionMode.Companion })).rejects.toThrow(
      'companion language model routing is provided by the host runtime, not by llmd',
    );
    await expect(generateStructuredResponse({ ...structuredRequest, executionMode: ExecutionMode.Remote })).rejects.toThrow(
      'remote routing requires OPENROUTER_API_KEY',
    );
    expect(model.doGenerateCalls).toHaveLength(0);
  });

  test('rejects a chat call whose stream stalls with no new chunks within the idle window', async () => {
    const stalledModel = new MockLanguageModelV3({
      modelId: 'stalled-model',
      doStream: async options => hangingStreamResult(options.abortSignal),
    });
    const generateChatCompletion = createChatCompletionGenerator(
      { ...completeConfiguration(LLMDAutoRoute.RemoteFirst), streamIdleTimeoutMs: 50 },
      languageModelFactory(stalledModel, stalledModel),
    );

    const startTime = Date.now();
    await expect(generateChatCompletion({ ...chatRequest, executionMode: ExecutionMode.Remote })).rejects.toMatchObject({
      code: 'provider_unavailable',
      status: 503,
      allowLegacyFallback: true,
    });
    expect(Date.now() - startTime).toBeLessThan(2000);
  });

  test('rejects a structured call whose stream stalls with no new chunks within the idle window', async () => {
    const stalledModel = new MockLanguageModelV3({
      modelId: 'stalled-model',
      doStream: async options => hangingStreamResult(options.abortSignal),
    });
    const generateStructuredResponse = createStructuredResponseGenerator(
      { ...completeConfiguration(LLMDAutoRoute.RemoteFirst), streamIdleTimeoutMs: 50 },
      languageModelFactory(stalledModel, stalledModel),
    );

    const startTime = Date.now();
    await expect(generateStructuredResponse({ ...structuredRequest, executionMode: ExecutionMode.Remote })).rejects.toMatchObject({
      code: 'provider_unavailable',
      status: 503,
      allowLegacyFallback: true,
    });
    expect(Date.now() - startTime).toBeLessThan(2000);
  });

  test('succeeds when a chat stream keeps producing chunks slower than the idle window allows in total', async () => {
    const flowingParts: LanguageModelV3StreamPart[] = [
      { type: 'stream-start', warnings: [] },
      { type: 'response-metadata', modelId: 'flowing-model' },
      { type: 'text-start', id: 'text-0' },
      { type: 'text-delta', id: 'text-0', delta: 'The ' },
      { type: 'text-delta', id: 'text-0', delta: 'answer ' },
      { type: 'text-delta', id: 'text-0', delta: 'is ready.' },
      { type: 'text-end', id: 'text-0' },
      { type: 'tool-call', toolCallId: 'call-0', toolName: 'lookup', input: '{"key":"result"}' },
      { type: 'finish', usage: defaultUsage(), finishReason: { unified: 'tool-calls', raw: 'tool_calls' } },
    ];
    const flowingModel = new MockLanguageModelV3({
      modelId: 'flowing-model',
      doStream: async () => ({ stream: simulateReadableStream({ chunks: flowingParts, chunkDelayInMs: 15 }) }),
    });
    const generateChatCompletion = createChatCompletionGenerator(
      { ...completeConfiguration(LLMDAutoRoute.RemoteFirst), streamIdleTimeoutMs: 50 },
      languageModelFactory(flowingModel, flowingModel),
    );

    const response = await generateChatCompletion({ ...chatRequest, executionMode: ExecutionMode.Remote });

    expect(response.message.toolCalls?.[0]?.function).toEqual({ name: 'lookup', arguments: '{"key":"result"}' });
    expect(flowingModel.doStreamCalls).toHaveLength(1);
  });
});

function nativeStructuredConfiguration(autoRoute: LLMDAutoRoute): LLMDConfiguration {
  return { ...completeConfiguration(autoRoute), structuredOutputMode: LLMDStructuredOutputMode.Native };
}

function jsonTextLanguageModel(modelID: string, output: unknown): MockLanguageModelV3 {
  return generationBackedLanguageModel(modelID, () => ({
    content: [{ type: 'text', text: JSON.stringify(output) }],
    finishReason: { unified: 'stop', raw: 'stop' },
    usage: defaultUsage(),
    response: { modelId: modelID },
    warnings: [],
  }));
}


function completeConfiguration(autoRoute: LLMDAutoRoute): LLMDConfiguration {
  return {
    authKey: 'installation-key',
    autoRoute,
    llamaAPIKey: 'local-key',
    llamaBaseURL: 'http://127.0.0.1:8080/v1',
    llamaModel: 'local-model',
    llamaStructuredOutputsEnabled: true,
    localOnly: false,
    openRouterAPIKey: 'remote-key',
    openRouterBaseURL: 'https://openrouter.invalid/api/v1',
    structuredOutputMode: LLMDStructuredOutputMode.ToolCall,
    socketPath: '/tmp/blueclaw-llmd-provider-test.sock',
  };
}

function languageModelFactory(
  llamaModel: MockLanguageModelV3,
  openRouterModel: MockLanguageModelV3,
): ProviderLanguageModelFactory {
  return {
    createLlamaLanguageModel: () => llamaModel,
    createOpenRouterLanguageModel: () => openRouterModel,
  };
}

type ProviderFactoryCall = {
  provider: 'llama.cpp' | 'openrouter';
  parallelToolCalls: boolean | undefined;
};

function recordingLanguageModelFactory(
  llamaModel: MockLanguageModelV3,
  openRouterModel: MockLanguageModelV3,
  calls: ProviderFactoryCall[],
): ProviderLanguageModelFactory {
  return {
    createLlamaLanguageModel(_modelName, _baseURL, _apiKey, parallelToolCalls) {
      calls.push({ provider: 'llama.cpp', parallelToolCalls });
      return llamaModel;
    },
    createOpenRouterLanguageModel(_modelName, _baseURL, _apiKey, parallelToolCalls) {
      calls.push({ provider: 'openrouter', parallelToolCalls });
      return openRouterModel;
    },
  };
}

function wireStreamingChatCompletionBody(): string {
  const chunks = [
    { id: 'wire-test', choices: [{ index: 0, delta: { role: 'assistant', content: 'ok' }, finish_reason: null }] },
    {
      id: 'wire-test',
      choices: [{ index: 0, delta: {}, finish_reason: 'stop' }],
      usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 },
    },
  ];
  const dataLines = chunks.map(chunk => `data: ${JSON.stringify(chunk)}\n\n`);
  return [...dataLines, 'data: [DONE]\n\n'].join('');
}

function wireLanguageModelFactory(requestBodies: Array<Record<string, unknown>>): ProviderLanguageModelFactory {
  const fetch = Object.assign(
    async (_input: string | URL | Request, init?: BunFetchRequestInit) => {
      const body = init?.body;
      if (typeof body !== 'string') throw new Error('wire test request body must be a string');
      const parsedBody: unknown = JSON.parse(body);
      if (!isRecord(parsedBody)) throw new Error('wire test request body must be an object');
      requestBodies.push(parsedBody);
      return new Response(wireStreamingChatCompletionBody(), { headers: { 'content-type': 'text/event-stream' } });
    },
    { preconnect: globalThis.fetch.preconnect },
  );
  return {
    createLlamaLanguageModel(modelName, baseURL, apiKey, parallelToolCalls) {
      const provider = createOpenAICompatible({
        apiKey,
        baseURL,
        name: 'llama-wire-test',
        supportsStructuredOutputs: true,
        fetch,
        transformRequestBody: parallelToolCalls === undefined
          ? undefined
          : requestBody => ({ ...requestBody, parallel_tool_calls: parallelToolCalls }),
      });
      return provider.chatModel(modelName);
    },
    createOpenRouterLanguageModel(modelName, baseURL, apiKey, parallelToolCalls) {
      const provider = createOpenRouter({ apiKey, baseURL, compatibility: 'strict', fetch });
      return provider.chat(modelName, parallelToolCalls === undefined ? undefined : { parallelToolCalls });
    },
  };
}

function streamPartsFromGenerateResult(result: LanguageModelV3GenerateResult): LanguageModelV3StreamPart[] {
  const parts: LanguageModelV3StreamPart[] = [{ type: 'stream-start', warnings: result.warnings }];
  if (result.response?.modelId !== undefined) parts.push({ type: 'response-metadata', modelId: result.response.modelId });
  result.content.forEach((content, index) => {
    if (content.type === 'text') {
      const id = `text-${index}`;
      parts.push({ type: 'text-start', id });
      parts.push({ type: 'text-delta', id, delta: content.text });
      parts.push({ type: 'text-end', id });
      return;
    }
    if (content.type === 'tool-call') {
      parts.push(content);
      return;
    }
    throw new Error(`unsupported content type for stream test conversion: ${content.type}`);
  });
  parts.push({ type: 'finish', usage: result.usage, finishReason: result.finishReason, providerMetadata: result.providerMetadata });
  return parts;
}

function streamResultFromGeneration(result: LanguageModelV3GenerateResult) {
  return { stream: simulateReadableStream({ chunks: streamPartsFromGenerateResult(result) }) };
}

function hangingStreamResult(abortSignal: AbortSignal | undefined): { stream: ReadableStream<LanguageModelV3StreamPart> } {
  return {
    stream: new ReadableStream<LanguageModelV3StreamPart>({
      start(controller) {
        controller.enqueue({ type: 'stream-start', warnings: [] });
      },
      pull() {
        return new Promise((_resolve, reject) => {
          if (abortSignal?.aborted) {
            reject(new DOMException('The operation was aborted', 'AbortError'));
            return;
          }
          abortSignal?.addEventListener('abort', () => {
            reject(new DOMException('The operation was aborted', 'AbortError'));
          }, { once: true });
        });
      },
    }),
  };
}

function generationBackedLanguageModel(
  modelID: string,
  generate: () => LanguageModelV3GenerateResult,
): MockLanguageModelV3 {
  return new MockLanguageModelV3({
    modelId: modelID,
    doGenerate: async () => generate(),
    doStream: async () => streamResultFromGeneration(generate()),
  });
}

function successfulLanguageModel(
  modelID: string,
  output: unknown,
  usage: LanguageModelV3Usage = defaultUsage(),
  onGenerate: () => void = () => {},
): MockLanguageModelV3 {
  return generationBackedLanguageModel(modelID, () => {
    onGenerate();
    return successfulGeneration(modelID, output, usage);
  });
}

function toolCallLanguageModel(
  modelID: string,
  toolCalls: Array<{ toolName: string; input: string }>,
): MockLanguageModelV3 {
  return generationBackedLanguageModel(modelID, () => ({
    content: toolCalls.map((toolCall, index) => ({
      type: 'tool-call' as const,
      toolCallId: `call-${index}`,
      toolName: toolCall.toolName,
      input: toolCall.input,
    })),
    finishReason: { unified: 'tool-calls', raw: 'tool_calls' },
    usage: defaultUsage(),
    response: { modelId: modelID },
    warnings: [],
  }));
}

function sequencedToolCallLanguageModel(modelID: string, inputs: string[]): MockLanguageModelV3 {
  return sequencedNamedToolCallLanguageModel(modelID, 'lookup', inputs);
}

function sequencedNamedToolCallLanguageModel(modelID: string, toolName: string, inputs: string[]): MockLanguageModelV3 {
  let generationCount = 0;
  return generationBackedLanguageModel(modelID, () => {
    const input = inputs[Math.min(generationCount, inputs.length - 1)];
    generationCount += 1;
    return toolCallGeneration(modelID, toolName, input ?? '{}');
  });
}

function sequencedToolCallSetsLanguageModel(
  modelID: string,
  toolCallSets: Array<Array<{ toolName: string; input: string }>>,
): MockLanguageModelV3 {
  let generationCount = 0;
  return generationBackedLanguageModel(modelID, () => {
    const toolCalls = toolCallSets[Math.min(generationCount, toolCallSets.length - 1)] ?? [];
    generationCount += 1;
    return toolCallsGeneration(modelID, toolCalls);
  });
}

function sequencedStructuredToolCallLanguageModel(modelID: string, inputs: string[]): MockLanguageModelV3 {
  let generationCount = 0;
  return generationBackedLanguageModel(modelID, () => {
    const input = inputs[Math.min(generationCount, inputs.length - 1)];
    generationCount += 1;
    return toolCallGeneration(modelID, 'provider_test_output', input ?? '{}');
  });
}

function malformedThenValidLanguageModel(modelID: string, output: unknown): MockLanguageModelV3 {
  let generationCount = 0;
  return generationBackedLanguageModel(modelID, () => {
    generationCount += 1;
    if (generationCount === 1) return toolCallGeneration(modelID, 'provider_test_output', '{');
    return successfulGeneration(modelID, output, defaultUsage());
  });
}

function toolCallGeneration(modelID: string, toolName: string, input: string): LanguageModelV3GenerateResult {
  return toolCallsGeneration(modelID, [{ toolName, input }], 'structured-output-call');
}

function toolCallsGeneration(
  modelID: string,
  toolCalls: Array<{ toolName: string; input: string }>,
  firstToolCallID = 'call-0',
): LanguageModelV3GenerateResult {
  return {
    content: toolCalls.map((toolCall, index) => ({
      type: 'tool-call',
      toolCallId: index === 0 ? firstToolCallID : `call-${index}`,
      toolName: toolCall.toolName,
      input: toolCall.input,
    })),
    finishReason: { unified: 'tool-calls', raw: 'tool_calls' },
    usage: defaultUsage(),
    response: { modelId: modelID },
    warnings: [],
  };
}

function chatLanguageModel(modelID: string): MockLanguageModelV3 {
  return generationBackedLanguageModel(modelID, () => ({
    content: [{
      type: 'tool-call',
      toolCallId: 'call-2',
      toolName: 'lookup',
      input: '{"key":"result"}',
    }],
    finishReason: { unified: 'tool-calls', raw: 'tool_calls' },
    usage: defaultUsage(),
    response: { modelId: modelID, headers: { 'x-test': 'ok' } },
    providerMetadata: { openrouter: { trace: 'test' } },
    warnings: [],
  }));
}

function usageAccountingChatLanguageModel(modelID: string): MockLanguageModelV3 {
  return generationBackedLanguageModel(modelID, () => ({
    content: [{ type: 'text', text: 'The answer is ready.' }],
    finishReason: { unified: 'stop', raw: 'stop' },
    usage: defaultUsage(),
    response: { modelId: modelID },
    providerMetadata: {
      openrouter: {
        usage: { promptTokens: 10, completionTokens: 5, totalTokens: 15, cost: 0.0042, costDetails: { upstreamInferenceCost: 0.0031 } },
      },
    },
    warnings: [],
  }));
}

function emptyLanguageModel(modelID: string): MockLanguageModelV3 {
  return generationBackedLanguageModel(modelID, () => ({
    content: [],
    finishReason: { unified: 'stop', raw: 'stop' },
    usage: defaultUsage(),
    response: { modelId: modelID },
    warnings: [],
  }));
}

function textLanguageModel(modelID: string): MockLanguageModelV3 {
  return generationBackedLanguageModel(modelID, () => ({
    content: [{ type: 'text', text: 'The answer is ready.' }],
    finishReason: { unified: 'stop', raw: 'stop' },
    usage: defaultUsage(),
    response: { modelId: modelID },
    warnings: [],
  }));
}

function nestedChatRequest(): ChatCompletionRequest {
  return {
    ...chatRequest,
    tools: [{
      type: 'function',
      function: {
        name: 'lookup',
        description: 'Look up a value.',
        parameters: {
          type: 'object',
          properties: {
            details: {
              type: 'object',
              properties: { count: { type: 'number' } },
              required: ['count'],
              additionalProperties: false,
            },
          },
          required: ['details'],
          additionalProperties: false,
        },
      },
    }],
  };
}

function multipleToolChatRequest(): ChatCompletionRequest {
  return {
    ...chatRequest,
    tools: [
      {
        type: 'function',
        function: {
          name: 'lookup',
          description: 'Look up a value.',
          parameters: { type: 'object', properties: { key: { type: 'string' } } },
        },
      },
      {
        type: 'function',
        function: {
          name: 'task_add',
          description: 'Add a task.',
          parameters: {
            type: 'object',
            properties: {
              title: { type: 'string' },
              endDate: { type: 'string' },
            },
            required: ['title'],
            additionalProperties: false,
          },
        },
      },
    ],
    toolChoice: { type: 'function', function: { name: 'task_add' } },
  };
}

function openTaskChatRequest(): ChatCompletionRequest {
  return {
    ...chatRequest,
    tools: [{
      type: 'function',
      function: {
        name: 'lookup',
        description: 'Look up a task.',
        parameters: {
          type: 'object',
          properties: {
            task: {
              type: 'object',
              properties: {
                title: { type: 'string' },
                priority: { type: 'number' },
              },
              required: ['title'],
            },
            items: {
              type: 'array',
              items: {
                type: 'object',
                properties: {
                  name: { type: 'string' },
                  label: { type: 'string' },
                },
                required: ['name'],
              },
            },
            optionalNote: { type: 'string' },
          },
          required: ['task', 'items'],
        },
      },
    }],
  };
}

function explicitOpenChatRequest(): ChatCompletionRequest {
  return {
    ...chatRequest,
    tools: [{
      type: 'function',
      function: {
        name: 'lookup',
        description: 'Look up a task.',
        parameters: {
          type: 'object',
          properties: {
            metadata: { type: 'object', additionalProperties: true },
            labels: { type: 'object', additionalProperties: { type: 'string' } },
          },
          required: ['metadata', 'labels'],
        },
      },
    }],
  };
}

function nullNormalizationChatRequest(): ChatCompletionRequest {
  return {
    ...chatRequest,
    tools: [{
      type: 'function',
      function: {
        name: 'lookup',
        description: 'Look up a value.',
        parameters: {
          type: 'object',
          properties: {
            optionalText: { type: 'string' },
            requiredText: { type: 'string' },
            nullableText: { type: ['string', 'null'] },
            nested: {
              type: 'object',
              properties: {
                optionalCount: { type: 'number' },
                requiredCount: { type: 'number' },
              },
              required: ['requiredCount'],
            },
            rows: {
              type: 'array',
              items: {
                type: 'object',
                properties: {
                  optionalLabel: { type: 'string' },
                  requiredLabel: { type: 'string' },
                  nullableLabel: { type: ['string', 'null'] },
                },
                required: ['requiredLabel'],
              },
            },
            metadata: { type: 'object', additionalProperties: { type: 'string' } },
          },
          required: ['requiredText', 'nested', 'rows'],
        },
      },
    }],
  };
}

function nestedStructuredRequest(): StructuredResponseRequest {
  return {
    ...structuredRequest,
    structuredOutputSchema: {
      ...structuredRequest.structuredOutputSchema,
      document: {
        type: 'object',
        properties: {
          details: {
            type: 'object',
            properties: { count: { type: 'number' } },
            required: ['count'],
            additionalProperties: false,
          },
        },
        required: ['details'],
        additionalProperties: false,
      },
    },
  };
}

function retryFailingLanguageModel(
  routeName: string,
  isRetryable: boolean,
  routeAttempts: string[],
): MockLanguageModelV3 {
  const apiCallError = providerAPICallError(isRetryable);
  const fail = () => {
    routeAttempts.push(routeName);
    throw new RetryError({
      message: 'provider retries failed',
      reason: isRetryable ? 'maxRetriesExceeded' : 'errorNotRetryable',
      errors: [apiCallError],
    });
  };
  return new MockLanguageModelV3({
    doGenerate: async () => fail(),
    doStream: async () => fail(),
  });
}

function apiFailingLanguageModel(
  routeName: string,
  isRetryable: boolean,
  routeAttempts: string[],
  statusCode?: number,
): MockLanguageModelV3 {
  const fail = () => {
    routeAttempts.push(routeName);
    throw providerAPICallError(isRetryable, statusCode);
  };
  return new MockLanguageModelV3({
    doGenerate: async () => fail(),
    doStream: async () => fail(),
  });
}

function providerAPICallError(isRetryable: boolean, statusCode?: number): APICallError {
  return new APICallError({
    message: 'provider request failed',
    url: 'https://provider.invalid',
    requestBodyValues: {},
    isRetryable,
    statusCode,
  });
}

function successfulGeneration(
  modelID: string,
  output: unknown,
  usage: LanguageModelV3Usage,
): LanguageModelV3GenerateResult {
  return {
    content: [{
      type: 'tool-call',
      toolCallId: 'structured-output-call',
      toolName: 'provider_test_output',
      input: JSON.stringify(output),
    }],
    finishReason: { unified: 'tool-calls', raw: 'tool_calls' },
    usage,
    response: { modelId: modelID },
    warnings: [],
  };
}

function defaultUsage(): LanguageModelV3Usage {
  return {
    inputTokens: { total: 10, noCache: 10, cacheRead: undefined, cacheWrite: undefined },
    outputTokens: { total: 5, text: 5, reasoning: undefined },
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

type WireSystemMessage = { role: 'system'; content: Array<{ cache_control?: { type: string } }> };

function isWireSystemMessage(value: unknown): value is WireSystemMessage {
  return isRecord(value) && value.role === 'system' && Array.isArray(value.content);
}
