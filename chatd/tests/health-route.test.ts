import { describe, expect, test } from 'bun:test';
import { createRequestHandler, healthPath } from '../src/server.ts';

function handlerFor(isReady: () => boolean) {
  return createRequestHandler({
    isReady,
    outbound: async () => new Response('outbound', { status: 200 }),
  });
}

describe('the health route', () => {
  test('answers before the relay connection is up, and says it is not ready', async () => {
    const response = await handlerFor(() => false)(new Request('http://chatd' + healthPath));
    expect(response.status).toBe(503);
  });

  test('reports ready once the relay connection is up', async () => {
    const response = await handlerFor(() => true)(new Request('http://chatd' + healthPath));
    expect(response.status).toBe(200);
  });

  test('leaves every other unknown path a 404', async () => {
    const response = await handlerFor(() => true)(new Request('http://chatd/nothing'));
    expect(response.status).toBe(404);
  });

  test('routes platform calls to the outbound handler', async () => {
    const response = await handlerFor(() => true)(
      new Request('http://chatd/v1/platform/buzz/dm.ensure', { method: 'POST' }),
    );
    expect(await response.text()).toBe('outbound');
  });
});
