import { createOpenAICompatible } from '@ai-sdk/openai-compatible';
import type {
  JSONSchema7,
  JSONValue,
  LanguageModelV3,
  LanguageModelV3GenerateResult,
  LanguageModelV3StreamPart,
  LanguageModelV3ToolCall,
  SharedV3ProviderOptions,
} from '@ai-sdk/provider';
import { createOpenRouter } from '@openrouter/ai-sdk-provider';
import {
  ChatCompletionFinishReason,
  ExecutionMode,
  LanguageModelBackend,
  StructuredOutputDiagnosticCategory,
  StructuredOutputRepairStatus,
  StructuredOutputConstraintMode,
  StructuredOutputValidationCode,
  structuredOutputSchemaSchema,
} from '@blueclaw/protocol';
import type {
  ChatCompletionRequest,
  ChatCompletionResponse,
  StructuredResponse,
  StructuredResponseRequest,
  StructuredOutputDiagnostic,
  StructuredOutputValidationIssue,
} from '@blueclaw/protocol';
import {
  generateText,
  InvalidToolInputError,
  jsonSchema,
  JSONParseError,
  Output,
  streamText,
  type ToolChoice,
  type ModelMessage,
  type SystemModelMessage,
  TypeValidationError,
  wrapLanguageModel,
} from 'ai';
import Ajv, { type ErrorObject } from 'ajv/dist/2020.js';

import { LLMDAutoRoute, LLMDStructuredOutputMode, type LLMDConfiguration } from './configuration.ts';
import { classifyLLMDError, isRetryableProviderError, LLMDError } from './errors.ts';

const providerCacheControlBreakpoint: SharedV3ProviderOptions = {
  openrouter: { cacheControl: { type: 'ephemeral' } },
};

type ProviderRoute = {
  constraintMode?: StructuredOutputConstraintMode;
  languageModel: LanguageModelV3;
  modelName: string;
  providerName: 'llama.cpp' | 'openrouter';
  selectedBackend: LanguageModelBackend;
};

export type ProviderLanguageModelFactory = {
  createLlamaLanguageModel(modelName: string, baseURL: string, apiKey: string, parallelToolCalls?: boolean): LanguageModelV3;
  createOpenRouterLanguageModel(modelName: string, baseURL: string, apiKey: string, parallelToolCalls?: boolean): LanguageModelV3;
};

export type StructuredResponseGenerator = (request: StructuredResponseRequest, abortSignal?: AbortSignal) => Promise<StructuredResponse>;
export type ChatCompletionGenerator = (request: ChatCompletionRequest, abortSignal?: AbortSignal) => Promise<ChatCompletionResponse>;

type ProviderRequest = StructuredResponseRequest | ChatCompletionRequest;
type GenerationOptions = NonNullable<ProviderRequest['generationOptions']>;

type DynamicTool = {
  description?: string;
  inputSchema: ReturnType<typeof jsonSchema>;
};

type DynamicToolSet = Record<string, DynamicTool>;
type ChatProviderMetadata = NonNullable<ChatCompletionResponse['providerMetadata']>;

export function createStructuredResponseGenerator(
  configuration: LLMDConfiguration,
  languageModelFactory: ProviderLanguageModelFactory = defaultLanguageModelFactory,
): StructuredResponseGenerator {
  const idleTimeoutMs = configuration.streamIdleTimeoutMs ?? defaultStreamIdleTimeoutMs;
  return async (request, abortSignal) => {
    throwIfAborted(abortSignal);
    validateStructuredOutputRequest(request);
    const routes = resolveProviderRoutes(request, configuration, languageModelFactory);
    let lastError: unknown;
    for (const route of routes) {
      throwIfAborted(abortSignal);
      try {
        return await generateStructuredForRoute(request, route, idleTimeoutMs, abortSignal);
      } catch (errorValue) {
        lastError = errorValue;
        if (abortSignal?.aborted) throw errorValue;
        if (!isRetryableProviderError(errorValue)) break;
      }
    }
    if (lastError !== undefined) throw classifyLLMDError(lastError);
    throw new LLMDError('configuration_invalid', 503, false, 'no configured language model route accepted the request');
  };
}

export function createChatCompletionGenerator(
  configuration: LLMDConfiguration,
  languageModelFactory: ProviderLanguageModelFactory = defaultLanguageModelFactory,
): ChatCompletionGenerator {
  const idleTimeoutMs = configuration.streamIdleTimeoutMs ?? defaultStreamIdleTimeoutMs;
  return async (request, abortSignal) => {
    throwIfAborted(abortSignal);
    const routes = resolveProviderRoutes(request, configuration, languageModelFactory, false);
    let lastError: unknown;
    for (const route of routes) {
      throwIfAborted(abortSignal);
      try {
        return await generateChatForRoute(request, route, idleTimeoutMs, abortSignal);
      } catch (errorValue) {
        lastError = errorValue;
        if (abortSignal?.aborted) throw errorValue;
        if (!isRetryableProviderError(errorValue)) break;
      }
    }
    if (lastError !== undefined) throw classifyLLMDError(lastError);
    throw new LLMDError('configuration_invalid', 503, false, 'no configured language model route accepted the request');
  };
}

function resolveProviderRoutes(
  request: ProviderRequest,
  configuration: LLMDConfiguration,
  languageModelFactory: ProviderLanguageModelFactory,
  requireStructuredOutputs = true,
): ProviderRoute[] {
  const parallelToolCalls = parallelToolCallsForRoute(request, requireStructuredOutputs);
  if (request.executionMode === ExecutionMode.Device) return [createLlamaRoute(configuration, languageModelFactory, requireStructuredOutputs, parallelToolCalls)];
  if (request.executionMode === ExecutionMode.Remote) {
    if (configuration.localOnly) {
      throw new LLMDError('policy_remote_disabled', 403, false, 'remote routing is disabled by local-only mode');
    }
    return [createOpenRouterRoute(request, configuration, languageModelFactory, parallelToolCalls)];
  }
  if (request.executionMode === ExecutionMode.Companion) throw new Error('companion language model routing is provided by the host runtime, not by llmd');
  const routes = configuration.localOnly
    ? [optionalLlamaRoute(configuration, languageModelFactory, requireStructuredOutputs, parallelToolCalls)]
    : configuration.autoRoute === LLMDAutoRoute.LocalFirst
      ? [
          optionalLlamaRoute(configuration, languageModelFactory, requireStructuredOutputs, parallelToolCalls),
          optionalOpenRouterRoute(request, configuration, languageModelFactory, parallelToolCalls),
        ]
      : remoteRouteWithoutLocalFallback(request, configuration, languageModelFactory, requireStructuredOutputs, parallelToolCalls);
  const configuredRoutes = routes.filter(route => route !== undefined);
  if (configuredRoutes.length === 0) {
    throw new LLMDError('configuration_invalid', 503, false, 'auto routing requires an OpenRouter or llama.cpp configuration');
  }
  return configuredRoutes;
}

