import { z } from 'zod';

import {
  CapabilityAvailabilityState,
  CapabilityEstimatedLatency,
  CapabilityModelVisibility,
  CapabilitySideEffect,
  ResourceEffectIdentity,
  capabilityIdempotencySchema,
  capabilityDescriptorSchema,
  resourceEffectContractSchema,
} from './capability.ts';
import { jsonValueSchema } from './common.ts';

const dateDescription = 'Date in YYYY-MM-DD format.';
// A task and a calendar event are one row, so both are placed in time the same way.
const momentDescription = 'ISO 8601 with timezone for a moment, or YYYY-MM-DD for a whole day.';
const resourceIDSchema = z.string()
  .min(1)
  .regex(/^\S(?:.*\S)?$/, 'Resource identity must not have leading or trailing whitespace.');
const sha256Schema = z.string().regex(/^[a-f0-9]{64}$/);

export enum WorkspaceTaskSize {
  ExtraSmall = 'XS',
  Small = 'S',
  Medium = 'M',
  Large = 'L',
  ExtraLarge = 'XL',
  ExtraExtraLarge = 'XXL',
}

export enum WorkspaceTaskStatus {
  Planned = 'planned',
  InProgress = 'in_progress',
  Completed = 'completed',
  Requested = 'requested',
  Paused = 'paused',
  Rejected = 'rejected',
  Cancelled = 'cancelled',
}

export enum WorkspaceTaskInitialStatus {
  Planned = WorkspaceTaskStatus.Planned,
  InProgress = WorkspaceTaskStatus.InProgress,
  Completed = WorkspaceTaskStatus.Completed,
  Paused = WorkspaceTaskStatus.Paused,
  Rejected = WorkspaceTaskStatus.Rejected,
  Cancelled = WorkspaceTaskStatus.Cancelled,
}

export enum WorkspaceTaskScope {
  Self = 'self',
  All = 'all',
}

export enum CalendarToolName {
  Add = 'event_add',
  List = 'event_list',
  Update = 'event_update',
  Delete = 'event_delete',
}

export enum MessageToolName {
  Context = 'message_context',
  Search = 'message_search',
  Send = 'message_send',
  Update = 'message_update',
  Delete = 'message_delete',
}

export enum ChannelToolName {
  Update = 'channel_update',
}

export enum MessageTargetType {
  DirectMessage = 'directMessage',
  CurrentThread = 'currentThread',
  CurrentChannel = 'currentChannel',
  Channel = 'channel',
}

export enum MessageSearchScope {
  CurrentThread = 'currentThread',
  CurrentChannel = 'currentChannel',
  DirectMessage = 'directMessage',
  Channel = 'channel',
}

export enum MessageAuthor {
  Assistant = 'assistant',
  Requester = 'requester',
  Anyone = 'anyone',
}

export enum MessageDeliveryStatus {
  Sent = 'sent',
  Updated = 'updated',
  Deleted = 'deleted',
}

export enum DocumentToolName {
  Read = 'document_read',
}

export enum ImageToolName {
  Read = 'image_read',
}

export enum BrowserToolName {
  Open = 'browser_open',
  Snapshot = 'browser_snapshot',
  Screenshot = 'browser_screenshot',
  Click = 'browser_click',
}

export enum ArtifactToolName {
  Review = 'artifact_review',
}

export enum WebToolName {
  Search = 'web_search',
}

export enum ArtifactKind {
  Site = 'site',
  Slides = 'slides',
  PowerPoint = 'pptx',
  Word = 'docx',
  PDF = 'pdf',
}

export enum ArtifactIssueSeverity {
  Blocking = 'blocking',
  Warning = 'warning',
  Information = 'info',
}

export enum ArtifactIssueCategory {
  TextFit = 'textFit',
  Layout = 'layout',
  VisualHierarchy = 'visualHierarchy',
  ContentDensity = 'contentDensity',
  TemplateSmell = 'templateSmell',
  Responsiveness = 'responsiveness',
  RenderFidelity = 'renderFidelity',
}

export enum ArtifactEvidenceMimeType {
  PNG = 'image/png',
  JPEG = 'image/jpeg',
}

export enum SiteToolName {
  Serve = 'site_serve',
  List = 'site_list',
  Unserve = 'site_unserve',
}

export enum SiteServeMode {
  Preview = 'preview',
  Publish = 'publish',
}

export enum SiteLifecycleStatus {
  Draft = 'draft',
  Publishing = 'publishing',
  Published = 'published',
  Unpublished = 'unpublished',
  Failed = 'failed',
}

export enum ResourceMutationEffect {
  Created = 'created',
  Sent = 'sent',
  Updated = 'updated',
  Previewed = 'previewed',
  Published = 'published',
  Deleted = 'deleted',
}

export enum CalendarReminderLeadHours {
  One = 1,
  Two = 2,
  Three = 3,
  Six = 6,
  Twelve = 12,
  TwentyFour = 24,
  FortyEight = 48,
}

const workspaceTaskSizeSchema = z.enum(WorkspaceTaskSize);
const workspaceTaskStatusSchema = z.enum(WorkspaceTaskStatus);

const taskParticipantSchema = z.strictObject({
  personID: z.string().optional(),
  displayName: z.string().optional(),
  email: z.string().optional(),
  mattermostUsername: z.string().optional(),
  mention: z.string().optional(),
});

export const taskResultSchema = z.strictObject({
  taskID: resourceIDSchema,
  ownerID: z.string().optional(),
  ownerName: z.string().optional(),
  participantIDs: z.array(z.string()).optional(),
  participantNames: z.array(z.string()).optional(),
  participantPresentations: z.array(taskParticipantSchema).optional(),
  business: z.string().optional(),
  type: z.string().optional(),
  content: z.string().optional(),
  size: z.string().optional(),
  status: z.string().optional(),
  startDate: z.string().optional(),
  endDate: z.string().optional(),
  weekCode: z.string().optional(),
  flag: z.number().int().optional(),
  requestReason: z.string().optional(),
  decisionReason: z.string().optional(),
  mattermostPostID: z.string().optional(),
});

export const taskAddInputSchema = z.strictObject({
  title: z.string().describe(
    "Concise noun-phrase title for the work itself, in the user's language. Keep the user's exact title when they give one; otherwise write one rather than reusing their sentence. People belong in the person fields, not the title.",
  ),
  size: workspaceTaskSizeSchema
    .describe('Effort size using the work-size rubric. Omit when the request does not support a useful estimate.')
    .optional(),
  status: z.enum(WorkspaceTaskInitialStatus)
    .describe('Initial task status. Defaults to planned. The runtime may change delegated tasks to requested.')
    .optional(),
  startsAt: z.string().describe(`When the work starts. ${momentDescription} Resolve relative dates from the current date. Omit when the user did not specify one.`).optional(),
  endsAt: z.string().describe(`When the work is due. ${momentDescription} Resolve relative dates from the current date. Omit when the user did not specify one.`).optional(),
  participantPersonHints: z.array(z.string())
    .describe('Names, @handles, or emails of the people the task belongs to. Naming nobody makes it the requester\u2019s own.')
    .optional(),
});

