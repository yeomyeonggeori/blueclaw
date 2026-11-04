import { z } from 'zod';

import { agentPartSchema } from './agent.ts';
import { nonNegativeIntegerSchema } from './common.ts';

export enum AskInteractionKind {
  Confirm = 'ask_confirm',
  Input = 'ask_input',
}

export enum AskSelectionMode {
  Single = 'single',
  Multiple = 'multiple',
}

export const inputAttachmentSchema = z.looseObject({
  platform: z.string().optional(),
  fileID: z.string().optional(),
  messageID: z.string().optional(),
  filename: z.string().optional(),
  contentType: z.string().optional(),
  sizeBytes: nonNegativeIntegerSchema.optional(),
  path: z.string().optional(),
  isAvailable: z.boolean().optional(),
  errorCode: z.string().optional(),
  message: z.string().optional(),
});

export const visibleContextSenderSchema = z.looseObject({
  platform: z.string().optional(),
  senderID: z.string().optional(),
  handle: z.string().optional(),
  email: z.string().optional(),
  name: z.string().optional(),
  callingName: z.string().optional(),
});

export const visibleContextMessageSchema = z.looseObject({
  speaker: z.string(),
  speakerCallingName: z.string().optional(),
  speakerHandle: z.string().optional(),
  text: z.string(),
  sentAt: z.iso.datetime({ offset: true }).optional(),
  inputAttachments: z.array(inputAttachmentSchema).optional(),
});

export const visibleContextSchema = z.looseObject({
  messages: z.array(visibleContextMessageSchema).nullable(),
  hasMoreBefore: z.boolean(),
  historyCursor: z.string(),
  messagesOpenOtherExchanges: z.boolean().optional(),
  responseLanguage: z.string().optional(),
  requesterTimeZone: z.string().optional(),
  sender: visibleContextSenderSchema.optional(),
  conversationType: z.string().optional(),
  channelID: z.string().optional(),
  channelName: z.string().optional(),
  roomMemberEmails: z.array(z.string()).optional(),
  addressing: z.looseObject({
    botMentioned: z.boolean().optional(),
    otherPersonMentioned: z.boolean().optional(),
  }).optional(),
  attachmentsOnly: z.boolean().optional(),
  inputAttachments: z.array(inputAttachmentSchema).optional(),
  materials: z.array(inputAttachmentSchema).optional(),
});

export const platformInboundEventSchema = z.looseObject({
  conversationID: z.string(),
  messageID: z.string(),
  senderID: z.string(),
  replyTargetID: z.string(),
  prompt: z.string(),
  inputParts: z.array(agentPartSchema).optional(),
  responseLanguage: z.string().optional(),
  context: visibleContextSchema,
  legacyFields: z.record(z.string(), z.unknown()).optional(),
});

export const askChoiceOptionSchema = z.looseObject({
  key: z.string(),
  label: z.string(),
  shortLabel: z.string().optional(),
  value: z.string().optional(),
});

export const askInteractionSchema = z.looseObject({
  interactionID: z.string(),
  taskRunID: z.string(),
  kind: z.enum(AskInteractionKind),
  message: z.string().optional(),
  question: z.string().optional(),
  options: z.array(askChoiceOptionSchema).optional(),
  recommendedOptionKey: z.string().optional(),
  selectionMode: z.enum(AskSelectionMode).optional(),
  responseLanguage: z.string().optional(),
  targetPlatformUserID: z.string().optional(),
});

export const replyTargetSchema = z.looseObject({
  conversationID: z.string(),
  replyTargetID: z.string(),
  dedupeKey: z.string(),
});

export const connectorRuntimeResultSchema = z.looseObject({
  handled: z.boolean(),
  platform: z.string(),
  duplicate: z.boolean(),
  ignored: z.boolean(),
  reason: z.string().optional(),
  taskRunID: z.string().optional(),
  replyDispatchID: z.string().optional(),
});

export type PlatformInboundEvent = z.infer<typeof platformInboundEventSchema>;
