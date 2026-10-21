import { afterAll, describe, expect, test } from 'bun:test';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createRequestHandler } from '../src/server.ts';
import { asAHostService, serviceTokenFrom } from '../src/service-credential.ts';

const directory = mkdtempSync(join(tmpdir(), 'chatd-service-token-'));
afterAll(() => rmSync(directory, { recursive: true, force: true }));

const serviceToken = 'a'.repeat(64);

function chatdHolding(token: string) {
  const reached: string[] = [];
  const handler = createRequestHandler({
    isReady: () => true,
    serviceToken: token,
    outbound: async (request) => {
      reached.push(new URL(request.url).pathname);
      return new Response('{}', { status: 200 });
    },
  });
  return { handler, reached };
}

function post(path: string, authorization?: string): Request {
  return new Request('http://127.0.0.1:18090' + path, {
    method: 'POST',
    headers: authorization === undefined ? {} : { Authorization: authorization },
    body: '{"replyTargetID":"buzz:general","message":"아무나 보낸 글"}',
  });
}

describe('the platform routes answer only the services that hold the token', () => {
  for (const [why, authorization] of [
    ['no credential', undefined],
    ['an empty bearer', 'Bearer '],
    ['a wrong bearer', 'Bearer ' + 'b'.repeat(64)],
    ['the token without the scheme', serviceToken],
    ['a prefix of the token', 'Bearer ' + serviceToken.slice(0, 63)],
  ] as const) {
    test(`a call with ${why} is refused and posts nothing`, async () => {
      const chatd = chatdHolding(serviceToken);
      for (const capability of ['message.post', 'dm.send', 'reply.send', 'person.dm.send']) {
        const response = await chatd.handler(post(`/v1/platform/buzz/${capability}`, authorization));
        expect(response.status).toBe(401);
      }
      expect(chatd.reached).toEqual([]);
    });
  }

  test('a call that carries the token reaches the platform', async () => {
    const chatd = chatdHolding(serviceToken);
    const response = await chatd.handler(post('/v1/platform/buzz/message.post', 'Bearer ' + serviceToken));
    expect(response.status).toBe(200);
    expect(chatd.reached).toEqual(['/v1/platform/buzz/message.post']);
  });

  test('the health route still answers without one, so a supervisor can see chatd is up', async () => {
    const chatd = chatdHolding(serviceToken);
    const response = await chatd.handler(new Request('http://127.0.0.1:18090/healthz'));
    expect(response.status).toBe(200);
  });
});

describe('the token chatd checks against', () => {
  test('is read from the file the service is pointed at', () => {
    const path = join(directory, 'service-token');
    writeFileSync(path, serviceToken + '\n');
    expect(serviceTokenFrom({ CHATD_SERVICE_TOKEN_PATH: path })).toBe(serviceToken);
  });

  test('stops chatd from starting when none is named, or the file is empty or short', () => {
    const emptyPath = join(directory, 'empty');
    writeFileSync(emptyPath, '\n');
    const shortPath = join(directory, 'short');
    writeFileSync(shortPath, 'short');
    expect(() => serviceTokenFrom({})).toThrow('CHATD_SERVICE_TOKEN_PATH');
    expect(() => serviceTokenFrom({ CHATD_SERVICE_TOKEN_PATH: emptyPath })).toThrow();
    expect(() => serviceTokenFrom({ CHATD_SERVICE_TOKEN_PATH: shortPath })).toThrow();
    expect(() => serviceTokenFrom({ CHATD_SERVICE_TOKEN_PATH: join(directory, 'missing') })).toThrow();
  });
});

describe('what chatd sends to the other host services', () => {
  test('carries the same token, read from the file it was pointed at', () => {
    const path = join(directory, 'outgoing-service-token');
    writeFileSync(path, serviceToken + '\n');

    const init = asAHostService({ method: 'POST', headers: { 'Content-Type': 'application/json' } }, { CHATD_SERVICE_TOKEN_PATH: path });

    const headers = new Headers(init.headers);
    expect(headers.get('Authorization')).toBe('Bearer ' + serviceToken);
    expect(headers.get('Content-Type')).toBe('application/json');
  });

  test('is left as it was where no token is named', () => {
    const init = { method: 'POST' };
    expect(asAHostService(init, {})).toBe(init);
  });
});