export const taskAddInputIntentSchema = taskAddInputSchema.partial();

export const taskListInputSchema = z.strictObject({
  query: z.string()
    .describe("Free-text keyword filter matched against task titles and notes, e.g. 'budget'. Do not put dates, week codes, or person names here — use the dedicated fields instead.")
    .optional(),
  participantPersonHint: z.string().describe('Name or email of a specific person whose tasks to list. Leave empty to use scope.').optional(),
  scope: z.enum(WorkspaceTaskScope)
    .describe('Whose tasks to list when participantPersonHint is empty. Defaults to self. Use all only for an explicit workspace-wide request.')
    .optional(),
  weekFrom: z.number()
    .describe('Start of the week range as an offset from this week: 0 this week, -1 last week, 1 next week. Omit both weekFrom and weekTo to list the current week; widen the range for other periods.')
    .optional(),
  weekTo: z.number().describe('End of the week range as an offset from this week. Omit both weekFrom and weekTo to list the current week.').optional(),
  status: z.string()
    .describe("Filter by task status. Accepted values: 'planned', 'in_progress', 'completed', 'requested', 'paused', 'rejected', 'cancelled'. Leave empty to return all statuses.")
    .optional(),
  limit: z.number().describe('Maximum number of tasks to return. Defaults to 50.').optional(),
});

const taskHintSchema = z.string().min(1).max(256).describe(
  'Identifies the existing task to act on: its exact task ID or its exact CURRENT title as it appears in a task_list result. Never a new or intended title. Resolved server-side to the canonical task; if it does not uniquely resolve, the call fails with a candidates list of the matching tasks to retry against.',
);

const taskUpdateObjectSchema = z.strictObject({
  taskHint: taskHintSchema,
  title: z.string().describe('New task title.').optional(),
  status: workspaceTaskStatusSchema.describe('New task status.').optional(),
  size: workspaceTaskSizeSchema.describe('Effort size estimate.').optional(),
  business: z.string().describe('Business label, taken from registeredLabels.businesses in a task_list result.').optional(),
  type: z.string().describe('Task type label, taken from registeredLabels.types in a task_list result.').optional(),
  startsAt: z.string().describe(`When the work starts. ${momentDescription}`).optional(),
  endsAt: z.string().describe(`When the work is due. ${momentDescription}`).optional(),
  participantPersonHints: z.array(z.string())
    .describe('Names, @handles, or emails of everyone taking part, replacing the current participants. Send the whole set, not just additions.')
    .optional(),
});

export const taskUpdateInputSchema = taskUpdateObjectSchema
  .refine(hasMutationField, 'At least one task field must be updated.')
  .meta({ minProperties: 2 });

export const taskUpdateInputIntentSchema = taskUpdateObjectSchema.omit({ taskHint: true });

export const taskDeleteInputSchema = z.strictObject({
  taskHint: taskHintSchema,
});

export const taskDeleteInputIntentSchema = z.strictObject({});

const taskLabelVocabularySchema = z.strictObject({
  businesses: z.array(z.string()),
  types: z.array(z.string()),
  sizes: z.array(z.string()),
  statuses: z.array(z.string()),
});

export const taskListResultSchema = z.strictObject({
  tasks: z.array(taskResultSchema),
  count: z.number().int(),
  scope: z.string(),
  weekFrom: z.number().int().optional(),
  weekTo: z.number().int().optional(),
  statusFilter: z.string().optional(),
  ownerID: z.string().optional(),
  registeredLabels: taskLabelVocabularySchema,
});

export const taskDeleteResultSchema = z.strictObject({
  taskID: resourceIDSchema,
  deleted: z.literal(true),
});

export const personListInputSchema = z.strictObject({});

export const personListInputIntentSchema = z.strictObject({});

const personResultSchema = z.strictObject({
  personID: z.string(),
  name: z.string(),
  email: z.string(),
  mattermostUsername: z.string().optional(),
  mention: z.string().optional(),
});

export const personListResultSchema = z.strictObject({
  count: z.number(),
  people: z.array(personResultSchema),
});

const calendarParticipantResultSchema = z.strictObject({
  personID: z.string().optional(),
  name: z.string(),
  email: z.string().optional(),
});

export const calendarEventResultSchema = z.strictObject({
  eventID: resourceIDSchema,
  title: z.string(),
  note: z.string(),
  location: z.string(),
  startsAt: z.string(),
  endsAt: z.string(),
  isWholeDay: z.boolean(),
  participants: z.array(calendarParticipantResultSchema),
  notifyMinutesBefore: z.number().int().optional(),
  updatedAt: z.string(),
});

const calendarMutableFields = {
  title: z.string().describe('New event title.').optional(),
  note: z.string().describe('New event notes or agenda. Use an empty string to clear them.').optional(),
  location: z.string().describe('New physical or virtual location. Use an empty string to clear it.').optional(),
  startsAt: z.string().describe(`New event start. ${momentDescription}`).optional(),
  endsAt: z.string().describe(`New event end. ${momentDescription}`).optional(),
  isWholeDay: z.boolean().describe('Whether the event takes the whole day.').optional(),
  participantPersonHints: z.array(z.string())
    .describe('Names, @handles, or emails of everyone attending, replacing the current attendees. Send the whole set, not just additions.')
    .optional(),
  notifyMinutesBefore: z.number().int().positive().describe('Minutes before the start to notify attendees.').optional(),
};

export const calendarAddInputSchema = z.strictObject({
  title: z.string().describe('Event title shown in the calendar.'),
  note: z.string().describe('Optional event notes or agenda visible to attendees.').optional(),
  location: z.string().describe('Optional physical or virtual location.').optional(),
  startsAt: z.string().describe(`Event start. ${momentDescription}`),
  endsAt: z.string().describe(`Event end. ${momentDescription} It must be after startsAt.`),
  isWholeDay: z.boolean().describe('Set true for an event that takes the whole day.').optional(),
  participantPersonHints: z.array(z.string())
    .describe('Names, @handles, or emails of the people attending. Naming nobody makes the event the requester\u2019s own, naming colleagues makes it theirs, and naming everyone leaves it open to all.')
    .optional(),
  notifyMinutesBefore: z.number().int().positive().describe('Minutes before the start to notify attendees.').optional(),
});

