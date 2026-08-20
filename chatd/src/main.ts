import { Chat, type Adapter } from 'chat';
import { createMemoryState } from '@chat-adapter/state-memory';
import { createBuzzAdapter } from './adapters/buzz/index.ts';
import { createMattermostAdapter } from './adapters/mattermost/index.ts';
import { loadConfiguration } from './configuration.ts';
import { createBridge } from './bridge.ts';
import { createOutboundHandler } from './outbound.ts';
import { createMirror, type MirrorWiring } from './mirror/wire.ts';
import type { NormalizedPlatformAdapter } from './visible-context.ts';
import { createMattermostPersonalGateway } from './personal/mattermost.ts';
import { createBuzzPersonalGateway } from './personal/buzz.ts';
import type { PersonalGateway } from './personal/gateway.ts';

const configuration = loadConfiguration(process.env);

// Peripheral platforms only; Buzz is the hub, not a fan-out target.
const connectedPlatforms: string[] = [];
if (configuration.mattermost) connectedPlatforms.push('mattermost');

let mirror: MirrorWiring | undefined;
if (configuration.admindBaseURL && configuration.buzz) {
  mirror = createMirror({
    admindBaseURL: configuration.admindBaseURL,
    connectedPlatforms,
    buzz: { relayURL: configuration.buzz.relayURL, authTagJSON: configuration.buzz.authTagJSON },
    mattermost:
      configuration.mattermost?.adminToken
        ? { baseURL: configuration.mattermost.baseURL, adminToken: configuration.mattermost.adminToken }
        : undefined,
    onError: (context, detail) => console.error('[mirror]', context, detail),
    onSummary: (summary) => console.log('[mirror]', summary),
  });
}

const adapters: Record<string, Adapter> = {};
const normalizedAdapters: Record<string, NormalizedPlatformAdapter> = {};
const personalGateways: Record<string, PersonalGateway> = {};
if (configuration.mattermost) {
  adapters.mattermost = createMattermostAdapter({
    baseUrl: configuration.mattermost.baseURL,
    botToken: configuration.mattermost.botToken,
    callbackUrl: configuration.mattermost.actionCallbackURL,
    mirror: mirror?.mattermost,
  });
  personalGateways.mattermost = createMattermostPersonalGateway(configuration.mattermost.baseURL);
}
if (configuration.buzz) {
  const buzzAdapter = createBuzzAdapter({
    relayURL: configuration.buzz.relayURL,
    privateKeyHex: configuration.buzz.privateKeyHex,
    botDisplayName: configuration.botUserName,
    accountLinksPath: configuration.buzz.accountLinksPath,
    authTagJSON: configuration.buzz.authTagJSON,
    mirror: mirror?.buzz,
  });
  adapters.buzz = buzzAdapter;
  normalizedAdapters.buzz = buzzAdapter;
  personalGateways.buzz = createBuzzPersonalGateway(buzzAdapter, {
    relayURL: configuration.buzz.relayURL,
    authTagJSON: configuration.buzz.authTagJSON,
  });
}

const chat = new Chat({
  userName: configuration.botUserName,
  state: createMemoryState(),
  concurrency: 'queue',
  adapters,
});

createBridge(chat, configuration, normalizedAdapters);

await chat.initialize();

const outboundHandler = createOutboundHandler(adapters as never, configuration, personalGateways);

Bun.serve({
  port: configuration.listenPort,
  hostname: '127.0.0.1',
  fetch: async (request) => {
    const requestUrl = new URL(request.url);
    if (requestUrl.pathname === '/webhooks/mattermost' && adapters.mattermost) {
      return chat.webhooks.mattermost?.(request) ?? new Response('Not Found', { status: 404 });
    }
    if (requestUrl.pathname.startsWith('/v1/platform/')) {
      return outboundHandler(request);
    }
    return new Response('Not Found', { status: 404 });
  },
});
