import { describe, expect, test } from 'bun:test';
import { appendFileSync, mkdtempSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { deliveredMessagesAt, deliveryMemorySeconds } from '../src/delivered-messages.ts';

const now = 1_790_000_000;

function freshDirectory(): string {
  return mkdtempSync(join(tmpdir(), 'chatd-delivered-'));
}

describe('deliveredMessagesAt', () => {
  test('a directory with no record yet starts empty, and says so', () => {
    const delivered = deliveredMessagesAt(freshDirectory(), now);
    expect(delivered.startedEmpty).toBe(true);
    expect(delivered.has('a')).toBe(false);
  });

  test('a message recorded before a restart is still known after it', () => {
    const directory = freshDirectory();
    deliveredMessagesAt(directory, now).record('a', now - 60);

    const afterRestart = deliveredMessagesAt(directory, now + 60);

    expect(afterRestart.startedEmpty).toBe(false);
    expect(afterRestart.has('a')).toBe(true);
  });

  test('a message older than the memory is forgotten and dropped from the file', () => {
    const directory = freshDirectory();
    const delivered = deliveredMessagesAt(directory, now);
    delivered.record('old', now - deliveryMemorySeconds - 1);
    delivered.record('recent', now - 1);

    const afterRestart = deliveredMessagesAt(directory, now);

    expect(afterRestart.has('old')).toBe(false);
    expect(afterRestart.has('recent')).toBe(true);
    expect(readFileSync(join(directory, 'delivered-messages.jsonl'), 'utf8')).not.toContain('"old"');
  });

  test('a line cut short by a crash is skipped and the rest is kept', () => {
    const directory = freshDirectory();
    deliveredMessagesAt(directory, now).record('whole', now);
    appendFileSync(join(directory, 'delivered-messages.jsonl'), '{"id":"torn","a');

    const afterRestart = deliveredMessagesAt(directory, now);

    expect(afterRestart.has('whole')).toBe(true);
    expect(afterRestart.has('torn')).toBe(false);
  });

  test('the oldest message worth catching up is one memory back', () => {
    expect(deliveredMessagesAt(freshDirectory(), now).oldestRememberedSeconds(now)).toBe(now - deliveryMemorySeconds);
  });
});