export const calendarAddInputIntentSchema = calendarAddInputSchema.partial();

export const calendarListInputSchema = z.strictObject({
  startsAt: z.string().describe(`Inclusive start of the window. ${momentDescription}`).optional(),
  endsAt: z.string().describe(`Exclusive end of the window. ${momentDescription}`).optional(),
  weekFrom: z.number().int()
    .describe('Start of the week range as an offset from this week: 0 this week, -1 last week, 1 next week. Use instead of startsAt and endsAt when the user speaks in weeks.')
    .optional(),
  weekTo: z.number().int().describe('End of the week range as an offset from this week.').optional(),
  query: z.string().describe('Optional free-text filter matched against event titles, notes, and locations.').optional(),
  limit: z.number().positive().refine(Number.isInteger, 'Limit must be a whole number.').describe('Maximum number of events to return.').optional(),
});

const calendarEventHintSchema = z.string().min(1).max(256).describe(
  'Identifies the existing calendar event to act on: its exact event ID or its exact CURRENT title as it appears in a event_list result. Never a new or intended title. Resolved server-side; if it does not uniquely resolve, the call fails with a candidates list of matching events.',
);

const calendarUpdateObjectSchema = z.strictObject({
  eventHint: calendarEventHintSchema,
  ...calendarMutableFields,
});

export const calendarUpdateInputSchema = calendarUpdateObjectSchema
  .refine(hasMutationField, 'At least one calendar event field must be updated.')
  .meta({ minProperties: 2 });

export const calendarUpdateInputIntentSchema = calendarUpdateObjectSchema.omit({ eventHint: true });

export const calendarDeleteInputSchema = z.strictObject({
  eventHint: calendarEventHintSchema,
});

export const calendarDeleteInputIntentSchema = z.strictObject({});

export const calendarListResultSchema = z.strictObject({
  events: z.array(calendarEventResultSchema),
});

export const calendarDeleteResultSchema = z.strictObject({
  eventID: resourceIDSchema,
  deleted: z.literal(true),
});

const uniqueResourceIDArraySchema = z.array(resourceIDSchema)
  .min(1)
  .max(50)
  .refine(values => new Set(values).size === values.length, 'Resource identities must be unique.')
  .meta({ uniqueItems: true });

const uniqueMessageIDArraySchema = z.array(resourceIDSchema)
  .min(1)
  .max(25)
  .refine(values => new Set(values).size === values.length, 'Message identities must be unique.')
  .meta({ uniqueItems: true });

export const messageContextInputSchema = z.strictObject({});

export const messageSearchInputSchema = z.strictObject({
  messageIDs: uniqueMessageIDArraySchema.describe('Exact message IDs to read in full instead of searching. Returns each message\'s complete text rather than a preview. Use this before rewriting a long message.').optional(),
  scope: z.enum(MessageSearchScope)
    .describe('Where to search. Current conversation scopes use the active Mattermost context.')
    .optional(),
  channelName: z.string().describe('Exact Mattermost channel name without the # prefix.').optional(),
  channelID: resourceIDSchema.describe('Exact channel ID from message_context or a prior result.').optional(),
  personHint: z.string().describe('Exact name, @handle, or email of the direct-message counterpart.').optional(),
  authoredBy: z.enum(MessageAuthor).describe('Message author filter. Defaults to anyone.').optional(),
  queries: z.array(z.string().min(1)).describe('Keyword queries matched against message content.').optional(),
  limit: z.number().int().min(1).max(25).describe('Maximum messages to return. Defaults to 20.').optional(),
  cursor: z.string().describe('Pagination cursor from a previous message_search result.').optional(),
});

export const messageSendInputSchema = z.strictObject({
  targetType: z.enum(MessageTargetType).describe('Destination for the new message.'),
  message: z.string().min(1).regex(/\S/, 'Message must contain a non-whitespace character.'),
  channelName: z.string().describe('Exact Mattermost channel name without the # prefix.').optional(),
  channelID: resourceIDSchema.describe('Exact channel ID from message_context or a prior result.').optional(),
  personHint: z.string().describe('Name, @handle, or email of one direct-message recipient. Omit for a direct message to the requester themself.').optional(),
  personHints: z.array(z.string().min(1)).max(50).describe('Direct-message recipients for one fan-out send.').optional(),
  pin: z.boolean().describe('Whether to pin the created message. Defaults to false.').optional(),
  reason: z.string().describe('Reason shown to the approver.').optional(),
});

export const messageSendInputIntentSchema = messageSendInputSchema.partial();

const messageUpdateObjectSchema = z.strictObject({
  messageID: resourceIDSchema.describe('Exact message ID from message_search or message.send.'),
  oldText: z.string().min(1).regex(/\S/, 'oldText must contain a non-whitespace character.').describe('Exact text as it currently appears in that message, copied verbatim from a message_search preview or from the message you sent. Must occur exactly once in the message. Quote only the span that changes, never the whole message.').optional(),
  newText: z.string().describe('Text that replaces oldText. Empty string removes the span.').optional(),
  isPinned: z.boolean().describe('Whether the message should be pinned.').optional(),
});

export const messageUpdateInputSchema = messageUpdateObjectSchema
  .refine(hasPairedMessageEdit, 'oldText and newText must be given together.')
  .refine(hasMutationField, 'At least one message field must be updated.')
  .meta({ minProperties: 2 });

export const messageUpdateInputIntentSchema = messageUpdateObjectSchema.partial();

export const messageDeleteInputSchema = z.strictObject({
  messageIDs: uniqueMessageIDArraySchema.describe('Exact message IDs from message.search.'),
});

export const messageDeleteInputIntentSchema = messageDeleteInputSchema.partial();

const messageSearchCandidateSchema = z.strictObject({
  messageID: resourceIDSchema,
  channelID: resourceIDSchema,
  rootMessageID: resourceIDSchema.optional(),
  userID: resourceIDSchema,
  authoredBy: z.enum(MessageAuthor),
  createdAt: z.number().int().nonnegative(),
  text: z.string().optional(),
  preview: z.string().optional(),
  deletable: z.boolean(),
  protectedReason: z.string().optional(),
});

export const messageContextResultSchema = z.strictObject({
  platform: z.string().min(1),
  conversationID: z.string(),
  conversationType: z.string(),
  channelID: z.string(),
  channelName: z.string(),
  replyTargetID: z.string(),
  rootMessageID: z.string(),
  currentMessageID: z.string(),
  requesterPersonID: z.string(),
  requesterPlatformUserID: z.string(),
  botUserID: resourceIDSchema,
  botUsername: z.string().min(1),
});

