import { describe, expect, test } from 'bun:test';
import { z } from 'zod';

import {
  ArtifactKind,
  ArtifactToolName,
  BrowserToolName,
  CalendarToolName,
  ChannelToolName,
  DocumentToolName,
  ImageToolName,
  MessageAuthor,
  MessageDeliveryStatus,
  MessageSearchScope,
  MessageTargetType,
  MessageToolName,
  SiteLifecycleStatus,
  SiteServeMode,
  SiteToolName,
  WebToolName,
  WorkspaceTaskInitialStatus,
  WorkspaceTaskSize,
  artifactReviewInputSchema,
  artifactReviewResultSchema,
  browserClickInputSchema,
  browserClickResultSchema,
  browserOpenInputSchema,
  browserOpenResultSchema,
  browserScreenshotInputSchema,
  browserScreenshotResultSchema,
  browserSnapshotInputSchema,
  browserSnapshotResultSchema,
  buildCapabilityToolCatalog,
  calendarAddInputSchema,
  calendarDeleteInputSchema,
  calendarDeleteInputIntentSchema,
  calendarListInputSchema,
  calendarUpdateInputSchema,
  calendarUpdateInputIntentSchema,
  channelUpdateInputSchema,
  channelUpdateResultSchema,
  documentReadInputSchema,
  documentReadResultSchema,
  imageReadInputSchema,
  imageReadResultSchema,
  messageContextInputSchema,
  messageContextResultSchema,
  messageDeleteInputSchema,
  messageDeleteResultSchema,
  messageSearchInputSchema,
  messageSearchResultSchema,
  messageSendInputSchema,
  messageSendResultSchema,
  messageUpdateInputSchema,
  messageUpdateResultSchema,
  siteListInputSchema,
  siteListResultSchema,
  siteServeInputSchema,
  siteServeInputIntentSchema,
  siteServeResultSchema,
  siteUnserveInputSchema,
  siteUnserveResultSchema,
  taskAddInputSchema,
  taskDeleteInputSchema,
  taskDeleteInputIntentSchema,
  taskListInputSchema,
  taskUpdateInputSchema,
  taskUpdateInputIntentSchema,
  webSearchInputSchema,
  webSearchResultSchema,
} from '../src/capability_tools.ts';
import {
  CapabilityModelVisibility,
  CapabilitySideEffect,
  ResourceEffectIdentity,
} from '../src/capability.ts';
import { protocolVersion } from '../src/registry.ts';