function remoteRouteWithoutLocalFallback(
  request: ProviderRequest,
  configuration: LLMDConfiguration,
  languageModelFactory: ProviderLanguageModelFactory,
  requireStructuredOutputs: boolean,
  parallelToolCalls?: boolean,
): Array<ProviderRoute | undefined> {
  const remoteRoute = optionalOpenRouterRoute(request, configuration, languageModelFactory, parallelToolCalls);
  if (remoteRoute) return [remoteRoute];
  return [optionalLlamaRoute(configuration, languageModelFactory, requireStructuredOutputs, parallelToolCalls)];
}

function parallelToolCallsForRoute(request: ProviderRequest, requireStructuredOutputs: boolean): boolean | undefined {
  if (requireStructuredOutputs) return false;
  if ('parallelToolCalls' in request && typeof request.parallelToolCalls === 'boolean') return request.parallelToolCalls;
  return undefined;
}

function optionalLlamaRoute(
  configuration: LLMDConfiguration,
  languageModelFactory: ProviderLanguageModelFactory,
  requireStructuredOutputs = true,
  parallelToolCalls?: boolean,
): ProviderRoute | undefined {
  if (!configuration.llamaBaseURL || !configuration.llamaModel) {
    return undefined;
  }
  if (requireStructuredOutputs && !configuration.llamaStructuredOutputsEnabled) return undefined;
  return createLlamaRoute(configuration, languageModelFactory, requireStructuredOutputs, parallelToolCalls);
}

function optionalOpenRouterRoute(
  request: ProviderRequest,
  configuration: LLMDConfiguration,
  languageModelFactory: ProviderLanguageModelFactory,
  parallelToolCalls?: boolean,
): ProviderRoute | undefined {
  if (!configuration.openRouterAPIKey) return undefined;
  return createOpenRouterRoute(request, configuration, languageModelFactory, parallelToolCalls);
}

function structuredConstraintMode(configuration: LLMDConfiguration): StructuredOutputConstraintMode {
  return configuration.structuredOutputMode === LLMDStructuredOutputMode.ToolCall
    ? StructuredOutputConstraintMode.NativeToolCall
    : StructuredOutputConstraintMode.OpenAIJSONSchema;
}


function createLlamaRoute(
  configuration: LLMDConfiguration,
  languageModelFactory: ProviderLanguageModelFactory,
  requireStructuredOutputs = true,
  parallelToolCalls?: boolean,
): ProviderRoute {
  if (!configuration.llamaBaseURL || !configuration.llamaModel) {
    throw new LLMDError('configuration_invalid', 503, false, 'device routing requires BLUECLAW_LLMD_LLAMA_BASE_URL and BLUECLAW_LLMD_LLAMA_MODEL');
  }
  if (requireStructuredOutputs && !configuration.llamaStructuredOutputsEnabled) {
    throw new LLMDError('configuration_invalid', 503, false, 'device structured output routing requires explicit conformance enablement');
  }
  return {
    constraintMode: structuredConstraintMode(configuration),
    languageModel: languageModelFactory.createLlamaLanguageModel(
      configuration.llamaModel,
      configuration.llamaBaseURL,
      configuration.llamaAPIKey,
      parallelToolCalls,
    ),
    modelName: configuration.llamaModel,
    providerName: 'llama.cpp',
    selectedBackend: LanguageModelBackend.Device,
  };
}

function createOpenRouterRoute(
  request: ProviderRequest,
  configuration: LLMDConfiguration,
  languageModelFactory: ProviderLanguageModelFactory,
  parallelToolCalls?: boolean,
): ProviderRoute {
  if (!configuration.openRouterAPIKey) {
    throw new LLMDError('configuration_invalid', 503, false, 'remote routing requires OPENROUTER_API_KEY');
  }
  const modelName = request.model?.trim();
  if (!modelName) throw new LLMDError('request_invalid', 400, false, 'remote routing requires a model');
  return {
    constraintMode: structuredConstraintMode(configuration),
    languageModel: languageModelFactory.createOpenRouterLanguageModel(
      modelName,
      configuration.openRouterBaseURL,
      configuration.openRouterAPIKey,
      parallelToolCalls,
    ),
    modelName,
    providerName: 'openrouter',
    selectedBackend: LanguageModelBackend.Remote,
  };
}

const firstByteTimeoutMs = 90_000;

const fetchWithFirstByteTimeout = Object.assign(
  (input: string | URL | Request, requestInit?: RequestInit): Promise<Response> => {
    const firstByteController = new AbortController();
    const firstByteTimer = setTimeout(
      () => firstByteController.abort(new LLMDError('provider_unavailable', 503, true, `provider returned no response headers within ${firstByteTimeoutMs}ms`)),
      firstByteTimeoutMs,
    );
    const callerSignal = requestInit?.signal;
    const signal = callerSignal ? AbortSignal.any([callerSignal, firstByteController.signal]) : firstByteController.signal;
    return fetch(input, { ...requestInit, signal }).finally(() => clearTimeout(firstByteTimer));
  },
  { preconnect: fetch.preconnect },
);

const defaultLanguageModelFactory: ProviderLanguageModelFactory = {
  createLlamaLanguageModel(modelName, baseURL, apiKey, parallelToolCalls) {
    const provider = createOpenAICompatible({
      apiKey,
      baseURL,
      fetch: fetchWithFirstByteTimeout,
      name: 'llama',
      supportsStructuredOutputs: true,
      transformRequestBody: parallelToolCalls === undefined
        ? undefined
        : requestBody => ({ ...requestBody, parallel_tool_calls: parallelToolCalls }),
    });
    return provider.chatModel(modelName);
  },
  createOpenRouterLanguageModel(modelName, baseURL, apiKey, parallelToolCalls) {
    const provider = createOpenRouter({
      apiKey,
      baseURL,
      compatibility: 'strict',
      fetch: fetchWithFirstByteTimeout,
    });
    return provider.chat(modelName, {
      provider: { order: ['deepinfra', 'parasail'], allow_fallbacks: true, require_parameters: true },
      usage: { include: true },
    });
  },
};

const defaultStreamIdleTimeoutMs = 60_000;

