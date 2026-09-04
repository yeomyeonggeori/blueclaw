import { afterEach, describe, expect, test } from 'bun:test';
import { deliverInboundEventToRelay, type NormalizedInboundEvent } from '../src/relay-inbound.ts';

const originalFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = originalFetch;
});

const relayInboundURL = 'https://relay.example.com/inbound';

function inboundEvent(): NormalizedInboundEvent {
  return {
    platform: 'buzz',
    conversationID: 'buzz:room-1',
    messageID: 'message-1',
    senderID: 'person-1',
    replyTargetID: 'buzz:room-1',
    prompt: 'hello',
    context: { inputAttachments: [] },
  };
}

function respondWith(statuses: number[]): { requestCount: () => number } {
  let requestCount = 0;
  globalThis.fetch = (async () => {
    const status = statuses[Math.min(requestCount, statuses.length - 1)] ?? 202;
    requestCount += 1;
    return new Response(null, { status });
  }) as never;
  return { requestCount: () => requestCount };
}

async function sleepInstantly(): Promise<void> {}

describe('delivering an inbound event to the relay', () => {
  test('posts once when the relay accepts on the first attempt', async () => {
    const relay = respondWith([202]);

    await deliverInboundEventToRelay(relayInboundURL, inboundEvent(), sleepInstantly);

    expect(relay.requestCount()).toBe(1);
  });

  test('retries a server error and resolves once the relay accepts', async () => {
    const relay = respondWith([500, 202]);

    await deliverInboundEventToRelay(relayInboundURL, inboundEvent(), sleepInstantly);

    expect(relay.requestCount()).toBe(2);
  });

  test('treats a 200 as a failure because only 202 means durably queued', async () => {
    const relay = respondWith([200, 202]);

    await deliverInboundEventToRelay(relayInboundURL, inboundEvent(), sleepInstantly);

    expect(relay.requestCount()).toBe(2);
  });

  test('backs off further on every retry, capped at five seconds', async () => {
    respondWith([503, 503, 503, 503, 503, 503, 503, 202]);
    const delays: number[] = [];

    await deliverInboundEventToRelay(relayInboundURL, inboundEvent(), async (milliseconds) => {
      delays.push(milliseconds);
    });

    expect(delays).toEqual([250, 500, 1000, 2000, 4000, 5000, 5000]);
  });

  test('gives up after the last attempt with an error naming the message', async () => {
    const relay = respondWith([500]);

    await expect(
      deliverInboundEventToRelay(relayInboundURL, inboundEvent(), sleepInstantly),
    ).rejects.toThrow(/message-1/);
    expect(relay.requestCount()).toBe(8);
  });

  test('retries a thrown fetch and reports its message when the attempts run out', async () => {
    globalThis.fetch = (async () => {
      throw new Error('The operation timed out.');
    }) as never;

    await expect(
      deliverInboundEventToRelay(relayInboundURL, inboundEvent(), sleepInstantly),
    ).rejects.toThrow(/The operation timed out\./);
  });
});