export const messageSearchResultSchema = z.strictObject({
  scope: z.enum(MessageSearchScope),
  queries: z.array(z.string()),
  authoredBy: z.enum(MessageAuthor),
  messageIDs: z.array(resourceIDSchema),
  candidates: z.array(messageSearchCandidateSchema),
  nextCursor: z.string().optional(),
  hasMore: z.boolean(),
});

const messageDeliveryFailureSchema = z.strictObject({
  personHint: z.string().optional(),
  messageID: z.string().optional(),
  errorCode: z.string().min(1),
  message: z.string().min(1),
});

export const messageSendResultSchema = z.strictObject({
  messageIDs: uniqueResourceIDArraySchema,
  deliveryStatus: z.literal(MessageDeliveryStatus.Sent),
  failures: z.array(messageDeliveryFailureSchema).optional(),
});

export const messageUpdateResultSchema = z.strictObject({
  messageID: resourceIDSchema,
  deliveryStatus: z.literal(MessageDeliveryStatus.Updated),
  messageUpdated: z.boolean(),
  isPinned: z.boolean().optional(),
});

export const messageDeleteResultSchema = z.strictObject({
  messageIDs: uniqueResourceIDArraySchema,
  deliveryStatus: z.literal(MessageDeliveryStatus.Deleted),
  failures: z.array(messageDeliveryFailureSchema).optional(),
});

const channelUpdateObjectSchema = z.strictObject({
  channelID: resourceIDSchema.describe('Exact channel ID from message_context or a prior result.').optional(),
  channelName: z.string().min(1).describe('Exact Mattermost channel name without the # prefix.').optional(),
  header: z.string().describe('New channel header. Use an empty string to clear it.').optional(),
  displayName: z.string().min(1).describe('New channel display name.').optional(),
  inviteeHints: z.array(z.string().min(1)).describe('Names, @handles, or emails of people to invite.').optional(),
});

export const channelUpdateInputSchema = channelUpdateObjectSchema
  .refine(input => Boolean(input.channelID || input.channelName), 'channelID or channelName is required.')
  .refine(input => input.header !== undefined || input.displayName !== undefined || input.inviteeHints !== undefined, 'At least one channel field must be updated.')
  .meta({ minProperties: 2 });

export const channelUpdateInputIntentSchema = channelUpdateObjectSchema;

export const channelUpdateResultSchema = z.strictObject({
  channelID: resourceIDSchema,
  updated: z.literal(true),
  invitedUserIDs: z.array(resourceIDSchema).optional(),
});

const storedSiteSlugSchema = z.string()
  .min(1)
  .regex(/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/, 'Stored site slugs follow the admind acceptance pattern.');

export const siteServeInputSchema = z.strictObject({
  title: z.string().min(1).describe('Human-readable site title. The server derives and owns the URL slug from this title on first serve.'),
  sourceWorkspacePath: resourceIDSchema.describe('Exact workspace path of the site project root to serve — the directory containing DESIGN.md and app/, e.g. ~/sites/my-site.'),
  mode: z.enum(SiteServeMode).describe("Serve target: 'preview' for a temporary review URL, 'publish' for the public URL."),
  siteReference: resourceIDSchema.describe('Exact slug or siteID of an EXISTING served site to update, from site_list or an earlier serve result. Omit on first serve so the server allocates a new slug.').optional(),
});

export const siteServeInputIntentSchema = siteServeInputSchema.partial();

export const siteListInputSchema = z.strictObject({
  siteReference: resourceIDSchema.describe('Exact slug or siteID to narrow the listing to one site. Omit to list every served site.').optional(),
});

export const siteUnserveInputSchema = z.strictObject({
  siteReference: resourceIDSchema.describe('Exact slug or siteID of the served site to take down, from site_list or an earlier serve result.'),
  reason: z.string().describe('Reason shown in the approval prompt.').optional(),
});

export const siteUnserveInputIntentSchema = siteUnserveInputSchema.partial();

export const siteServeResultSchema = z.strictObject({
  siteID: resourceIDSchema,
  slug: storedSiteSlugSchema,
  mode: z.enum(SiteServeMode),
  previewURL: resourceIDSchema.optional(),
  publishedURL: resourceIDSchema.optional(),
  sourceSHA256: sha256Schema,
});

const siteListEntrySchema = z.strictObject({
  siteID: resourceIDSchema,
  slug: storedSiteSlugSchema,
  title: z.string(),
  status: z.enum(SiteLifecycleStatus),
  publishedURL: resourceIDSchema.optional(),
  updatedAt: z.string().optional(),
});

export const siteListResultSchema = z.strictObject({
  sites: z.array(siteListEntrySchema),
});

export const siteUnserveResultSchema = z.strictObject({
  siteID: resourceIDSchema,
  slug: storedSiteSlugSchema,
  unserved: z.literal(true),
});

export const documentReadInputSchema = z.strictObject({
  path: resourceIDSchema.describe('Exact absolute /workspace path of the document to read.'),
  maxPages: z.number().int().min(1).max(500).describe('Maximum PDF pages to extract. Omit for the runtime default.').optional(),
  maxOutputBytes: z.number().int().min(1024).max(1000000).describe('Maximum Markdown bytes to return. Omit for the runtime default.').optional(),
});

export const documentReadResultSchema = z.strictObject({
  status: z.literal('ok'),
  path: resourceIDSchema,
  format: z.literal('markdown'),
  content: z.string(),
  warnings: z.array(z.string()),
  truncated: z.boolean(),
  backend: z.string().optional(),
  model: z.string().optional(),
});

const imageReadAttachmentSchema = z.strictObject({
  devicePath: resourceIDSchema,
  filename: resourceIDSchema,
  contentType: resourceIDSchema,
  sizeBytes: z.number().int().nonnegative(),
  contentBase64: z.string().min(1),
});

export const imageReadInputSchema = z.strictObject({
  path: resourceIDSchema.describe('Exact absolute /workspace path of the image to read.'),
});

export const imageReadResultSchema = z.strictObject({
  status: z.literal('ok'),
  path: resourceIDSchema,
  attachments: z.array(imageReadAttachmentSchema).min(1),
});

export const browserOpenInputSchema = z.strictObject({
  url: resourceIDSchema.describe('Absolute HTTP or HTTPS URL to open.'),
});

export const browserOpenInputIntentSchema = browserOpenInputSchema.partial();

export const browserOpenResultSchema = z.strictObject({
  url: resourceIDSchema,
  requestedURL: resourceIDSchema,
  title: z.string().optional(),
  snapshotText: z.string().optional(),
  interactiveRefs: z.array(resourceIDSchema).optional(),
  capturedAt: resourceIDSchema,
});

export const browserSnapshotInputSchema = z.strictObject({});