async function generateWithIdleTimeout(
  options: Parameters<typeof streamText>[0],
  idleTimeoutMs: number,
  callerAbortSignal: AbortSignal | undefined,
) {
  const idleController = new AbortController();
  const abortSignal = callerAbortSignal ? AbortSignal.any([callerAbortSignal, idleController.signal]) : idleController.signal;
  const result = streamText({ ...options, abortSignal });
  let idleTimer: ReturnType<typeof setTimeout> | undefined;
  let capturedStreamError: unknown;
  const resetIdleTimer = (): void => {
    if (idleTimer !== undefined) clearTimeout(idleTimer);
    idleTimer = setTimeout(() => idleController.abort(streamStalledError(idleTimeoutMs)), idleTimeoutMs);
  };
  resetIdleTimer();
  try {
    for await (const chunk of result.fullStream) {
      if (chunk.type === 'error' && capturedStreamError === undefined) capturedStreamError = chunk.error;
      resetIdleTimer();
    }
  } finally {
    if (idleTimer !== undefined) clearTimeout(idleTimer);
  }
  if (capturedStreamError !== undefined) throw capturedStreamError;
  const [text, toolCalls, totalUsage, finishReason, response, providerMetadata] = await Promise.all([
    result.text,
    result.toolCalls,
    result.totalUsage,
    result.finishReason,
    result.response,
    result.providerMetadata,
  ]);
  const structuredOutput = options.experimental_output === undefined ? undefined : await result.output;
  return { text, toolCalls, totalUsage, finishReason, response, providerMetadata, structuredOutput };
}

function streamStalledError(idleTimeoutMs: number): LLMDError {
  return new LLMDError('provider_unavailable', 503, true, `provider stream stalled for ${idleTimeoutMs}ms`);
}

// Prefer the provider's own JSON schema constraint, and fall back to a forced
// tool call for providers or models that reject it.
async function generateStructuredForRoute(
  request: StructuredResponseRequest,
  route: ProviderRoute,
  idleTimeoutMs: number,
  abortSignal?: AbortSignal,
): Promise<StructuredResponse> {
  if (route.constraintMode !== StructuredOutputConstraintMode.OpenAIJSONSchema) {
    return generateForRoute(request, route, idleTimeoutMs, abortSignal);
  }
  try {
    return await generateNativeStructuredForRoute(request, route, idleTimeoutMs, abortSignal);
  } catch (errorValue) {
    if (abortSignal?.aborted) throw errorValue;
    if (!rejectsNativeStructuredOutput(errorValue)) throw errorValue;
    return generateForRoute(request, { ...route, constraintMode: StructuredOutputConstraintMode.NativeToolCall }, idleTimeoutMs, abortSignal);
  }
}

// A provider that advertises structured outputs can still refuse a particular
// schema or model; that refusal arrives as a request-shaped provider error.
function rejectsNativeStructuredOutput(errorValue: unknown): boolean {
  const message = errorValue instanceof Error ? errorValue.message.toLowerCase() : '';
  return message.includes('response_format') || message.includes('json_schema') || message.includes('structured output');
}

// A provider that constrains generation to a JSON schema needs no tool: the
// model answers with the object. Only providers without that capability have to
// be squeezed through a forced tool call.
async function generateNativeStructuredForRoute(
  request: StructuredResponseRequest,
  route: ProviderRoute,
  idleTimeoutMs: number,
  abortSignal?: AbortSignal,
): Promise<StructuredResponse> {
  const outputSchema = createValidatedJSONSchema(request.structuredOutputSchema.document);
  const result = await generateWithIdleTimeout({
    model: route.languageModel,
    system: systemMessages(request),
    messages: convertConversationMessages(request),
    experimental_output: Output.object({ schema: outputSchema }),
    maxOutputTokens: request.generationOptions?.maxTokens,
    maxRetries: 0,
    seed: request.generationOptions?.seed,
    temperature: request.generationOptions?.temperature,
  }, idleTimeoutMs, abortSignal);
  throwIfAborted(abortSignal);
  if (result.structuredOutput === undefined) {
    throw new LLMDError(
      'structured_output_invalid',
      422,
      false,
      'structured output generation returned no object',
      {
        category: StructuredOutputDiagnosticCategory.EmptyCompletion,
        finishReason: normalizeChatFinishReason(result.finishReason),
      },
    );
  }
  return {
    provider: route.providerName,
    model: result.response.modelId || route.modelName,
    content: serializeStructuredOutput(result.structuredOutput),
    selectedBackend: route.selectedBackend,
    finishReason: 'stop',
    constraintMode: StructuredOutputConstraintMode.OpenAIJSONSchema,
    usage: normalizeUsage(result.totalUsage, result.providerMetadata),
  };
}

async function generateForRoute(
  request: StructuredResponseRequest,
  route: ProviderRoute,
  idleTimeoutMs: number,
  abortSignal?: AbortSignal,
): Promise<StructuredResponse> {
  const outputSchema = createValidatedJSONSchema(request.structuredOutputSchema.document);
  const outputToolName = request.structuredOutputSchema.name;
  const tools = { [outputToolName]: { inputSchema: outputSchema } };
  const messages = convertConversationMessages(request);
  const system = systemMessages(request);
  let repairAttempted = false;
  let result;
  try {
    result = await generateWithIdleTimeout({
      model: route.languageModel,
      system,
      messages,
      tools,
      toolChoice: { type: 'tool', toolName: outputToolName },
      maxOutputTokens: request.generationOptions?.maxTokens,
      maxRetries: 0,
      experimental_repairToolCall: async ({ toolCall, error, inputSchema }) => {
        if (abortSignal?.aborted || repairAttempted || !InvalidToolInputError.isInstance(error) || toolCall.toolName !== outputToolName) return null;
        repairAttempted = true;
        return repairInvalidToolCall({
          route,
          tools,
          toolName: outputToolName,
          toolCall,
          messages,
          system,
          generationOptions: request.generationOptions,
          abortSignal,
          inputSchema,
          error,
        });
      },
      seed: request.generationOptions?.seed,
      temperature: request.generationOptions?.temperature,
    }, idleTimeoutMs, abortSignal);
  } catch (errorValue) {
    if (abortSignal?.aborted) throwIfAborted(abortSignal);
    throw errorValue;
  }
  throwIfAborted(abortSignal);
  const toolCall = requireStructuredOutputToolCall(result, outputToolName);
  const content = serializeStructuredOutput(toolCall.input);
  return {
    provider: route.providerName,
    model: result.response.modelId || route.modelName,
    content,
    selectedBackend: route.selectedBackend,
    finishReason: 'stop',
    constraintMode: route.constraintMode,
    usage: normalizeUsage(result.totalUsage, result.providerMetadata),
  };
}

type ToolRepairRequest = {
  route: ProviderRoute;
  tools: DynamicToolSet;
  toolName: string;
  toolCall: LanguageModelV3ToolCall;
  messages: ModelMessage[];
  system: SystemModelMessage[] | undefined;
  generationOptions: GenerationOptions | undefined;
  inputSchema: JSONSchema7;
  validationCategory: StructuredOutputDiagnosticCategory;
  validationFailure: string;
  abortSignal?: AbortSignal;
};

