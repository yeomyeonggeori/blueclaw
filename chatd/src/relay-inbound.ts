export type NormalizedInboundEvent = {
  platform: string;
  conversationID: string;
  messageID: string;
  senderID: string;
  replyTargetID: string;
  prompt: string;
  context: unknown;
};

export type SleepFunction = (milliseconds: number) => Promise<void>;

type DeliveryAttempt = { accepted: true } | { accepted: false; failure: string };

const acceptedStatusCode = 202;
const requestTimeoutMilliseconds = 10_000;
const maximumAttempts = 8;
const initialBackoffMilliseconds = 250;
const maximumBackoffMilliseconds = 5_000;

export async function deliverInboundEventToRelay(
  relayInboundURL: string,
  event: NormalizedInboundEvent,
  sleep: SleepFunction = sleepForMilliseconds,
): Promise<void> {
  let backoffMilliseconds = initialBackoffMilliseconds;
  let lastFailure = 'no attempt was made';
  for (let attempt = 1; attempt <= maximumAttempts; attempt += 1) {
    const outcome = await attemptDelivery(relayInboundURL, event);
    if (outcome.accepted) return;
    lastFailure = outcome.failure;
    if (attempt === maximumAttempts) break;
    await sleep(backoffMilliseconds);
    backoffMilliseconds = Math.min(backoffMilliseconds * 2, maximumBackoffMilliseconds);
  }
  throw new Error(
    `relay inbound did not accept ${event.platform} message ${event.messageID} after ${maximumAttempts} attempts: ${lastFailure}`,
  );
}

async function attemptDelivery(relayInboundURL: string, event: NormalizedInboundEvent): Promise<DeliveryAttempt> {
  try {
    const response = await fetch(relayInboundURL, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(event),
      signal: AbortSignal.timeout(requestTimeoutMilliseconds),
    });
    if (response.status === acceptedStatusCode) return { accepted: true };
    return { accepted: false, failure: `status ${response.status}` };
  } catch (error) {
    return { accepted: false, failure: error instanceof Error ? error.message : String(error) };
  }
}

function sleepForMilliseconds(milliseconds: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}
