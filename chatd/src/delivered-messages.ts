import { appendFileSync, existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';

export const deliveryMemorySeconds = 3 * 24 * 60 * 60;

export type DeliveredMessages = {
  readonly startedEmpty: boolean;
  has(messageID: string): boolean;
  record(messageID: string, sentAtSeconds: number): void;
  oldestRememberedSeconds(nowSeconds: number): number;
};

type Entry = { id: string; at: number };

export function deliveredMessagesInMemory(): DeliveredMessages {
  return ledgerOver(new Map(), true, () => undefined);
}

export function deliveredMessagesAt(stateDirectory: string, nowSeconds: number): DeliveredMessages {
  const path = join(stateDirectory, 'delivered-messages.jsonl');
  const startedEmpty = !existsSync(path);
  const remembered = startedEmpty ? new Map<string, number>() : entriesStillRemembered(path, nowSeconds);
  rewrite(path, remembered);
  return ledgerOver(remembered, startedEmpty, (entry) => appendFileSync(path, `${JSON.stringify(entry)}\n`));
}

function ledgerOver(
  remembered: Map<string, number>,
  startedEmpty: boolean,
  persist: (entry: Entry) => void,
): DeliveredMessages {
  return {
    startedEmpty,
    has: (messageID) => remembered.has(messageID),
    record(messageID, sentAtSeconds) {
      if (remembered.has(messageID)) return;
      remembered.set(messageID, sentAtSeconds);
      persist({ id: messageID, at: sentAtSeconds });
    },
    oldestRememberedSeconds: (nowSeconds) => nowSeconds - deliveryMemorySeconds,
  };
}

function entriesStillRemembered(path: string, nowSeconds: number): Map<string, number> {
  const horizon = nowSeconds - deliveryMemorySeconds;
  const remembered = new Map<string, number>();
  for (const line of readFileSync(path, 'utf8').split('\n')) {
    const entry = entryOf(line);
    if (entry && entry.at >= horizon) remembered.set(entry.id, entry.at);
  }
  return remembered;
}

function entryOf(line: string): Entry | undefined {
  if (!line.trim()) return undefined;
  const parsed = parsedLine(line);
  return isEntry(parsed) ? parsed : undefined;
}

function parsedLine(line: string): unknown {
  try {
    return JSON.parse(line);
  } catch {
    return undefined;
  }
}

function isEntry(value: unknown): value is Entry {
  return (
    typeof value === 'object' &&
    value !== null &&
    'id' in value &&
    typeof value.id === 'string' &&
    'at' in value &&
    typeof value.at === 'number'
  );
}

function rewrite(path: string, remembered: Map<string, number>): void {
  mkdirSync(dirname(path), { recursive: true });
  const lines = [...remembered].map(([id, at]) => `${JSON.stringify({ id, at })}\n`).join('');
  const temporary = `${path}.rewriting`;
  writeFileSync(temporary, lines);
  renameSync(temporary, path);
}