type ToolRepairCallbackRequest = Omit<ToolRepairRequest, 'inputSchema' | 'validationCategory' | 'validationFailure'> & {
  error: unknown;
  inputSchema: ({ toolName }: { toolName: string }) => PromiseLike<JSONSchema7>;
};

async function repairInvalidToolCall({ inputSchema, error, ...repairRequest }: ToolRepairCallbackRequest): Promise<LanguageModelV3ToolCall | null> {
  try {
    const schemaDocument = await inputSchema({ toolName: repairRequest.toolName });
    return repairToolCall({
      ...repairRequest,
      tools: repairToolSet(repairRequest.tools, repairRequest.toolName),
      inputSchema: createClosedJSONSchema(schemaDocument),
      validationCategory: diagnoseInvalidToolCall(error).category,
      validationFailure: validationFailureMessage(error),
    });
  } catch {
    throwIfAborted(repairRequest.abortSignal);
    return null;
  }
}

async function repairToolCall(repairRequest: ToolRepairRequest): Promise<LanguageModelV3ToolCall | null> {
  throwIfAborted(repairRequest.abortSignal);
  const repairTimeoutSignal = AbortSignal.timeout(defaultStreamIdleTimeoutMs);
  const repairAbortSignal = repairRequest.abortSignal
    ? AbortSignal.any([repairRequest.abortSignal, repairTimeoutSignal])
    : repairTimeoutSignal;
  try {
    const result = await generateText({
      model: repairRequest.route.languageModel,
      system: repairSystem(repairRequest.system),
      messages: repairMessages(repairRequest),
      tools: repairRequest.tools,
      toolChoice: { type: 'tool', toolName: repairRequest.toolName },
      maxOutputTokens: repairRequest.generationOptions?.maxTokens,
      maxRetries: 0,
      abortSignal: repairAbortSignal,
      seed: repairRequest.generationOptions?.seed,
      temperature: repairRequest.generationOptions?.temperature,
    });
    throwIfAborted(repairRequest.abortSignal);
    const repairedToolCall = requireStructuredOutputToolCall(result, repairRequest.toolName);
    return { ...repairRequest.toolCall, input: serializeStructuredOutput(repairedToolCall.input) };
  } catch {
    throwIfAborted(repairRequest.abortSignal);
    return null;
  }
}

function repairMessages(repairRequest: ToolRepairRequest): ModelMessage[] {
  return [
    ...repairRequest.messages,
    {
      role: 'assistant',
      content: [{
        type: 'tool-call',
        toolCallId: repairRequest.toolCall.toolCallId,
        toolName: repairRequest.toolName,
        input: repairToolCallInput(repairRequest.toolCall.input),
      }],
    },
    {
      role: 'tool',
      content: [{
        type: 'tool-result',
        toolCallId: repairRequest.toolCall.toolCallId,
        toolName: repairRequest.toolName,
        output: {
          type: 'error-text',
          value: [
            `Malformed arguments: ${repairRequest.toolCall.input}`,
            `Closed JSON schema: ${JSON.stringify(repairRequest.inputSchema)}`,
            `Validation category: ${repairRequest.validationCategory}`,
            `Validation failure: ${repairRequest.validationFailure}`,
          ].join('\n'),
        },
      }],
    },
  ];
}

function repairToolSet(tools: DynamicToolSet, toolName: string): DynamicToolSet {
  const tool = tools[toolName];
  if (tool === undefined) return {};
  return { [toolName]: tool };
}

function repairToolCallInput(input: string): JSONValue {
  const parsedInput = parseJSONValue(input);
  return isJSONValue(parsedInput) ? parsedInput : {};
}

function validationFailureMessage(errorValue: unknown): string {
  if (!InvalidToolInputError.isInstance(errorValue)) return 'tool arguments do not match the schema';
  if (JSONParseError.isInstance(errorValue.cause)) return 'tool arguments are not valid JSON';
  if (!TypeValidationError.isInstance(errorValue.cause)) return 'tool arguments do not match the schema';
  const validationCause = errorValue.cause.cause;
  if (!(validationCause instanceof Error)) return 'tool arguments do not match the schema';
  return validationCause.message.trim() || 'tool arguments do not match the schema';
}

function repairSystem(system: SystemModelMessage[] | undefined): SystemModelMessage[] {
  const instruction = 'Repair the previous tool call using the closed JSON schema in the repair prompt and return exactly one forced tool call.';
  return [...(system ?? []), { role: 'system', content: instruction }];
}

function serializeStructuredOutput(value: unknown): string {
  try {
    const content = JSON.stringify(value);
    if (content !== undefined) return content;
  } catch {
    throw structuredOutputSerializationError();
  }
  throw structuredOutputSerializationError();
}

function structuredOutputSerializationError(): LLMDError {
  return new LLMDError(
    'structured_output_invalid',
    422,
    false,
    'structured output tool arguments are not serializable',
    { category: StructuredOutputDiagnosticCategory.Serialization },
  );
}

function requireStructuredOutputToolCall(
  result: StructuredOutputToolResult,
  toolName: string,
) {
  if (result.finishReason !== 'tool-calls') {
    if (isEmptyCompletion(result)) throw emptyCompletionError(result, 'structured output generation');
    throw new LLMDError(
      'structured_output_invalid',
      422,
      false,
      `structured output generation finished with ${result.finishReason}`,
      {
        category: StructuredOutputDiagnosticCategory.FinishReason,
        finishReason: normalizeChatFinishReason(result.finishReason),
      },
    );
  }
  if (result.toolCalls.length !== 1) {
    throw new LLMDError(
      'structured_output_invalid',
      422,
      false,
      'structured output generation must return exactly one tool call',
      { category: StructuredOutputDiagnosticCategory.ToolCallContract },
    );
  }
  const toolCall = result.toolCalls[0];
  if (toolCall === undefined || toolCall.toolName !== toolName) {
    throw new LLMDError(
      'structured_output_invalid',
      422,
      false,
      'structured output generation returned an invalid tool call',
      { category: StructuredOutputDiagnosticCategory.ToolCallContract },
    );
  }
  if (toolCall.invalid) {
    throw new LLMDError(
      'structured_output_invalid',
      422,
      false,
      'structured output generation returned schema-invalid arguments',
      diagnoseInvalidToolCall(toolCall.error),
    );
  }
  return toolCall;
}