describe('canonical capability tools', () => {
  test('defines the complete canonical tool family', () => {
    const catalog = buildCapabilityToolCatalog(protocolVersion);

    expect(catalog.protocolVersion).toBe(protocolVersion);
    expect(catalog.tools.map(tool => tool.name)).toEqual([
      'task_add',
      'task_list',
      'task_update',
      'task_delete',
      'person_list',
      CalendarToolName.Add,
      CalendarToolName.List,
      CalendarToolName.Update,
      CalendarToolName.Delete,
      MessageToolName.Context,
      MessageToolName.Search,
      MessageToolName.Send,
      MessageToolName.Update,
      MessageToolName.Delete,
      ChannelToolName.Update,
      WebToolName.Search,
      SiteToolName.Serve,
      SiteToolName.List,
      SiteToolName.Unserve,
      DocumentToolName.Read,
      ImageToolName.Read,
      BrowserToolName.Open,
      BrowserToolName.Snapshot,
      BrowserToolName.Screenshot,
      BrowserToolName.Click,
      ArtifactToolName.Review,
    ]);
    expect(catalog.tools.every(tool => tool.inputSchemaStrict && tool.outputSchemaStrict)).toBe(true);
    expect(catalog.tools.every(tool => (
      JSON.stringify(tool.outputSchema) === JSON.stringify(tool.resultContract?.schema)
    ))).toBe(true);
    expect(new Set(catalog.tools.map(tool => tool.name)).size).toBe(catalog.tools.length);
  });

  test('publishes explicit intent schemas for model-visible state changes', () => {
    const catalog = buildCapabilityToolCatalog(protocolVersion);
    const stateChangingTools = catalog.tools.filter(tool =>
      tool.modelVisibility === CapabilityModelVisibility.Visible
      && tool.sideEffectClass !== CapabilitySideEffect.Read
      && tool.sideEffectClass !== CapabilitySideEffect.Computation
    );

    expect(stateChangingTools.map(tool => tool.name)).toEqual([
      'task_add',
      'task_update',
      'task_delete',
      CalendarToolName.Add,
      CalendarToolName.Update,
      CalendarToolName.Delete,
      MessageToolName.Send,
      MessageToolName.Update,
      MessageToolName.Delete,
      ChannelToolName.Update,
      SiteToolName.Serve,
      SiteToolName.Unserve,
      BrowserToolName.Open,
      BrowserToolName.Click,
    ]);

    for (const tool of stateChangingTools) {
      if (tool.inputIntentSchema === undefined) {
        throw new Error(`${tool.name} is missing inputIntentSchema`);
      }
      const intentSchema = z.fromJSONSchema(tool.inputIntentSchema);
      expect(intentSchema.safeParse({}).success).toBe(true);
      expect(intentSchema.safeParse({ unexpected: true }).success).toBe(false);
    }

    expect(siteServeInputIntentSchema.safeParse({ title: 'Team Dashboard' }).success).toBe(true);
    expect(siteServeInputIntentSchema.safeParse({ mode: 'publish', unexpected: true }).success).toBe(false);
  });

  test('defines exact web search inputs and normalized results', () => {
    expect(webSearchInputSchema.safeParse({ query: 'internkim', limit: 3 }).success).toBe(true);
    expect(webSearchInputSchema.safeParse({ query: 'internkim', allowedDomains: ['internkim.example'] }).success).toBe(true);
    expect(webSearchInputSchema.safeParse({ query: 'internkim', limit: 0 }).success).toBe(false);
    expect(webSearchInputSchema.safeParse({ query: '   ' }).success).toBe(false);
    expect(webSearchInputSchema.safeParse({ query: 'internkim', unknown: true }).success).toBe(false);
    expect(webSearchInputSchema.safeParse({}).success).toBe(false);

    const result = {
      provider: 'openrouter',
      remoteLLMInvolved: true,
      compatibility: 'openrouter_server_tool_auto',
      query: 'internkim',
      answer: 'result',
      results: [{
        title: 'InternKim',
        url: 'https://internkim.example',
        snippet: 'An agent platform',
        source: 'internkim.example',
      }],
    };
    expect(webSearchResultSchema.safeParse(result).success).toBe(true);
    expect(webSearchResultSchema.safeParse({ ...result, extra: true }).success).toBe(false);
    expect(webSearchResultSchema.safeParse({ ...result, results: [{ title: 'InternKim' }] }).success).toBe(false);

    const catalog = buildCapabilityToolCatalog(protocolVersion);
    const descriptor = catalog.tools.find(tool => tool.name === WebToolName.Search);
    expect(descriptor?.resultContract?.effects).toEqual([]);
    expect(descriptor?.requiresApproval).toBeUndefined();
    expect(descriptor?.sideEffectClass).toBe(CapabilitySideEffect.Read);
  });

  test('defines exact browser inputs and successful results', () => {
    expect(browserOpenInputSchema.safeParse({ url: 'https://preview.example/site-1' }).success).toBe(true);
    expect(browserSnapshotInputSchema.safeParse({}).success).toBe(true);
    expect(browserScreenshotInputSchema.safeParse({ ttlSeconds: 300 }).success).toBe(true);
    expect(browserClickInputSchema.safeParse({ ref: '@e1' }).success).toBe(true);

    expect(browserOpenInputSchema.safeParse({ startURL: 'https://preview.example/site-1' }).success).toBe(false);
    expect(browserSnapshotInputSchema.safeParse({ interactive: true }).success).toBe(false);
    expect(browserScreenshotInputSchema.safeParse({ ttlSeconds: -1 }).success).toBe(false);
    expect(browserClickInputSchema.safeParse({}).success).toBe(false);

    expect(browserOpenResultSchema.safeParse({
      url: 'https://preview.example/site-1',
      requestedURL: 'https://preview.example/site-1',
      title: 'Quarterly support',
      snapshotText: '- button "Open report" [ref=e1]',
      interactiveRefs: ['@e1'],
      capturedAt: '2026-07-19T00:00:00Z',
    }).success).toBe(true);
    expect(browserSnapshotResultSchema.safeParse({
      url: 'https://preview.example/site-1',
      title: 'Quarterly support',
      snapshotText: '- button "Open report" [ref=e1]',
      interactiveRefs: ['@e1'],
      hasMore: false,
      capturedAt: '2026-07-19T00:00:00Z',
    }).success).toBe(true);
    expect(browserScreenshotResultSchema.safeParse({
      fileID: 'file-1',
      filename: 'site.png',
      sizeBytes: 1024,
      contentType: 'image/png',
      devicePath: '/tmp/internkim-companion-files/site.png',
      expiresAt: '2026-07-19T00:05:00Z',
      capturedAt: '2026-07-19T00:00:00Z',
    }).success).toBe(true);
    expect(browserClickResultSchema.safeParse({
      ok: true,
      action: 'click',
      target: '@e1',
      capturedAt: '2026-07-19T00:00:00Z',
    }).success).toBe(true);
    expect(browserClickResultSchema.safeParse({
      ok: true,
      action: 'fill',
      capturedAt: '2026-07-19T00:00:00Z',
    }).success).toBe(false);
  });

  test('defines exact artifact review evidence and result contracts', () => {
    const reviewInput = {
      artifactKind: ArtifactKind.Site,
      intent: 'Check the customer support landing page',
      rubric: 'Verify hierarchy, text fit, and primary interaction',
      evidence: [{
        role: 'desktopScreenshot',
        path: '/tmp/internkim-companion-files/site.png',
        mimeType: 'image/png',
        label: 'Desktop preview',
      }],
    };
    const reviewResult = {
      passed: false,
      issues: [{
        severity: 'warning',
        category: 'visualHierarchy',
        target: 'Primary action',
        message: 'The action is hard to distinguish.',
        suggestedFix: 'Increase contrast.',
      }],
      acceptedWarnings: [],
      summary: 'One visual hierarchy issue remains.',
    };

    expect(artifactReviewInputSchema.safeParse(reviewInput).success).toBe(true);
    expect(artifactReviewResultSchema.safeParse(reviewResult).success).toBe(true);
    expect(artifactReviewInputSchema.safeParse({ ...reviewInput, evidence: [] }).success).toBe(false);
    expect(artifactReviewInputSchema.safeParse({
      ...reviewInput,
      evidence: [{ ...reviewInput.evidence[0], mimeType: 'text/html' }],
    }).success).toBe(false);
    expect(artifactReviewResultSchema.safeParse({
      ...reviewResult,
      issues: [{ ...reviewResult.issues[0], category: 'performance' }],
    }).success).toBe(false);

    const catalog = buildCapabilityToolCatalog(protocolVersion);
    for (const toolName of [
      BrowserToolName.Open,
      BrowserToolName.Snapshot,
      BrowserToolName.Screenshot,
      BrowserToolName.Click,
      ArtifactToolName.Review,
    ]) {
      expect(catalog.tools.find(tool => tool.name === toolName)?.resultContract?.effects).toEqual([]);
    }
    expect(catalog.tools.find(tool => tool.name === ArtifactToolName.Review)?.resultContract?.evidenceCondition).toEqual({
      resultField: 'passed',
      equals: true,
    });
  });

  test('keeps task mutation contracts exact', () => {
    const catalog = buildCapabilityToolCatalog(protocolVersion);
    const addTool = catalog.tools.find(tool => tool.name === 'task_add');
    const updateTool = catalog.tools.find(tool => tool.name === 'task_update');
    const deleteTool = catalog.tools.find(tool => tool.name === 'task_delete');

    expect(addTool?.resultContract?.effects).toEqual([
      { objectType: 'task', effect: 'created', resultField: 'taskID', effectIdentity: ResourceEffectIdentity.ID },
    ]);
    expect(updateTool?.resultContract?.effects).toEqual([
      { objectType: 'task', effect: 'updated', resultField: 'taskID', effectIdentity: ResourceEffectIdentity.ID },
    ]);
    expect(deleteTool?.resultContract?.effects).toEqual([
      { objectType: 'task', effect: 'deleted', resultField: 'taskID', effectIdentity: ResourceEffectIdentity.ID },
    ]);
    expect(deleteTool?.requiresApproval).toBe(true);
  });

  test('keeps runtime task identities out of user intent', () => {
    expect(taskUpdateInputIntentSchema.safeParse({ title: 'quarterly settlement review' }).success).toBe(true);
    expect(taskUpdateInputIntentSchema.safeParse({ taskHint: 'task-1' }).success).toBe(false);
    expect(taskDeleteInputIntentSchema.safeParse({}).success).toBe(true);
    expect(taskDeleteInputIntentSchema.safeParse({ taskHint: 'task-1' }).success).toBe(false);
  });

  test('keeps calendar metadata and mutation contracts explicit', () => {
    const catalog = buildCapabilityToolCatalog(protocolVersion);
    const addTool = catalog.tools.find(tool => tool.name === CalendarToolName.Add);
    const listTool = catalog.tools.find(tool => tool.name === CalendarToolName.List);
    const updateTool = catalog.tools.find(tool => tool.name === CalendarToolName.Update);
    const deleteTool = catalog.tools.find(tool => tool.name === CalendarToolName.Delete);

    expect(addTool).toMatchObject({
      namespace: 'calendar',
      privacyClass: 'workspace_calendar',
      policyResource: 'tool:calendar_add',
    });
    expect(addTool?.resultContract?.effects).toEqual([
      { objectType: 'calendar', effect: 'created', resultField: 'eventID', effectIdentity: ResourceEffectIdentity.ID },
    ]);
    expect(listTool?.resultContract?.effects).toEqual([]);
    expect(updateTool?.resultContract?.effects).toEqual([
      { objectType: 'calendar', effect: 'updated', resultField: 'eventID', effectIdentity: ResourceEffectIdentity.ID },
    ]);
    expect(deleteTool?.resultContract?.effects).toEqual([
      { objectType: 'calendar', effect: 'deleted', resultField: 'eventID', effectIdentity: ResourceEffectIdentity.ID },
    ]);
    expect(deleteTool?.requiresApproval).toBe(true);
  });

  test('keeps runtime calendar identities out of user intent', () => {
    expect(calendarUpdateInputIntentSchema.safeParse({ startISO: '2026-07-24T15:00:00+09:00' }).success).toBe(true);
    expect(calendarUpdateInputIntentSchema.safeParse({ eventHint: 'event-1' }).success).toBe(false);
    expect(calendarDeleteInputIntentSchema.safeParse({}).success).toBe(true);
    expect(calendarDeleteInputIntentSchema.safeParse({ eventHint: 'event-1' }).success).toBe(false);
  });

  test('validates shallow message and channel inputs', () => {
    expect(messageContextInputSchema.safeParse({}).success).toBe(true);
    expect(messageSearchInputSchema.safeParse({
      scope: MessageSearchScope.CurrentChannel,
      authoredBy: MessageAuthor.Assistant,
      queries: ['quarterly settlement'],
      limit: 20,
    }).success).toBe(true);
    expect(messageSendInputSchema.safeParse({
      targetType: MessageTargetType.DirectMessage,
      message: 'please review the quarterly settlement material.',
      personHint: '@support-lead',
    }).success).toBe(true);
    expect(messageUpdateInputSchema.safeParse({
      messageID: 'message-1',
      oldText: 'quarterly settlement material',
      newText: 'quarterly settlement notice',
    }).success).toBe(true);
    expect(messageDeleteInputSchema.safeParse({
      messageIDs: ['message-1', 'message-2'],
    }).success).toBe(true);
    expect(channelUpdateInputSchema.safeParse({
      channelID: 'channel-1',
      header: 'customer support quarterly settlement share',
      inviteeHints: ['@support-lead'],
    }).success).toBe(true);

    expect(messageContextInputSchema.safeParse({ scope: 'currentChannel' }).success).toBe(false);
    expect(messageSearchInputSchema.safeParse({ query: 'quarterly settlement' }).success).toBe(false);
    expect(messageSearchInputSchema.safeParse({ limit: 26 }).success).toBe(false);
    expect(messageSendInputSchema.safeParse({
      targetType: MessageTargetType.CurrentChannel,
      message: '   ',
    }).success).toBe(false);
    expect(messageSendInputSchema.safeParse({
      targetType: MessageTargetType.CurrentChannel,
      message: 'notice',
      deliveryTarget: { type: 'currentChannel' },
    }).success).toBe(false);
    expect(messageUpdateInputSchema.safeParse({ messageID: 'message-1' }).success).toBe(false);
    expect(messageDeleteInputSchema.safeParse({ messageIDs: [] }).success).toBe(false);
    expect(messageDeleteInputSchema.safeParse({ messageIDs: ['message-1', 'message-1'] }).success).toBe(false);
    expect(channelUpdateInputSchema.safeParse({ channelID: 'channel-1' }).success).toBe(false);
    expect(channelUpdateInputSchema.safeParse({ header: 'new header' }).success).toBe(false);
  });

  test('requires canonical message and channel result identities', () => {
    const contextResult = {
      platform: 'mattermost',
      conversationID: 'conversation-1',
      conversationType: 'direct',
      channelID: 'channel-1',
      channelName: 'support',
      replyTargetID: 'message-1',
      rootMessageID: '',
      currentMessageID: 'message-1',
      requesterPersonID: 'person-1',
      requesterPlatformUserID: 'user-1',
      botUserID: 'bot-1',
      botUsername: 'internkim',
    };
    const searchResult = {
      scope: MessageSearchScope.CurrentChannel,
      queries: ['quarterly settlement'],
      authoredBy: MessageAuthor.Assistant,
      messageIDs: ['message-1'],
      candidates: [{
        messageID: 'message-1',
        channelID: 'channel-1',
        userID: 'bot-1',
        authoredBy: MessageAuthor.Assistant,
        createdAt: 1784422800000,
        preview: 'quarterly settlement notice',
        deletable: true,
      }],
      hasMore: false,
    };
    const sendResult = {
      messageIDs: ['message-2'],
      deliveryStatus: MessageDeliveryStatus.Sent,
    };
    const updateResult = {
      messageID: 'message-2',
      deliveryStatus: MessageDeliveryStatus.Updated,
      messageUpdated: true,
      isPinned: false,
    };
    const deleteResult = {
      messageIDs: ['message-2'],
      deliveryStatus: MessageDeliveryStatus.Deleted,
    };
    const channelResult = {
      channelID: 'channel-1',
      updated: true,
      invitedUserIDs: ['user-2'],
    };

    expect(messageContextResultSchema.safeParse(contextResult).success).toBe(true);
    expect(messageSearchResultSchema.safeParse(searchResult).success).toBe(true);
    expect(messageSendResultSchema.safeParse(sendResult).success).toBe(true);
    expect(messageUpdateResultSchema.safeParse(updateResult).success).toBe(true);
    expect(messageDeleteResultSchema.safeParse(deleteResult).success).toBe(true);
    expect(channelUpdateResultSchema.safeParse(channelResult).success).toBe(true);

    expect(messageContextResultSchema.safeParse({ ...contextResult, extra: true }).success).toBe(false);
    expect(messageSearchResultSchema.safeParse({ ...searchResult, candidates: [{ messageID: 'message-1' }] }).success).toBe(false);
    expect(messageSendResultSchema.safeParse({ ...sendResult, messageIDs: [] }).success).toBe(false);
    expect(messageUpdateResultSchema.safeParse({ ...updateResult, messageID: '' }).success).toBe(false);
    expect(messageDeleteResultSchema.safeParse({ ...deleteResult, messageIDs: ['message-2', 'message-2'] }).success).toBe(false);
    expect(channelUpdateResultSchema.safeParse({ ...channelResult, channelID: ' channel-1 ' }).success).toBe(false);
  });

  test('publishes exact message and channel effects and approvals', () => {
    const catalog = buildCapabilityToolCatalog(protocolVersion);
    const contextTool = catalog.tools.find(tool => tool.name === MessageToolName.Context);
    const searchTool = catalog.tools.find(tool => tool.name === MessageToolName.Search);
    const sendTool = catalog.tools.find(tool => tool.name === MessageToolName.Send);
    const updateTool = catalog.tools.find(tool => tool.name === MessageToolName.Update);
    const deleteTool = catalog.tools.find(tool => tool.name === MessageToolName.Delete);
    const channelTool = catalog.tools.find(tool => tool.name === ChannelToolName.Update);

    expect(contextTool?.resultContract?.effects).toEqual([]);
    expect(searchTool?.resultContract?.effects).toEqual([]);
    expect(sendTool?.resultContract?.effects).toEqual([
      { objectType: 'message', effect: 'sent', resultField: 'messageIDs', effectIdentity: ResourceEffectIdentity.ID },
    ]);
    expect(updateTool?.resultContract?.effects).toEqual([
      { objectType: 'message', effect: 'updated', resultField: 'messageID', effectIdentity: ResourceEffectIdentity.ID },
    ]);
    expect(deleteTool?.resultContract?.effects).toEqual([
      { objectType: 'message', effect: 'deleted', resultField: 'messageIDs', effectIdentity: ResourceEffectIdentity.ID },
    ]);
    expect(channelTool?.resultContract?.effects).toEqual([
      { objectType: 'channel', effect: 'updated', resultField: 'channelID', effectIdentity: ResourceEffectIdentity.ID },
    ]);
    expect(contextTool?.requiresApproval).toBeUndefined();
    expect(searchTool?.requiresApproval).toBeUndefined();
    expect(sendTool?.requiresApproval).toBe(true);
    expect(updateTool?.requiresApproval).toBe(false);
    expect(deleteTool?.requiresApproval).toBe(true);
    expect(channelTool?.requiresApproval).toBe(true);
    expect(sendTool?.idempotency).toEqual({ supported: true, required: false, scope: 'operation' });
    expect(updateTool?.idempotency).toEqual({ supported: false, required: false, scope: 'operation' });
    expect(sendTool?.completionEvidence).toEqual({
      mode: 'success',
      action: 'send_message',
      targetKind: 'message',
    });
  });

  test('validates task inputs without operation aliases', () => {
    expect(taskAddInputSchema.parse({
      title: 'customer support quarterly settlement gap check',
      size: WorkspaceTaskSize.Small,
      status: WorkspaceTaskInitialStatus.Planned,
      endDate: '2026-07-24',
    })).toEqual({
      title: 'customer support quarterly settlement gap check',
      size: WorkspaceTaskSize.Small,
      status: WorkspaceTaskInitialStatus.Planned,
      endDate: '2026-07-24',
    });
    expect(taskListInputSchema.safeParse({ query: 'settlement', scope: 'self' }).success).toBe(true);
    expect(taskUpdateInputSchema.safeParse({ taskHint: 'task-1', title: 'updated title' }).success).toBe(true);
    expect(taskDeleteInputSchema.safeParse({ taskHint: 'task-1' }).success).toBe(true);

    expect(taskAddInputSchema.safeParse({ content: 'invalid alias' }).success).toBe(false);
    expect(taskUpdateInputSchema.safeParse({ taskHint: 'task-1' }).success).toBe(false);
    expect(taskUpdateInputSchema.safeParse({ query: 'settlement', content: 'update' }).success).toBe(false);
    expect(taskDeleteInputSchema.safeParse({ taskHint: 'task-1', query: 'settlement' }).success).toBe(false);
  });

  test('validates calendar inputs with exact mutation identities', () => {
    expect(calendarAddInputSchema.safeParse({
      title: 'customer support weekly check',
      startISO: '2026-07-24T14:00:00+09:00',
      endISO: '2026-07-24T15:00:00+09:00',
      people: ['support@example.com'],
    }).success).toBe(true);
    expect(calendarUpdateInputSchema.safeParse({
      eventHint: 'event-1',
      startISO: '2026-07-24T15:00:00+09:00',
    }).success).toBe(true);
    expect(calendarDeleteInputSchema.safeParse({ eventHint: 'event-1' }).success).toBe(true);
    expect(calendarListInputSchema.safeParse({ limit: 2 }).success).toBe(true);

    expect(calendarUpdateInputSchema.safeParse({ eventHint: 'event-1' }).success).toBe(false);
    expect(calendarAddInputSchema.safeParse({
      title: 'customer support weekly check',
      startISO: '2026-07-24T14:00:00+09:00',
      endISO: '2026-07-24T15:00:00+09:00',
      reminderLeadHours: 4,
    }).success).toBe(false);
    expect(calendarUpdateInputSchema.safeParse({ query: 'weekly check', title: 'change' }).success).toBe(false);
    expect(calendarDeleteInputSchema.safeParse({ query: 'weekly check' }).success).toBe(false);
    expect(calendarListInputSchema.safeParse({ limit: 0 }).success).toBe(false);
    expect(calendarListInputSchema.safeParse({ limit: 1.5 }).success).toBe(false);
  });

  test('publishes provider-portable minimum mutation property counts', () => {
    const catalog = buildCapabilityToolCatalog(protocolVersion);
    const taskUpdateTool = catalog.tools.find(tool => tool.name === 'task_update');
    const calendarAddTool = catalog.tools.find(tool => tool.name === CalendarToolName.Add);
    const calendarUpdateTool = catalog.tools.find(tool => tool.name === CalendarToolName.Update);

    expect(taskUpdateTool?.inputSchema).toMatchObject({ minProperties: 2 });
    expect(calendarAddTool?.inputSchema).toMatchObject({
      properties: {
        reminderLeadHours: {
          type: 'number',
        },
      },
    });
    expect(JSON.stringify(calendarAddTool?.inputSchema)).not.toContain('"enum":[1');
    expect(calendarUpdateTool?.inputSchema).toMatchObject({ minProperties: 2 });
  });

  test('keeps the hosting boundary to serve, list, and unserve', () => {
    const catalog = buildCapabilityToolCatalog(protocolVersion);
    const siteTools = catalog.tools.filter(tool => tool.namespace === 'site');

    expect(siteTools.map(tool => tool.name)).toEqual([
      SiteToolName.Serve,
      SiteToolName.List,
      SiteToolName.Unserve,
    ]);
    expect(siteTools.every(tool => tool.resultContract !== undefined)).toBe(true);
    expect(siteTools.every(tool => tool.resultContract?.schema.additionalProperties === false)).toBe(true);
  });

  test('requires exact site identities for hosting mutations', () => {
    expect(siteServeInputSchema.safeParse({
      title: 'customer support quarterly settlement',
      sourceWorkspacePath: '~/sites/customer-support-quarterly',
      mode: SiteServeMode.Publish,
    }).success).toBe(true);
    expect(siteServeInputSchema.safeParse({
      title: 'Customer Support Quarterly',
      sourceWorkspacePath: '~/sites/customer-support-quarterly',
      mode: SiteServeMode.Preview,
      siteReference: 'customer-support-quarterly',
    }).success).toBe(true);
    expect(siteListInputSchema.safeParse({}).success).toBe(true);
    expect(siteListInputSchema.safeParse({ siteReference: 'site-1' }).success).toBe(true);
    expect(siteUnserveInputSchema.safeParse({ siteReference: 'site-1', reason: 'The campaign ended.' }).success).toBe(true);

    expect(siteServeInputSchema.safeParse({ title: '', sourceWorkspacePath: '~/sites/a', mode: 'publish' }).success).toBe(false);
    expect(siteServeInputSchema.safeParse({ title: 'A', mode: 'publish' }).success).toBe(false);
    expect(siteServeInputSchema.safeParse({ title: 'A', sourceWorkspacePath: '~/sites/a' }).success).toBe(false);
    expect(siteServeInputSchema.safeParse({ title: 'A', sourceWorkspacePath: '~/sites/a', mode: 'deploy' }).success).toBe(false);
    expect(siteServeInputSchema.safeParse({ title: 'A', sourceWorkspacePath: '~/sites/a', mode: 'publish', slug: 'a' }).success).toBe(false);
    expect(siteListInputSchema.safeParse({ siteReference: ' site-1 ' }).success).toBe(false);
    expect(siteUnserveInputSchema.safeParse({}).success).toBe(false);
    expect(siteUnserveInputSchema.safeParse({ siteID: 'site-1' }).success).toBe(false);
  });

  test('requires operation-specific site result shapes', () => {
    const previewServeResult = {
      siteID: 'site-1',
      slug: 'customer-support-quarterly',
      mode: SiteServeMode.Preview,
      previewURL: 'https://customer-support-quarterly.example/__preview/preview-1',
      sourceSHA256: '254cc09182b94752e96474af9ba307f74dcfff4e8dfa5b0c4a76f97e634c1c28',
    };
    const publishServeResult = {
      siteID: 'site-1',
      slug: 'customer-support-quarterly',
      mode: SiteServeMode.Publish,
      publishedURL: 'https://customer-support-quarterly.example',
      sourceSHA256: '254cc09182b94752e96474af9ba307f74dcfff4e8dfa5b0c4a76f97e634c1c28',
    };
    const listResult = {
      sites: [{
        siteID: 'site-1',
        slug: 'customer-support-quarterly',
        title: 'customer support quarterly settlement',
        status: SiteLifecycleStatus.Published,
        publishedURL: 'https://customer-support-quarterly.example',
        updatedAt: '2026-07-19T12:00:00Z',
      }],
    };
    const unserveResult = { siteID: 'site-1', slug: 'customer-support-quarterly', unserved: true };

    expect(siteServeResultSchema.safeParse(previewServeResult).success).toBe(true);
    expect(siteServeResultSchema.safeParse(publishServeResult).success).toBe(true);
    expect(siteListResultSchema.safeParse(listResult).success).toBe(true);
    expect(siteListResultSchema.safeParse({ sites: [] }).success).toBe(true);
    expect(siteUnserveResultSchema.safeParse(unserveResult).success).toBe(true);

    expect(siteServeResultSchema.safeParse({ ...publishServeResult, sourceSHA256: undefined }).success).toBe(false);
    expect(siteServeResultSchema.safeParse({ ...publishServeResult, mode: undefined }).success).toBe(false);
    expect(siteServeResultSchema.safeParse({ ...publishServeResult, slug: 'Invalid Slug' }).success).toBe(false);
    expect(siteListResultSchema.safeParse({ sites: [{ siteID: 'site-1' }] }).success).toBe(false);
    expect(siteUnserveResultSchema.safeParse({ siteID: 'site-1', slug: 'a', unserved: false }).success).toBe(false);
    expect(siteServeResultSchema.safeParse({ ...publishServeResult, extra: true }).success).toBe(false);
  });

  test('publishes mode-conditional serve effects and unserve completion evidence', () => {
    const catalog = buildCapabilityToolCatalog(protocolVersion);
    const serveTool = catalog.tools.find(tool => tool.name === SiteToolName.Serve);
    const listTool = catalog.tools.find(tool => tool.name === SiteToolName.List);
    const unserveTool = catalog.tools.find(tool => tool.name === SiteToolName.Unserve);

    expect(serveTool?.resultContract?.effects).toEqual([
      {
        objectType: 'website',
        effect: 'previewed',
        resultField: 'previewURL',
        effectIdentity: ResourceEffectIdentity.URL,
        when: { resultField: 'mode', equals: 'preview' },
      },
      {
        objectType: 'website',
        effect: 'published',
        resultField: 'publishedURL',
        effectIdentity: ResourceEffectIdentity.URL,
        when: { resultField: 'mode', equals: 'publish' },
      },
    ]);
    expect(serveTool?.requiresApproval).toBeUndefined();
    expect(listTool?.resultContract?.effects).toEqual([]);
    expect(unserveTool?.resultContract?.effects).toEqual([
      { objectType: 'website', effect: 'deleted', resultField: 'siteID', effectIdentity: ResourceEffectIdentity.ID },
    ]);
    expect(unserveTool?.requiresApproval).toBe(true);
    expect(unserveTool?.completionEvidence).toEqual({
      mode: 'success',
      action: 'delete_site',
      targetKind: 'site',
    });
  });

  test('validates document and image read inputs without material aliases', () => {
    expect(documentReadInputSchema.safeParse({
      path: '/workspace/shared/report.pdf',
      maxPages: 10,
      maxOutputBytes: 200000,
    }).success).toBe(true);
    expect(imageReadInputSchema.safeParse({ path: '/workspace/shared/logo.png' }).success).toBe(true);

    expect(documentReadInputSchema.safeParse({ materialID: 'material-1' }).success).toBe(false);
    expect(documentReadInputSchema.safeParse({ path: '/workspace/shared/report.pdf', ocrMode: 'always' }).success).toBe(false);
    expect(documentReadInputSchema.safeParse({ path: '/workspace/shared/report.pdf', maxPages: 0 }).success).toBe(false);
    expect(documentReadInputSchema.safeParse({ path: '/workspace/shared/report.pdf', maxPages: 501 }).success).toBe(false);
    expect(documentReadInputSchema.safeParse({ path: '/workspace/shared/report.pdf', maxOutputBytes: 0 }).success).toBe(false);
    expect(documentReadInputSchema.safeParse({ path: '/workspace/shared/report.pdf', maxOutputBytes: 1 }).success).toBe(false);
    expect(imageReadInputSchema.safeParse({ materialID: 'material-1' }).success).toBe(false);
    expect(imageReadInputSchema.safeParse({ path: '/workspace/shared/logo.png', materialID: 'material-1' }).success).toBe(false);
  });

  test('requires exact document and image read result contracts', () => {
    const documentResult = {
      status: 'ok',
      path: '/workspace/shared/report.pdf',
      format: 'markdown',
      content: '# Report',
      warnings: [],
      truncated: false,
      backend: 'anydoc',
      model: '',
    };
    const imageResult = {
      status: 'ok',
      path: '/workspace/shared/logo.png',
      attachments: [{
        devicePath: '/workspace/shared/logo.png',
        filename: 'logo.png',
        contentType: 'image/png',
        sizeBytes: 3,
        contentBase64: 'YWJj',
      }],
    };

    expect(documentReadResultSchema.safeParse(documentResult).success).toBe(true);
    expect(imageReadResultSchema.safeParse(imageResult).success).toBe(true);
    expect(documentReadResultSchema.safeParse({ ...documentResult, warnings: undefined }).success).toBe(false);
    expect(documentReadResultSchema.safeParse({ ...documentResult, format: 'text' }).success).toBe(false);
    expect(documentReadResultSchema.safeParse({ ...documentResult, extra: true }).success).toBe(false);
    expect(imageReadResultSchema.safeParse({ ...imageResult, attachments: [{ ...imageResult.attachments[0], sizeBytes: -1 }] }).success).toBe(false);
    expect(imageReadResultSchema.safeParse({ ...imageResult, attachments: [{ ...imageResult.attachments[0], devicePath: undefined }] }).success).toBe(false);
    expect(imageReadResultSchema.safeParse({ ...imageResult, extra: true }).success).toBe(false);
  });

  test('publishes mandatory read result contracts without effects', () => {
    const catalog = buildCapabilityToolCatalog(protocolVersion);
    const documentTool = catalog.tools.find(tool => tool.name === DocumentToolName.Read);
    const imageTool = catalog.tools.find(tool => tool.name === ImageToolName.Read);

    expect(documentTool?.resultContract?.effects).toEqual([]);
    expect(imageTool?.resultContract?.effects).toEqual([]);
    expect(documentTool?.resultContract?.schema.required).toEqual([
      'status', 'path', 'format', 'content', 'warnings', 'truncated',
    ]);
    expect(imageTool?.resultContract?.schema.required).toEqual(['status', 'path', 'attachments']);
    expect(documentTool?.inputSchema.properties).not.toHaveProperty('materialID');
    expect(imageTool?.inputSchema.properties).not.toHaveProperty('materialID');
  });
});
