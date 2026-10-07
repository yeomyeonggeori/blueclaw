import { z } from 'zod';

export { personEventCompanyKind } from './person-event-kind.ts';

// A person's conversations, said the same way whichever messenger they are on.
// chatd speaks it, the host relays it, and the screen keeps itself current
// from it, so each of them reads these shapes from here.

export const personalMentionsSchema = z.object({
  externalIDs: z.array(z.string()),
  isEveryone: z.boolean()
});

// A platform that keeps its custom emoji in a registry leaves the picture out
// and answers for the name separately; one that puts it on the reaction says so
// here.
export const personalReactionSchema = z.object({
  emoji: z.string(),
  imageURL: z.string().optional(),
  byExternalIDs: z.array(z.string())
});

// A store that addresses a file by the hash of its contents says so here, so a
// reader that keeps its own copy can tell it already has these bytes without
// fetching them again. Empty when the platform names files some other way.
export const personalAttachmentSchema = z.object({
  id: z.string(),
  filename: z.string(),
  contentType: z.string(),
  sizeBytes: z.number(),
  digest: z.string(),
  widthPixels: z.number().optional(),
  heightPixels: z.number().optional()
});

export const personalMessageSchema = z.object({
  id: z.string(),
  conversationID: z.string(),
  parentID: z.string().optional(),
  authorExternalID: z.string(),
  body: z.string(),
  postedAt: z.string(),
  editedAt: z.string().optional(),
  mentions: personalMentionsSchema.optional(),
  reactions: z.array(personalReactionSchema),
  attachments: z.array(personalAttachmentSchema)
});

// What changed in a person's conversations. A screen that has read a
// conversation once keeps it current from these alone; "conversation" is the
// one that says to read the list again, because a name or a roster changes too
// rarely to describe piece by piece.
export const personEventSchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('message'), message: personalMessageSchema }),
  z.object({
    kind: z.literal('message.edited'),
    conversationID: z.string(),
    messageID: z.string(),
    body: z.string(),
    editedAt: z.string()
  }),
  z.object({ kind: z.literal('message.removed'), conversationID: z.string(), messageID: z.string() }),
  z.object({
    kind: z.literal('reaction'),
    conversationID: z.string(),
    messageID: z.string(),
    emoji: z.string(),
    imageURL: z.string().optional(),
    externalID: z.string(),
    isAdded: z.boolean()
  }),
  z.object({ kind: z.literal('read'), readAtOfConversation: z.record(z.string(), z.string()) }),
  z.object({ kind: z.literal('conversation'), conversationID: z.string() }),
  z.object({ kind: z.literal('typing'), conversationID: z.string(), externalID: z.string() })
]);

export const personEventDeliverySchema = z.object({
  event: personEventSchema,
  recipientExternalIDs: z.array(z.string())
});

export type PersonalMentions = z.infer<typeof personalMentionsSchema>;
export type PersonalReaction = z.infer<typeof personalReactionSchema>;
export type PersonalAttachment = z.infer<typeof personalAttachmentSchema>;
export type PersonalMessage = z.infer<typeof personalMessageSchema>;
export type PersonEvent = z.infer<typeof personEventSchema>;
export type PersonEventDelivery = z.infer<typeof personEventDeliverySchema>;