function diagnoseInvalidToolCall(
  errorValue: unknown,
  toolName?: string,
  repairStatus?: StructuredOutputRepairStatus,
): StructuredOutputDiagnostic {
  const tool = toolName === undefined ? {} : { toolName };
  const repair = repairStatus === undefined ? {} : { repairStatus };
  if (!InvalidToolInputError.isInstance(errorValue)) {
    return { category: StructuredOutputDiagnosticCategory.SchemaValidation, ...tool, ...repair };
  }
  if (JSONParseError.isInstance(errorValue.cause)) {
    return { category: StructuredOutputDiagnosticCategory.JSONParse, ...tool, ...repair };
  }
  return {
    category: StructuredOutputDiagnosticCategory.SchemaValidation,
    ...tool,
    validationIssues: validationIssuesFromError(errorValue),
    ...repair,
  };
}

class JSONSchemaValidationError extends Error {
  constructor(readonly validationIssues: StructuredOutputValidationIssue[], message: string) {
    super(message);
    this.name = 'JSONSchemaValidationError';
  }
}

function validationIssuesFromError(errorValue: unknown): StructuredOutputValidationIssue[] {
  if (!InvalidToolInputError.isInstance(errorValue) || !TypeValidationError.isInstance(errorValue.cause)) return [];
  const validationError = errorValue.cause.cause;
  return validationError instanceof JSONSchemaValidationError ? validationError.validationIssues : [];
}

function validationIssuesFromAJV(errors: ErrorObject[] | null | undefined): StructuredOutputValidationIssue[] {
  return (errors ?? []).slice(0, 8).map(errorValue => ({
    fieldPath: validationFieldPath(errorValue),
    code: validationCode(errorValue.keyword),
  }));
}

const safeFieldPathPattern = /^\/(?:[A-Za-z0-9_.$~-]+(?:\/[A-Za-z0-9_.$~-]+)*)?$/;

function validationFieldPath(errorValue: ErrorObject): string {
  const instancePath = safeFieldPathPattern.test(errorValue.instancePath) ? errorValue.instancePath : '';
  if (errorValue.keyword === 'required') {
    return validationPropertyPath(instancePath, errorValue.params.missingProperty);
  }
  if (errorValue.keyword === 'additionalProperties') {
    return validationPropertyPath(instancePath, errorValue.params.additionalProperty);
  }
  return instancePath || '/';
}

function validationPropertyPath(instancePath: string, property: unknown): string {
  if (typeof property !== 'string' || !safeFieldPathPattern.test(`/${property}`)) return instancePath || '/';
  return `${instancePath}/${property}`;
}

function validationCode(keyword: string): StructuredOutputValidationCode {
  if (keyword === 'required') return StructuredOutputValidationCode.Required;
  if (keyword === 'additionalProperties') return StructuredOutputValidationCode.AdditionalProperty;
  if (keyword === 'type') return StructuredOutputValidationCode.Type;
  return StructuredOutputValidationCode.Other;
}

type StructuredOutputToolResult = {
  text: string;
  finishReason: string;
  toolCalls: Array<{ toolName: string; input: unknown; invalid?: boolean; error?: unknown }>;
};

async function generateChatForRoute(
  request: ChatCompletionRequest,
  route: ProviderRoute,
  idleTimeoutMs: number,
  abortSignal?: AbortSignal,
): Promise<ChatCompletionResponse> {
  const tools = createChatTools(request);
  const requestedToolChoice = convertToolChoice(request.toolChoice, Object.keys(tools));
  const providerChoice = providerToolChoice(
    requestedToolChoice,
    Object.keys(tools),
    request.parallelToolCalls,
  );
  const messages = convertChatMessages(request);
  const system = systemMessages(request);
  const chatRoute = routeForChatRequest(request, route, requestedToolChoice);
  let repairAttempted = false;
  const repairStatuses = new Map<string, StructuredOutputRepairStatus>();
  const result = await generateWithIdleTimeout({
    model: chatRoute.languageModel,
    system,
    messages,
    tools: Object.keys(tools).length > 0 ? tools : undefined,
    toolChoice: providerChoice,
    maxOutputTokens: request.generationOptions?.maxTokens,
    maxRetries: 0,
    experimental_repairToolCall: async ({ toolCall, error, inputSchema }) => {
      if (repairAttempted || !InvalidToolInputError.isInstance(error) || tools[toolCall.toolName] === undefined) return null;
      repairAttempted = true;
      const repairedToolCall = await repairInvalidToolCall({
        route: chatRoute,
        tools,
        toolName: toolCall.toolName,
        toolCall,
        messages,
        system,
        generationOptions: request.generationOptions,
        abortSignal,
        inputSchema,
        error,
      });
      if (repairedToolCall === null) repairStatuses.set(toolCall.toolCallId, StructuredOutputRepairStatus.Failed);
      return repairedToolCall;
    },
    seed: request.generationOptions?.seed,
    temperature: request.generationOptions?.temperature,
  }, idleTimeoutMs, abortSignal);
  throwIfAborted(abortSignal);
  const canonicalToolNames = Object.keys(tools);
  requireChatToolChoice(result, convertToolChoice(request.toolChoice, canonicalToolNames), canonicalToolNames);
  for (const toolCall of result.toolCalls) {
    if (!toolCall.invalid) continue;
    const toolName = tools[toolCall.toolName] === undefined ? undefined : toolCall.toolName;
    throw new LLMDError(
      'provider_response_invalid',
      502,
      false,
      'provider returned schema-invalid tool arguments',
      diagnoseInvalidToolCall(
        toolCall.error,
        toolName,
        repairStatuses.get(toolCall.toolCallId) ?? StructuredOutputRepairStatus.NotAttempted,
      ),
    );
  }
  return {
    provider: route.providerName,
    model: result.response.modelId || route.modelName,
    message: {
      role: 'assistant',
      content: result.text,
      toolCalls: result.toolCalls.map(toolCall => ({
        id: toolCall.toolCallId,
        type: 'function',
        function: {
          name: toolCall.toolName,
          arguments: JSON.stringify(toolCall.input) ?? '{}',
        },
      })),
    },
    selectedBackend: route.selectedBackend,
    finishReason: normalizeChatFinishReason(result.finishReason),
    usage: normalizeUsage(result.totalUsage, result.providerMetadata),
    providerMetadata: serializableProviderMetadata(result.providerMetadata),
  };
}

function providerToolChoice(
  toolChoice: ToolChoice<DynamicToolSet> | undefined,
  toolNames: string[],
  parallelToolCalls: boolean | undefined,
): ToolChoice<DynamicToolSet> | undefined {
  if (toolChoice !== 'required' || toolNames.length !== 1 || parallelToolCalls !== false) return toolChoice;
  const [toolName] = toolNames;
  if (toolName === undefined) return toolChoice;
  return { type: 'tool', toolName };
}

