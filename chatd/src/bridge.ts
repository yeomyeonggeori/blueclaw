import type { Chat, Message, Thread } from 'chat';
import type { ChatdConfiguration } from './configuration.ts';
import { deliverInboundEventToRelay, type NormalizedInboundEvent } from './relay-inbound.ts';
import {
  buildVisibleContext,
  emptyVisibleContext,
  inputAttachmentsOfMessage,
  type NormalizedPlatformAdapter,
} from './visible-context.ts';

export type BridgeInboundEvent = {
  kind: 'direct_message' | 'mention' | 'channel_message' | 'action';
  platform: 'mattermost';
  threadID: string;
  messageID: string;
  senderID: string;
  senderUserName: string;
  text: string;
  actionID?: string;
  actionValue?: string;
};

type BridgeChat = Pick<Chat, 'onDirectMessage' | 'onNewMention' | 'onSubscribedMessage' | 'onAction'>;

export type NormalizedInboundRouting = {
  conversationID: string;
  replyTargetID: string;
  isThread: boolean;
  historyScopeThreadID: string;
};

export function createBridge(
  chat: BridgeChat,
  configuration: ChatdConfiguration,
  normalizedAdapters: Record<string, NormalizedPlatformAdapter>,
): void {
  chat.onDirectMessage(async (thread, message) => {
    await forwardMessage(configuration, normalizedAdapters, 'direct_message', thread, message);
  });
  chat.onNewMention(async (thread, message) => {
    await thread.subscribe();
    await forwardMessage(configuration, normalizedAdapters, 'mention', thread, message);
  });
  chat.onSubscribedMessage(async (thread, message) => {
    await forwardMessage(
      configuration,
      normalizedAdapters,
      message.isMention ? 'mention' : 'channel_message',
      thread,
      message,
    );
  });
  for (const [platform, adapter] of Object.entries(normalizedAdapters)) {
    adapter.onMessageEdit?.(async (edit) => {
      await forwardNormalizedEvent(configuration, adapter, platform, { id: edit.threadID }, edit.message, edit.eventID);
    });
  }
  chat.onAction(async (event) => {
    await forwardLegacyEvent(configuration, {
      kind: 'action',
      platform: 'mattermost',
      threadID: event.threadId,
      messageID: event.messageId ?? '',
      senderID: event.user.userId,
      senderUserName: event.user.userName,
      text: '',
      actionID: event.actionId,
      actionValue: typeof event.value === 'string' ? event.value : JSON.stringify(event.value ?? null),
    });
  });
}

function platformOfThread(threadID: string): string {
  return threadID.split(':')[0] ?? '';
}

async function forwardMessage(
  configuration: ChatdConfiguration,
  normalizedAdapters: Record<string, NormalizedPlatformAdapter>,
  kind: BridgeInboundEvent['kind'],
  thread: Thread,
  message: Message,
): Promise<void> {
  const platform = platformOfThread(thread.id);
  const adapter = normalizedAdapters[platform];
  if (adapter) {
    await forwardNormalizedEvent(configuration, adapter, platform, thread, message);
    return;
  }
  await forwardLegacyEvent(configuration, {
    kind,
    platform: 'mattermost',
    threadID: thread.id,
    messageID: message.id,
    senderID: message.author.userId,
    senderUserName: message.author.userName,
    text: message.text,
  });
}

async function forwardNormalizedEvent(
  configuration: ChatdConfiguration,
  adapter: NormalizedPlatformAdapter,
  platform: string,
  thread: Pick<Thread, 'id'>,
  message: Message,
  eventID?: string,
): Promise<void> {
  const scopeThreadId = adapter.historyScopeThreadId(thread.id, message.id);
  const threadRootID = adapter.threadRootIdOf?.(message.raw);
  const conversationID = adapter.historyScopeThreadId(thread.id, threadRootID ?? message.id);
  // A message written under a root is read against that root and its replies. A
  // message that starts its own exchange is read against the other exchanges
  // this place holds, which is what they opened with and not what was said
  // inside them.
  const startsItsOwnExchange = scopeThreadId !== thread.id;
  const context = await buildVisibleContext(adapter, scopeThreadId, {
    beforeMessageId: message.id,
    senderId: message.author.userId,
    onlyExchangeOpenings: startsItsOwnExchange,
  }).catch(() => emptyVisibleContext(scopeThreadId));
  const addressing = adapter.addressingOf(message.raw);
  const event: NormalizedInboundEvent & { eventID?: string } = {
    platform,
    conversationID,
    messageID: message.id,
    senderID: message.author.userId,
    replyTargetID: thread.id,
    prompt: message.text,
    context: {
      ...context,
      inputAttachments: inputAttachmentsOfMessage(platform, message) ?? [],
      addressing: {
        botMentioned: addressing.botMentioned || message.isMention === true,
        otherPersonMentioned: addressing.otherPersonMentioned,
      },
    },
    ...(eventID ? { eventID } : {}),
  };
  if (configuration.relayInboundURL) {
    await deliverInboundEventToRelay(configuration.relayInboundURL, event);
    return;
  }
  const response = await fetch(`${configuration.blueclawBaseURL}/connectors/${platform}/events`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(event),
  });
  if (!response.ok) {
    throw new Error(`blueclaw ${platform} ingress returned ${response.status}`);
  }
}

export function normalizedInboundRoutingOf(
  adapter: NormalizedPlatformAdapter,
  thread: Pick<Thread, 'id'>,
  message: Pick<Message, 'id' | 'raw'>,
): NormalizedInboundRouting {
  const threadRootID = adapter.threadRootIdOf?.(message.raw);
  const historyScopeThreadID = adapter.historyScopeThreadId(thread.id, message.id);
  const conversationID = adapter.historyScopeThreadId(thread.id, threadRootID ?? message.id);
  return {
    conversationID,
    replyTargetID: thread.id,
    isThread: Boolean(threadRootID && threadRootID !== message.id),
    historyScopeThreadID,
  };
}

async function forwardLegacyEvent(configuration: ChatdConfiguration, event: BridgeInboundEvent): Promise<void> {
  if (!configuration.blueclawIngressURL) return;
  const response = await fetch(configuration.blueclawIngressURL, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(event),
  });
  if (!response.ok) {
    throw new Error(`blueclaw ingress returned ${response.status}`);
  }
}
