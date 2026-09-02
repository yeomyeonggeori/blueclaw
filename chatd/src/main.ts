import { Chat, type Adapter } from 'chat';
import { createMemoryState } from '@chat-adapter/state-memory';
import { createBuzzAdapter } from './adapters/buzz/index.ts';
import { createMattermostAdapter } from './adapters/mattermost/index.ts';
import { loadConfiguration } from './configuration.ts';
import { createBridge } from './bridge.ts';
import { createOutboundHandler } from './outbound.ts';
import { createRequestHandler } from './server.ts';
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

const outboundHandler = createOutboundHandler(adapters as never, configuration, personalGateways);

let connectedToTheRelay = false;

// Connecting first meant a relay that was down left no port open at all, while
// systemd reported the unit active and the journal stayed empty. The port is
// the one thing that can say so, so it opens before the connection is tried.
Bun.serve({
  port: configuration.listenPort,
  hostname: configuration.listenHostname,
  fetch: createRequestHandler({
    isReady: () => connectedToTheRelay,
    mattermostWebhook: adapters.mattermost
      ? (request) => chat.webhooks.mattermost?.(request) ?? new Response('Not Found', { status: 404 })
      : undefined,
    outbound: outboundHandler,
  }),
});

await chat.initialize();
connectedToTheRelay = true;