function routeForChatRequest(
  request: ChatCompletionRequest,
  route: ProviderRoute,
  requestedToolChoice: ToolChoice<DynamicToolSet> | undefined,
): ProviderRoute {
  if (request.parallelToolCalls !== false || isNamedToolChoice(requestedToolChoice)) return route;
  return { ...route, languageModel: firstToolCallLanguageModel(route.languageModel) };
}

function firstToolCallLanguageModel(languageModel: LanguageModelV3): LanguageModelV3 {
  return wrapLanguageModel({
    model: languageModel,
    middleware: {
      specificationVersion: 'v3',
      wrapGenerate: async ({ doGenerate }) => keepFirstToolCall(await doGenerate()),
      wrapStream: async ({ doStream }) => {
        const result = await doStream();
        return { ...result, stream: keepFirstToolCallStream(result.stream) };
      },
    },
  });
}

function keepFirstToolCallStream(
  stream: ReadableStream<LanguageModelV3StreamPart>,
): ReadableStream<LanguageModelV3StreamPart> {
  let hasKeptToolCall = false;
  return stream.pipeThrough(new TransformStream<LanguageModelV3StreamPart, LanguageModelV3StreamPart>({
    transform(part, controller) {
      if (part.type !== 'tool-call') {
        controller.enqueue(part);
        return;
      }
      if (hasKeptToolCall) return;
      hasKeptToolCall = true;
      controller.enqueue(part);
    },
  }));
}

function keepFirstToolCall(result: LanguageModelV3GenerateResult): LanguageModelV3GenerateResult {
  const toolCalls = result.content.filter(
    (content): content is LanguageModelV3ToolCall => content.type === 'tool-call',
  );
  const [keptToolCall, ...droppedToolCalls] = toolCalls;
  if (keptToolCall === undefined || droppedToolCalls.length === 0) return result;
  const canonicalName = (toolCall: LanguageModelV3ToolCall) => toolCall.toolName;
  console.warn(
    `llmd: parallel tool calls disabled, dropping ${droppedToolCalls.length} tool call(s) [${droppedToolCalls.map(canonicalName).join(', ')}] and keeping [${canonicalName(keptToolCall)}]`,
  );
  return {
    ...result,
    content: result.content.filter(content => content.type !== 'tool-call' || content === keptToolCall),
  };
}

function requireChatToolChoice(
  result: StructuredOutputToolResult,
  toolChoice: ToolChoice<DynamicToolSet> | undefined,
  toolNames: string[],
): void {
  if (toolChoice === 'required') {
    requireToolCallFinishReason(result, 'required tool choice');
    if (result.toolCalls.length === 0) {
      throw toolCallContractError('required tool choice did not return a tool call');
    }
    const onlyToolName = toolNames.length === 1 ? toolNames[0] : undefined;
    if (onlyToolName !== undefined && result.toolCalls.some(toolCall => toolCall.toolName !== onlyToolName)) {
      throw toolCallContractError(`required single tool choice ${onlyToolName} returned a different tool`);
    }
    return;
  }
  if (!isNamedToolChoice(toolChoice)) return;
  requireToolCallFinishReason(result, `named tool choice ${toolChoice.toolName}`);
  if (result.toolCalls.length !== 1) {
    throw toolCallContractError(`named tool choice ${toolChoice.toolName} must return exactly one tool call`);
  }
  if (result.toolCalls[0]?.toolName !== toolChoice.toolName) {
    throw toolCallContractError(`named tool choice ${toolChoice.toolName} returned a different tool`);
  }
}

function isNamedToolChoice(
  toolChoice: ToolChoice<DynamicToolSet> | undefined,
): toolChoice is { type: 'tool'; toolName: string } {
  return typeof toolChoice === 'object' && toolChoice.type === 'tool';
}

function requireToolCallFinishReason(result: StructuredOutputToolResult, subject: string): void {
  if (result.finishReason === 'tool-calls') return;
  if (isEmptyCompletion(result)) throw emptyCompletionError(result, subject);
  throw new LLMDError(
    'structured_output_invalid',
    422,
    false,
    `${subject} finished with ${result.finishReason}`,
    {
      category: StructuredOutputDiagnosticCategory.FinishReason,
      finishReason: normalizeChatFinishReason(result.finishReason),
    },
  );
}

function isEmptyCompletion(result: StructuredOutputToolResult): boolean {
  return result.text.trim() === '' && result.toolCalls.length === 0;
}

function emptyCompletionError(result: StructuredOutputToolResult, subject: string): LLMDError {
  return new LLMDError(
    'structured_output_invalid',
    422,
    false,
    `${subject} returned no text and no tool call`,
    {
      category: StructuredOutputDiagnosticCategory.EmptyCompletion,
      finishReason: normalizeChatFinishReason(result.finishReason),
    },
  );
}

function toolCallContractError(message: string): LLMDError {
  return new LLMDError(
    'structured_output_invalid',
    422,
    false,
    message,
    { category: StructuredOutputDiagnosticCategory.ToolCallContract },
  );
}

// OpenAI-family endpoints accept function names matching ^[a-zA-Z0-9_-]+$ and
// reject the entire request otherwise. Blueclaw tool names are namespace.name
// and that dot is identity, so the source cannot rename them. Carry an exact
// per-request map rather than guessing the way back from a sanitized name.
function createChatTools(request: ChatCompletionRequest): DynamicToolSet {
  const tools: DynamicToolSet = {};
  for (const tool of request.tools ?? []) {
    const parameters = tool.function.parameters;
    if (!isJSONSchema(parameters)) {
      throw new LLMDError('request_invalid', 400, false, `tool ${tool.function.name} parameters must be a JSON schema object`);
    }
    tools[tool.function.name] = {
      description: tool.function.description,
      inputSchema: createValidatedJSONSchema(parameters),
    };
  }
  return tools;
}

function throwIfAborted(abortSignal: AbortSignal | undefined): void {
  if (abortSignal?.aborted) throw new DOMException('The operation was aborted', 'AbortError');
}

function convertToolChoice(toolChoice: unknown, toolNames: string[]): ToolChoice<DynamicToolSet> | undefined {
  if (toolChoice === undefined || toolChoice === null) return undefined;
  if (toolChoice === 'auto' || toolChoice === 'none' || toolChoice === 'required') return toolChoice;
  if (!isRecord(toolChoice) || toolChoice.type !== 'function' || !isRecord(toolChoice.function)) {
    throw new LLMDError('request_invalid', 400, false, 'tool choice must be auto, none, required, or a function choice');
  }
  const toolName = toolChoice.function.name;
  if (typeof toolName !== 'string' || toolName.trim() === '') {
    throw new LLMDError('request_invalid', 400, false, 'tool choice function name is required');
  }
  if (!toolNames.includes(toolName)) {
    throw new LLMDError('request_invalid', 400, false, `tool choice references unknown tool ${toolName}`);
  }
  return { type: 'tool', toolName };
}