export const browserSnapshotResultSchema = z.strictObject({
  url: resourceIDSchema.optional(),
  title: z.string().optional(),
  snapshotText: z.string(),
  interactiveRefs: z.array(resourceIDSchema),
  hasMore: z.boolean(),
  capturedAt: resourceIDSchema,
});

export const browserScreenshotInputSchema = z.strictObject({
  ttlSeconds: z.number().int().nonnegative().optional(),
});

export const browserScreenshotResultSchema = z.strictObject({
  fileID: resourceIDSchema,
  filename: resourceIDSchema,
  sizeBytes: z.number().int().nonnegative(),
  contentType: resourceIDSchema,
  devicePath: resourceIDSchema,
  expiresAt: resourceIDSchema,
  capturedAt: resourceIDSchema,
});

const browserClickObjectSchema = z.strictObject({
  target: resourceIDSchema.optional(),
  ref: resourceIDSchema.optional(),
  selector: resourceIDSchema.optional(),
});

export const browserClickInputSchema = browserClickObjectSchema
  .refine(hasAnyField, 'A browser target, ref, or selector is required.')
  .meta({ minProperties: 1 });

export const browserClickInputIntentSchema = browserClickObjectSchema;

export const browserClickResultSchema = z.strictObject({
  ok: z.literal(true),
  action: z.literal('click'),
  target: resourceIDSchema,
  capturedAt: resourceIDSchema,
});

const artifactReviewIssueSchema = z.strictObject({
  severity: z.enum(ArtifactIssueSeverity),
  category: z.enum(ArtifactIssueCategory),
  target: z.string(),
  message: z.string(),
  suggestedFix: z.string(),
});

export const artifactReviewInputSchema = z.strictObject({
  artifactKind: z.enum(ArtifactKind),
  intent: resourceIDSchema,
  rubric: resourceIDSchema,
  evidence: z.array(z.strictObject({
    role: resourceIDSchema,
    path: resourceIDSchema,
    mimeType: z.enum(ArtifactEvidenceMimeType),
    label: resourceIDSchema,
  })).min(1).max(8),
  expectedText: z.array(z.strictObject({
    target: resourceIDSchema,
    text: z.string(),
  })).optional(),
  previousIssues: z.array(artifactReviewIssueSchema).optional(),
});

export const artifactReviewResultSchema = z.strictObject({
  passed: z.boolean(),
  issues: z.array(artifactReviewIssueSchema),
  acceptedWarnings: z.array(z.string()),
  summary: z.string(),
});

export const webSearchInputSchema = z.strictObject({
  query: z.string().min(1).regex(/\S/, 'Search query must contain a non-whitespace character.'),
  location: z.string().optional(),
  language: z.string().optional(),
  limit: z.number().int().min(1).max(10).optional(),
  allowedDomains: z.array(z.string().min(1)).optional(),
  excludedDomains: z.array(z.string().min(1)).optional(),
});

const webSearchResultItemSchema = z.strictObject({
  title: z.string(),
  url: z.string(),
  snippet: z.string(),
  source: z.string().optional(),
});

export const webSearchResultSchema = z.strictObject({
  provider: z.string(),
  remoteLLMInvolved: z.boolean(),
  compatibility: z.string(),
  query: z.string(),
  answer: z.string(),
  results: z.array(webSearchResultItemSchema),
});

function hasMutationField(document: object): boolean {
  return Object.keys(document).length > 1;
}

function hasPairedMessageEdit(document: { oldText?: string | undefined; newText?: string | undefined }): boolean {
  return (document.oldText === undefined) === (document.newText === undefined);
}

function hasAnyField(document: object): boolean {
  return Object.keys(document).length > 0;
}

type CapabilityResultDefinition = {
  schema: z.ZodType;
  effects: Array<z.infer<typeof resourceEffectContractSchema>>;
  evidenceCondition?: {
    resultField: string;
    equals: z.infer<typeof jsonValueSchema>;
  };
};

type CapabilityToolDefinition = {
  name: string;
  namespace: string;
  privacyClass: string;
  policyResource: string;
  description: string;
  version: string;
  estimatedLatency: CapabilityEstimatedLatency;
  inputSchema: z.ZodType;
  inputIntentSchema?: z.ZodType;
  result: CapabilityResultDefinition;
  sideEffect: CapabilitySideEffect;
  idempotency?: z.infer<typeof capabilityIdempotencySchema>;
  requiresApproval?: boolean;
  requiresUserPresence?: boolean;
  completionEvidence?: {
    mode: string;
    action: string;
    targetKind: string;
  };
};