function convertChatMessages(request: ChatCompletionRequest): ModelMessage[] {
  const toolNames = toolNamesByCallID(request);
  return request.messages.filter(message => message.role !== 'system').map(message => {
    if (message.role === 'user') return { role: 'user', content: userContent(message) };
    if (message.role === 'assistant') return assistantMessage(message);
    const toolName = toolNames.get(message.toolCallId ?? '');
    if (!toolName) throw new LLMDError('request_invalid', 400, false, `tool result ${message.toolCallId ?? ''} has no matching tool call`);
    return {
      role: 'tool',
      content: [{
        type: 'tool-result' as const,
        toolCallId: message.toolCallId ?? '',
        toolName,
        output: toolResultOutput(message.content ?? ''),
      }],
    };
  });
}

function assistantMessage(message: ChatCompletionRequest['messages'][number]): ModelMessage {
  const toolCalls = message.toolCalls ?? [];
  if (toolCalls.length === 0) return { role: 'assistant', content: message.content ?? '' };
  const content: Array<{ type: 'text'; text: string } | { type: 'tool-call'; toolCallId: string; toolName: string; input: unknown }> = [];
  if (message.content) content.push({ type: 'text', text: message.content });
  for (const toolCall of toolCalls) {
    content.push({
      type: 'tool-call',
      toolCallId: toolCall.id,
      toolName: toolCall.function.name,
      input: parseToolArguments(toolCall.function.arguments),
    });
  }
  return { role: 'assistant', content };
}

function toolNamesByCallID(request: ChatCompletionRequest): Map<string, string> {
  const toolNames = new Map<string, string>();
  for (const message of request.messages) {
    for (const toolCall of message.toolCalls ?? []) toolNames.set(toolCall.id, toolCall.function.name);
  }
  return toolNames;
}

function toolResultOutput(content: string) {
  const parsedContent = parseJSONValue(content);
  return isJSONValue(parsedContent) ? { type: 'json' as const, value: parsedContent } : { type: 'text' as const, value: content };
}

function parseToolArguments(argumentsText: string): unknown {
  const parsedArguments = parseJSONValue(argumentsText);
  if (parsedArguments === undefined) throw new LLMDError('request_invalid', 400, false, 'tool call arguments must be valid JSON');
  return parsedArguments;
}

function parseJSONValue(value: string): unknown {
  try {
    return JSON.parse(value) as unknown;
  } catch {
    return undefined;
  }
}

function normalizeChatFinishReason(finishReason: string): ChatCompletionFinishReason {
  const normalizedFinishReason = finishReason.replaceAll('-', '_');
  const finishReasonByValue: Record<string, ChatCompletionFinishReason> = {
    stop: ChatCompletionFinishReason.Stop,
    length: ChatCompletionFinishReason.Length,
    tool_calls: ChatCompletionFinishReason.ToolCalls,
    content_filter: ChatCompletionFinishReason.ContentFilter,
    error: ChatCompletionFinishReason.Error,
    other: ChatCompletionFinishReason.Other,
    unknown: ChatCompletionFinishReason.Unknown,
  };
  return finishReasonByValue[normalizedFinishReason] ?? ChatCompletionFinishReason.Unknown;
}

type UsageDocument = {
  inputTokens?: number;
  outputTokens?: number;
  totalTokens?: number;
  inputTokenDetails?: { cacheReadTokens?: number; cacheWriteTokens?: number };
  outputTokenDetails?: { reasoningTokens?: number };
};

type OpenRouterUsageAccountingDocument = {
  cost?: number;
  costDetails?: { upstreamInferenceCost: number };
};

function normalizeUsage(usage: UsageDocument, providerMetadata?: unknown) {
  const accounting = openRouterUsageAccounting(providerMetadata);
  return {
    promptTokens: normalizeTokenCount(usage.inputTokens),
    completionTokens: normalizeTokenCount(usage.outputTokens),
    totalTokens: normalizeTokenCount(usage.totalTokens),
    cachedPromptTokens: optionalTokenCount(usage.inputTokenDetails?.cacheReadTokens),
    cacheWriteTokens: optionalTokenCount(usage.inputTokenDetails?.cacheWriteTokens),
    reasoningTokens: optionalTokenCount(usage.outputTokenDetails?.reasoningTokens),
    costUSD: optionalCostUSD(accounting?.cost),
    upstreamInferenceCostUSD: optionalCostUSD(accounting?.costDetails?.upstreamInferenceCost),
  };
}

function openRouterUsageAccounting(providerMetadata: unknown): OpenRouterUsageAccountingDocument | undefined {
  if (!isRecord(providerMetadata)) return undefined;
  const openRouterMetadata = providerMetadata.openrouter;
  if (!isRecord(openRouterMetadata) || !isRecord(openRouterMetadata.usage)) return undefined;
  const usage = openRouterMetadata.usage;
  const costDetails = isRecord(usage.costDetails) ? usage.costDetails : undefined;
  return {
    cost: typeof usage.cost === 'number' ? usage.cost : undefined,
    costDetails: typeof costDetails?.upstreamInferenceCost === 'number'
      ? { upstreamInferenceCost: costDetails.upstreamInferenceCost }
      : undefined,
  };
}

function optionalCostUSD(value: number | undefined): number | undefined {
  if (value === undefined || !Number.isFinite(value) || value <= 0) return undefined;
  return value;
}

function serializableProviderMetadata(providerMetadata: unknown): ChatProviderMetadata | undefined {
  if (providerMetadata === undefined) return undefined;
  try {
    const serializedMetadata: unknown = JSON.parse(JSON.stringify(providerMetadata));
    if (isChatProviderMetadata(serializedMetadata)) return serializedMetadata;
  } catch {
    throw new LLMDError('provider_response_invalid', 502, false, 'provider metadata is not serializable');
  }
  throw new LLMDError('provider_response_invalid', 502, false, 'provider metadata is not serializable');
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

function isJSONValue(value: unknown): value is JSONValue {
  if (value === null || typeof value === 'string' || typeof value === 'boolean') return true;
  if (typeof value === 'number') return Number.isFinite(value);
  if (Array.isArray(value)) return value.every(isJSONValue);
  if (!isRecord(value)) return false;
  return Object.values(value).every(isJSONValue);
}

function isChatProviderMetadata(value: unknown): value is ChatProviderMetadata {
  if (value === null || typeof value === 'string' || typeof value === 'boolean') return true;
  if (typeof value === 'number') return Number.isFinite(value);
  if (Array.isArray(value)) return value.every(isChatProviderMetadata);
  if (!isRecord(value)) return false;
  return Object.values(value).every(isChatProviderMetadata);
}

function createValidatedJSONSchema(document: unknown) {
  if (!isJSONSchema(document)) {
    throw new LLMDError('request_invalid', 400, false, 'JSON schema must be an object');
  }
  const validationDocument = createClosedJSONSchema(document);
  const ajv = new Ajv({ allErrors: true, strict: false });
  let validator;
  try {
    validator = ajv.compile(validationDocument);
  } catch (errorValue) {
    throw new LLMDError('request_invalid', 400, false, errorValue instanceof Error ? errorValue.message : 'JSON schema is invalid');
  }
  return jsonSchema(document, {
    validate(value) {
      const normalizedValue = removeOptionalNullProperties(value, validationDocument);
      if (validator(normalizedValue)) return { success: true, value: normalizedValue };
      return {
        success: false,
        error: new JSONSchemaValidationError(validationIssuesFromAJV(validator.errors), ajv.errorsText(validator.errors)),
      };
    },
  });
}

function validateStructuredOutputRequest(request: StructuredResponseRequest): void {
  if (structuredOutputSchemaSchema.safeParse(request.structuredOutputSchema).success) return;
  throw new LLMDError('request_invalid', 400, false, 'structured output schema request is invalid');
}

function removeOptionalNullProperties(value: unknown, schema: unknown): unknown {
  if (Array.isArray(value)) return value.map((item, index) => removeOptionalNullProperties(item, arrayItemSchema(schema, index)));
  if (!isRecord(value)) return value;
  const properties = isRecord(schema) && isRecord(schema.properties) ? schema.properties : {};
  const requiredProperties = new Set(isRecord(schema) && Array.isArray(schema.required) ? schema.required : []);
  const normalizedValue: Record<string, unknown> = {};
  for (const [propertyName, propertyValue] of Object.entries(value)) {
    const propertySchema = properties[propertyName];
    if (propertySchema === undefined) {
      normalizedValue[propertyName] = cloneJSONValue(propertyValue);
      continue;
    }
    if (propertyValue === null && !requiredProperties.has(propertyName) && !schemaAllowsNull(propertySchema)) continue;
    normalizedValue[propertyName] = removeOptionalNullProperties(propertyValue, propertySchema);
  }
  return normalizedValue;
}

function arrayItemSchema(schema: unknown, index: number): unknown {
  if (!isRecord(schema)) return undefined;
  if (Array.isArray(schema.items)) return schema.items[index];
  return schema.items;
}

function schemaAllowsNull(schema: unknown): boolean {
  if (schema === true) return true;
  if (schema === false) return false;
  if (!isRecord(schema) || schema.type === undefined) return true;
  return schema.type === 'null' || (Array.isArray(schema.type) && schema.type.includes('null'));
}

function cloneJSONValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(cloneJSONValue);
  if (!isRecord(value)) return value;
  return Object.fromEntries(Object.entries(value).map(([key, nestedValue]) => [key, cloneJSONValue(nestedValue)]));
}

function createClosedJSONSchema(document: JSONSchema7): JSONSchema7 {
  const closedDocument = structuredClone(document);
  closeObjectSchemas(closedDocument);
  return closedDocument;
}

function closeObjectSchemas(value: unknown): void {
  if (Array.isArray(value)) {
    for (const item of value) closeObjectSchemas(item);
    return;
  }
  if (!isRecord(value)) return;
  const schemaType = value.type;
  const isObjectType = schemaType === 'object' || (Array.isArray(schemaType) && schemaType.includes('object'));
  if (isObjectType && value.additionalProperties === undefined) value.additionalProperties = false;
  for (const nestedValue of Object.values(value)) closeObjectSchemas(nestedValue);
}

function isJSONSchema(value: unknown): value is JSONSchema7 {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

function convertConversationMessages(request: StructuredResponseRequest): ModelMessage[] {
  return request.messages.filter(message => message.role !== 'system').map(message => {
    if (message.role === 'user') return { role: 'user', content: userContent(message) };
    const content = textContent(message);
    return { role: 'assistant', content };
  });
}

function systemMessages(request: ProviderRequest): SystemModelMessage[] | undefined {
  const contents: string[] = [];
  let cacheBreakpointIndex: number | undefined;
  let isBeforeFirstNonSystemMessage = true;
  for (const message of request.messages) {
    if (message.role !== 'system') {
      isBeforeFirstNonSystemMessage = false;
      continue;
    }
    const content = systemMessageContent(message);
    if (!content) continue;
    contents.push(content);
    if (isBeforeFirstNonSystemMessage) cacheBreakpointIndex = contents.length - 1;
  }
  if (contents.length === 0) return undefined;
  return contents.map((content, index) => (
    index === cacheBreakpointIndex
      ? { role: 'system' as const, content, providerOptions: providerCacheControlBreakpoint }
      : { role: 'system' as const, content }
  ));
}

function systemMessageContent(message: ProviderRequest['messages'][number]): string {
  if (!('parts' in message)) return message.content ?? '';
  const content = [message.content ?? ''];
  if (!Array.isArray(message.parts)) return message.content ?? '';
  for (const part of message.parts) {
    if (isRecord(part) && part.type === 'image') throw new Error(`${message.role} messages cannot contain image parts`);
    if (isRecord(part) && typeof part.text === 'string') content.push(part.text);
  }
  return content.filter(Boolean).join('\n');
}

type MessageWithParts = {
  content?: string | undefined;
  parts?: StructuredResponseRequest['messages'][number]['parts'];
};

function userContent(message: MessageWithParts) {
  if (!message.parts || message.parts.length === 0) return message.content ?? '';
  const content = [];
  if (message.content) content.push({ type: 'text' as const, text: message.content });
  for (const part of message.parts) {
    if (part.type === 'text') content.push({ type: 'text' as const, text: part.text });
    if (part.type === 'image') content.push({ type: 'image' as const, image: part.dataBase64, mediaType: part.mimeType });
  }
  return content;
}

function textContent(message: StructuredResponseRequest['messages'][number]): string {
  const content = [message.content ?? ''];
  for (const part of message.parts ?? []) {
    if (part.type === 'image') throw new Error(`${message.role} messages cannot contain image parts`);
    content.push(part.text);
  }
  return content.filter(Boolean).join('\n');
}

function normalizeTokenCount(value: number | undefined): number {
  return Math.max(0, Math.trunc(value ?? 0));
}

function optionalTokenCount(value: number | undefined): number | undefined {
  if (value === undefined) return undefined;
  return normalizeTokenCount(value);
}