const taskToolDefinitions: CapabilityToolDefinition[] = [
  {
    name: 'task_add',
    namespace: 'task',
    privacyClass: 'workspace_task',
    policyResource: 'tool:task_add',
    description: 'Create a new workspace task with typed task fields. Use this to add a todo or assignment for the requester or another team member. Do not use this to update an existing task — use task.update.',
    version: '3',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: taskAddInputSchema,
    inputIntentSchema: taskAddInputIntentSchema,
    result: {
      schema: taskResultSchema,
      effects: [{
        objectType: 'task',
        effect: ResourceMutationEffect.Created,
        resultField: 'taskID',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.WorkspaceWrite,
    completionEvidence: { mode: 'success', action: 'write_task', targetKind: 'task' },
  },
  {
    name: 'task_list',
    namespace: 'task',
    privacyClass: 'workspace_task',
    policyResource: 'tool:task_list',
    description: "List workspace tasks with optional filters. Use this to answer 'what tasks does X have', 'what is on my plate', or 'show incomplete items this week'. The default scope is the requester; set scope to all for the whole workspace.",
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Low,
    inputSchema: taskListInputSchema,
    result: { schema: taskListResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.Read,
  },
  {
    name: 'task_update',
    namespace: 'task',
    privacyClass: 'workspace_task',
    policyResource: 'tool:task_update',
    description: 'Update explicit fields on an existing task, including who takes part in it. taskHint is the exact task ID or exact task title from a task_list result, resolved server-side to the canonical task; use task_list first when neither is known. At least one mutable field is required.',
    version: '3',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: taskUpdateInputSchema,
    inputIntentSchema: taskUpdateInputIntentSchema,
    result: {
      schema: taskResultSchema,
      effects: [{
        objectType: 'task',
        effect: ResourceMutationEffect.Updated,
        resultField: 'taskID',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.WorkspaceWrite,
    completionEvidence: { mode: 'success', action: 'write_task', targetKind: 'task' },
  },
  {
    name: 'task_delete',
    namespace: 'task',
    privacyClass: 'workspace_task',
    policyResource: 'tool:task_delete',
    description: 'Permanently delete a task. taskHint is the exact task ID or exact task title from a task_list result, resolved server-side to the canonical task; use task_list first when neither is known. Requires approval; this action is irreversible.',
    version: '3',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: taskDeleteInputSchema,
    inputIntentSchema: taskDeleteInputIntentSchema,
    result: {
      schema: taskDeleteResultSchema,
      effects: [{
        objectType: 'task',
        effect: ResourceMutationEffect.Deleted,
        resultField: 'taskID',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.Destructive,
    requiresApproval: true,
    completionEvidence: { mode: 'success', action: 'delete_task', targetKind: 'task' },
  },
  {
    name: 'person_list',
    namespace: 'person',
    privacyClass: 'workspace_task',
    policyResource: 'tool:person_list',
    description: 'List the people in this workspace with their exact name, email, @handle, and mention. Call this to answer who a person is, and whenever a message names someone partly or by a given name alone, so the exact name can be passed instead of the fragment. Person hints on the task tools resolve against exactly this list.',
    version: '1',
    estimatedLatency: CapabilityEstimatedLatency.Low,
    inputSchema: personListInputSchema,
    inputIntentSchema: personListInputIntentSchema,
    result: { schema: personListResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.Read,
  },
];

const calendarToolDefinitions: CapabilityToolDefinition[] = [
  {
    name: CalendarToolName.Add,
    namespace: 'calendar',
    privacyClass: 'workspace_calendar',
    policyResource: 'tool:event_add',
    description: 'Create a calendar event with a concrete time range. Resolve natural-language dates and times before calling. Use event_update for an existing event.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: calendarAddInputSchema,
    inputIntentSchema: calendarAddInputIntentSchema,
    result: {
      schema: calendarEventResultSchema,
      effects: [{
        objectType: 'calendar',
        effect: ResourceMutationEffect.Created,
        resultField: 'eventID',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.WorkspaceWrite,
    completionEvidence: { mode: 'success', action: 'write_calendar', targetKind: 'calendar' },
  },
  {
    name: CalendarToolName.List,
    namespace: 'calendar',
    privacyClass: 'workspace_calendar',
    policyResource: 'tool:event_list',
    description: 'List calendar events in a concrete time window, optionally filtered by title, description, or location. Resolve natural-language dates to startISO and endISO before calling.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Low,
    inputSchema: calendarListInputSchema,
    result: { schema: calendarListResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.Read,
  },
  {
    name: CalendarToolName.Update,
    namespace: 'calendar',
    privacyClass: 'workspace_calendar',
    policyResource: 'tool:event_update',
    description: 'Update explicit fields on a calendar event. eventHint is the exact event ID or exact event title from a event_list result, resolved server-side to the canonical event; use event_list first when neither is known. At least one mutable field is required.',
    version: '3',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: calendarUpdateInputSchema,
    inputIntentSchema: calendarUpdateInputIntentSchema,
    result: {
      schema: calendarEventResultSchema,
      effects: [{
        objectType: 'calendar',
        effect: ResourceMutationEffect.Updated,
        resultField: 'eventID',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.WorkspaceWrite,
    completionEvidence: { mode: 'success', action: 'write_calendar', targetKind: 'calendar' },
  },
  {
    name: CalendarToolName.Delete,
    namespace: 'calendar',
    privacyClass: 'workspace_calendar',
    policyResource: 'tool:event_delete',
    description: 'Permanently delete a calendar event. eventHint is the exact event ID or exact event title from a event_list result, resolved server-side to the canonical event; use event_list first when neither is known. Requires approval; this action is irreversible.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: calendarDeleteInputSchema,
    inputIntentSchema: calendarDeleteInputIntentSchema,
    result: {
      schema: calendarDeleteResultSchema,
      effects: [{
        objectType: 'calendar',
        effect: ResourceMutationEffect.Deleted,
        resultField: 'eventID',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.Destructive,
    requiresApproval: true,
    completionEvidence: { mode: 'success', action: 'write_calendar', targetKind: 'calendar' },
  },
];

const messageToolDefinitions: CapabilityToolDefinition[] = [
  {
    name: MessageToolName.Context,
    namespace: 'message',
    privacyClass: 'platform_message',
    policyResource: 'tool:message_context',
    description: 'Return the exact current Mattermost conversation, thread, requester, and bot identities.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Low,
    inputSchema: messageContextInputSchema,
    result: { schema: messageContextResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.Read,
  },
  {
    name: MessageToolName.Search,
    namespace: 'message',
    privacyClass: 'platform_message',
    policyResource: 'tool:message_search',
    description: 'Find messages in an exact conversation scope, or read known ones in full. Searching by queries returns message IDs with a short preview around the match. Passing messageIDs instead returns those messages complete, which is what you need before rewriting or summarising a long message rather than a phrase inside it.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Low,
    inputSchema: messageSearchInputSchema,
    result: { schema: messageSearchResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.Read,
  },
  {
    name: MessageToolName.Send,
    namespace: 'message',
    privacyClass: 'platform_message',
    policyResource: 'tool:message_send',
    description: 'Send a Mattermost message to a direct message, channel, or the current conversation after approval.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: messageSendInputSchema,
    inputIntentSchema: messageSendInputIntentSchema,
    result: {
      schema: messageSendResultSchema,
      effects: [{
        objectType: 'message',
        effect: ResourceMutationEffect.Sent,
        resultField: 'messageIDs',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.ExternalSend,
    idempotency: { supported: true, required: false, scope: 'operation' },
    requiresApproval: true,
    completionEvidence: { mode: 'success', action: 'send_message', targetKind: 'message' },
  },
  {
    name: MessageToolName.Update,
    namespace: 'message',
    privacyClass: 'platform_message',
    policyResource: 'tool:message_update',
    description: 'Replace one exact span of text inside your own earlier Mattermost message, leaving the rest of it untouched. oldText must appear exactly once in that message; copy it verbatim from a message_search preview or from the message you sent. This is the tool for every correction to something you already posted — when the user points out a mistake, fix that message instead of posting a correction as a new one. It runs immediately without asking the user to confirm, because it can only change text you quoted. If oldText does not match, the call fails without changing anything and returns the message as it currently reads, so retry with a span copied from that. To rewrite a long message wholesale, read it in full first with message_search messageIDs and quote what you read.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: messageUpdateInputSchema,
    inputIntentSchema: messageUpdateInputIntentSchema,
    result: {
      schema: messageUpdateResultSchema,
      effects: [{
        objectType: 'message',
        effect: ResourceMutationEffect.Updated,
        resultField: 'messageID',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.ExternalWrite,
    requiresApproval: false,
    completionEvidence: { mode: 'success', action: 'update_message', targetKind: 'message' },
  },
  {
    name: MessageToolName.Delete,
    namespace: 'message',
    privacyClass: 'platform_message',
    policyResource: 'tool:message_delete',
    description: 'Permanently delete exact Mattermost message IDs from message_search after approval. List only the exact messages the user asked to remove; when several search matches quote or mention the same text, pick the one message that is the target itself, never the whole match list.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: messageDeleteInputSchema,
    inputIntentSchema: messageDeleteInputIntentSchema,
    result: {
      schema: messageDeleteResultSchema,
      effects: [{
        objectType: 'message',
        effect: ResourceMutationEffect.Deleted,
        resultField: 'messageIDs',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.Destructive,
    requiresApproval: true,
    completionEvidence: { mode: 'success', action: 'delete_message', targetKind: 'message' },
  },
];

const channelToolDefinitions: CapabilityToolDefinition[] = [
  {
    name: ChannelToolName.Update,
    namespace: 'channel',
    privacyClass: 'platform_message',
    policyResource: 'tool:channel_update',
    description: 'Update an exact Mattermost channel display name, header, or membership after approval.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: channelUpdateInputSchema,
    inputIntentSchema: channelUpdateInputIntentSchema,
    result: {
      schema: channelUpdateResultSchema,
      effects: [{
        objectType: 'channel',
        effect: ResourceMutationEffect.Updated,
        resultField: 'channelID',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.ExternalWrite,
    requiresApproval: true,
    completionEvidence: { mode: 'success', action: 'update_channel', targetKind: 'channel' },
  },
];

const siteToolDefinitions: CapabilityToolDefinition[] = [
  {
    name: SiteToolName.Serve,
    namespace: 'site',
    privacyClass: 'workspace_site',
    policyResource: 'tool:site_serve',
    description: 'Serve a site project directory you built in the workspace: preview mode returns a temporary review URL, publish mode deploys to the public URL. First serve allocates the slug from the title; pass siteReference to update an existing served site.',
    version: '3',
    estimatedLatency: CapabilityEstimatedLatency.High,
    inputSchema: siteServeInputSchema,
    inputIntentSchema: siteServeInputIntentSchema,
    result: {
      schema: siteServeResultSchema,
      effects: [
        {
          objectType: 'website',
          effect: ResourceMutationEffect.Previewed,
          resultField: 'previewURL',
          effectIdentity: ResourceEffectIdentity.URL,
          when: { resultField: 'mode', equals: SiteServeMode.Preview },
        },
        {
          objectType: 'website',
          effect: ResourceMutationEffect.Published,
          resultField: 'publishedURL',
          effectIdentity: ResourceEffectIdentity.URL,
          when: { resultField: 'mode', equals: SiteServeMode.Publish },
        },
      ],
    },
    sideEffect: CapabilitySideEffect.SitePublish,
    completionEvidence: { mode: 'success', action: 'serve_site', targetKind: 'site' },
  },
  {
    name: SiteToolName.List,
    namespace: 'site',
    privacyClass: 'workspace_site',
    policyResource: 'tool:site_list',
    description: 'List served sites with their exact siteID, slug, lifecycle status, and published URL. Pass siteReference to read one site.',
    version: '3',
    estimatedLatency: CapabilityEstimatedLatency.Low,
    inputSchema: siteListInputSchema,
    result: { schema: siteListResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.Read,
  },
  {
    name: SiteToolName.Unserve,
    namespace: 'site',
    privacyClass: 'workspace_site',
    policyResource: 'tool:site_unserve',
    description: 'Take a served site down after explicit runtime approval: unpublishes it, frees its slug, and deletes the server-side record. Workspace source files are not touched.',
    version: '3',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: siteUnserveInputSchema,
    inputIntentSchema: siteUnserveInputIntentSchema,
    result: {
      schema: siteUnserveResultSchema,
      effects: [{
        objectType: 'website',
        effect: ResourceMutationEffect.Deleted,
        resultField: 'siteID',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.Destructive,
    requiresApproval: true,
    completionEvidence: { mode: 'success', action: 'delete_site', targetKind: 'site' },
  },
];

const fileToolDefinitions: CapabilityToolDefinition[] = [
  {
    name: DocumentToolName.Read,
    namespace: 'document',
    privacyClass: 'workspace_document',
    policyResource: 'tool:document_read',
    description: 'Read a workspace document from an exact /workspace path and return Markdown content. Use image_read for image files.',
    version: '1',
    estimatedLatency: CapabilityEstimatedLatency.High,
    inputSchema: documentReadInputSchema,
    result: { schema: documentReadResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.Read,
  },
  {
    name: ImageToolName.Read,
    namespace: 'image',
    privacyClass: 'workspace_document',
    policyResource: 'tool:image_read',
    description: 'Read a workspace image from an exact /workspace path and return a base64 attachment. Use document_read for document files.',
    version: '1',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: imageReadInputSchema,
    result: { schema: imageReadResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.Read,
  },
];

const browserToolDefinitions: CapabilityToolDefinition[] = [
  {
    name: BrowserToolName.Open,
    namespace: 'browser',
    privacyClass: 'user_browser',
    policyResource: 'tool:browser_open',
    description: 'Open an exact HTTP or HTTPS URL in the available browser and return the resulting page identity and initial structure.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Interactive,
    inputSchema: browserOpenInputSchema,
    inputIntentSchema: browserOpenInputIntentSchema,
    result: { schema: browserOpenResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.Connect,
    requiresUserPresence: true,
  },
  {
    name: BrowserToolName.Snapshot,
    namespace: 'browser',
    privacyClass: 'user_browser',
    policyResource: 'tool:browser_snapshot',
    description: 'Read the current browser page structure and return stable interactive references for inspection and control.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Interactive,
    inputSchema: browserSnapshotInputSchema,
    result: { schema: browserSnapshotResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.Read,
  },
  {
    name: BrowserToolName.Screenshot,
    namespace: 'browser',
    privacyClass: 'user_browser',
    policyResource: 'tool:browser_screenshot',
    description: 'Capture the visible browser page and upload it to a temporary workspace-visible device path for visual review.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Interactive,
    inputSchema: browserScreenshotInputSchema,
    result: { schema: browserScreenshotResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.Read,
  },
  {
    name: BrowserToolName.Click,
    namespace: 'browser',
    privacyClass: 'user_browser',
    policyResource: 'tool:browser_click',
    description: 'Click one exact target from the current browser snapshot and return the completed action.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Interactive,
    inputSchema: browserClickInputSchema,
    inputIntentSchema: browserClickInputIntentSchema,
    result: { schema: browserClickResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.ExternalWrite,
  },
];

const artifactToolDefinitions: CapabilityToolDefinition[] = [
  {
    name: ArtifactToolName.Review,
    namespace: 'artifact',
    privacyClass: 'workspace_document',
    policyResource: 'tool:artifact_review',
    description: 'Review rendered artifact screenshots against a concrete intent and rubric, returning typed visual issues and suggested fixes.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.High,
    inputSchema: artifactReviewInputSchema,
    result: {
      schema: artifactReviewResultSchema,
      effects: [],
      evidenceCondition: { resultField: 'passed', equals: true },
    },
    sideEffect: CapabilitySideEffect.Read,
  },
];

const webToolDefinitions: CapabilityToolDefinition[] = [
  {
    name: WebToolName.Search,
    namespace: 'web',
    privacyClass: 'public_web',
    policyResource: 'tool:web_search',
    description: 'Search the public web and return ranked result snippets. Use this when you need current information, facts, or links that are not already in context. Do not use for workspace data, calendar, mail, or tasks — those have dedicated tools.',
    version: '1',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: webSearchInputSchema,
    result: { schema: webSearchResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.Read,
  },
];

const capabilityToolDefinitions = [
  ...taskToolDefinitions,
  ...calendarToolDefinitions,
  ...messageToolDefinitions,
  ...channelToolDefinitions,
  ...webToolDefinitions,
  ...siteToolDefinitions,
  ...fileToolDefinitions,
  ...browserToolDefinitions,
  ...artifactToolDefinitions,
];

export type CapabilityToolCatalog = {
  protocolVersion: string;
  tools: Array<z.infer<typeof capabilityDescriptorSchema>>;
};

export type TaskAddInput = z.infer<typeof taskAddInputSchema>;
export type TaskListInput = z.infer<typeof taskListInputSchema>;
export type TaskUpdateInput = z.infer<typeof taskUpdateInputSchema>;
export type TaskDeleteInput = z.infer<typeof taskDeleteInputSchema>;
export type TaskResult = z.infer<typeof taskResultSchema>;
export type CalendarAddInput = z.infer<typeof calendarAddInputSchema>;
export type CalendarListInput = z.infer<typeof calendarListInputSchema>;
export type CalendarUpdateInput = z.infer<typeof calendarUpdateInputSchema>;
export type CalendarDeleteInput = z.infer<typeof calendarDeleteInputSchema>;
export type CalendarEventResult = z.infer<typeof calendarEventResultSchema>;
export type MessageContextInput = z.infer<typeof messageContextInputSchema>;
export type MessageSearchInput = z.infer<typeof messageSearchInputSchema>;
export type MessageSendInput = z.infer<typeof messageSendInputSchema>;
export type MessageUpdateInput = z.infer<typeof messageUpdateInputSchema>;
export type MessageDeleteInput = z.infer<typeof messageDeleteInputSchema>;
export type MessageContextResult = z.infer<typeof messageContextResultSchema>;
export type MessageSearchResult = z.infer<typeof messageSearchResultSchema>;
export type MessageSendResult = z.infer<typeof messageSendResultSchema>;
export type MessageUpdateResult = z.infer<typeof messageUpdateResultSchema>;
export type MessageDeleteResult = z.infer<typeof messageDeleteResultSchema>;
export type ChannelUpdateInput = z.infer<typeof channelUpdateInputSchema>;
export type ChannelUpdateResult = z.infer<typeof channelUpdateResultSchema>;
export type SiteServeInput = z.infer<typeof siteServeInputSchema>;
export type SiteListInput = z.infer<typeof siteListInputSchema>;
export type SiteUnserveInput = z.infer<typeof siteUnserveInputSchema>;
export type SiteServeResult = z.infer<typeof siteServeResultSchema>;
export type SiteListResult = z.infer<typeof siteListResultSchema>;
export type SiteUnserveResult = z.infer<typeof siteUnserveResultSchema>;
export type DocumentReadInput = z.infer<typeof documentReadInputSchema>;
export type DocumentReadResult = z.infer<typeof documentReadResultSchema>;
export type ImageReadInput = z.infer<typeof imageReadInputSchema>;
export type ImageReadResult = z.infer<typeof imageReadResultSchema>;
export type BrowserOpenInput = z.infer<typeof browserOpenInputSchema>;
export type BrowserOpenResult = z.infer<typeof browserOpenResultSchema>;
export type BrowserSnapshotInput = z.infer<typeof browserSnapshotInputSchema>;
export type BrowserSnapshotResult = z.infer<typeof browserSnapshotResultSchema>;
export type BrowserScreenshotInput = z.infer<typeof browserScreenshotInputSchema>;
export type BrowserScreenshotResult = z.infer<typeof browserScreenshotResultSchema>;
export type BrowserClickInput = z.infer<typeof browserClickInputSchema>;
export type BrowserClickResult = z.infer<typeof browserClickResultSchema>;
export type ArtifactReviewInput = z.infer<typeof artifactReviewInputSchema>;
export type ArtifactReviewResult = z.infer<typeof artifactReviewResultSchema>;
export type WebSearchInput = z.infer<typeof webSearchInputSchema>;
export type WebSearchResult = z.infer<typeof webSearchResultSchema>;

export function buildCapabilityToolCatalog(protocolVersion: string): CapabilityToolCatalog {
  return {
    protocolVersion,
    tools: capabilityToolDefinitions.map(definition => buildCapabilityDescriptor(definition)),
  };
}

function buildCapabilityDescriptor(definition: CapabilityToolDefinition): z.infer<typeof capabilityDescriptorSchema> {
  const resultSchema = z.toJSONSchema(definition.result.schema);
  const resultContract = {
    schema: resultSchema,
    effects: definition.result.effects,
    evidenceCondition: definition.result.evidenceCondition,
  };
  return capabilityDescriptorSchema.parse({
    name: definition.name,
    canonicalName: definition.name,
    namespace: definition.namespace,
    modelName: definition.name,
    modelVisibility: CapabilityModelVisibility.Visible,
    modelVisible: true,
    description: definition.description,
    version: definition.version,
    privacyClass: definition.privacyClass,
    estimatedLatency: definition.estimatedLatency,
    requiresUserPresence: definition.requiresUserPresence ?? false,
    worksOffline: false,
    inputSchema: z.toJSONSchema(definition.inputSchema),
    inputIntentSchema: definition.inputIntentSchema === undefined
      ? undefined
      : z.toJSONSchema(definition.inputIntentSchema),
    outputSchema: resultSchema,
    inputSchemaStrict: true,
    outputSchemaStrict: true,
    resultContract,
    policyResource: definition.policyResource,
    sideEffectClass: definition.sideEffect,
    sideEffect: definition.sideEffect,
    requiresApproval: definition.requiresApproval,
    completionEvidence: definition.completionEvidence,
    availability: { state: CapabilityAvailabilityState.Available },
    idempotency: definition.idempotency ?? { supported: false, required: false, scope: 'operation' },
  });
}
