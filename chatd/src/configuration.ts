import { readFileSync } from 'node:fs';

export type MattermostConfiguration = {
  baseURL: string;
  botToken: string;
  actionCallbackURL: string | undefined;
  adminToken: string | undefined;
};

export type BuzzConfiguration = {
  relayURL: string;
  privateKeyHex: string;
  accountLinksPath: string | undefined;
  authTagJSON: string | undefined;
  keySeed: string | undefined;
};

export type ChatdConfiguration = {
  botUserName: string;
  blueclawBaseURL: string;
  blueclawIngressURL: string | undefined;
  admindBaseURL: string | undefined;
  listenPort: number;
  listenHostname: string;
  mattermost: MattermostConfiguration | undefined;
  buzz: BuzzConfiguration | undefined;
};

export function loadConfiguration(environment: Record<string, string | undefined>): ChatdConfiguration {
  const configuration: ChatdConfiguration = {
    botUserName: requireValue(environment, 'CHATD_BOT_USER_NAME'),
    blueclawBaseURL: trimTrailingSlash(
      environment['CHATD_BLUECLAW_BASE_URL']?.trim() ||
        deriveBaseURL(environment['CHATD_BLUECLAW_INGRESS_URL']) ||
        'http://127.0.0.1:8080',
    ),
    blueclawIngressURL: environment['CHATD_BLUECLAW_INGRESS_URL']?.trim() || undefined,
    admindBaseURL: environment['CHATD_ADMIND_BASE_URL']?.trim() || undefined,
    listenPort: parseListenPort(environment['CHATD_LISTEN_PORT']),
    listenHostname: environment['CHATD_LISTEN_HOSTNAME']?.trim() || '127.0.0.1',
    mattermost: loadMattermostConfiguration(environment),
    buzz: loadBuzzConfiguration(environment),
  };
  if (!configuration.mattermost && !configuration.buzz) {
    throw new Error('chatd needs at least one platform: set CHATD_MATTERMOST_* or CHATD_BUZZ_* variables');
  }
  if (configuration.mattermost && !configuration.blueclawIngressURL) {
    throw new Error('CHATD_BLUECLAW_INGRESS_URL is required when mattermost is enabled');
  }
  return configuration;
}

function loadMattermostConfiguration(environment: Record<string, string | undefined>): MattermostConfiguration | undefined {
  const baseURL = environment['CHATD_MATTERMOST_BASE_URL']?.trim();
  if (!baseURL) return undefined;
  return {
    baseURL,
    botToken: requireValue(environment, 'CHATD_MATTERMOST_BOT_TOKEN'),
    actionCallbackURL: environment['CHATD_ACTION_CALLBACK_URL'],
    adminToken: environment['CHATD_MATTERMOST_ADMIN_TOKEN']?.trim() || undefined,
  };
}

function loadBuzzConfiguration(environment: Record<string, string | undefined>): BuzzConfiguration | undefined {
  const relayURL = environment['CHATD_BUZZ_RELAY_URL']?.trim();
  if (!relayURL) return undefined;
  const privateKeyHex = requireValue(environment, 'CHATD_BUZZ_PRIVATE_KEY').toLowerCase();
  if (!/^[0-9a-f]{64}$/.test(privateKeyHex)) {
    throw new Error('CHATD_BUZZ_PRIVATE_KEY must be 64 hex characters');
  }
  return {
    relayURL,
    privateKeyHex,
    accountLinksPath: environment['CHATD_BUZZ_ACCOUNT_LINKS_PATH']?.trim() || undefined,
    authTagJSON: environment['CHATD_BUZZ_AUTH_TAG']?.trim() || undefined,
    keySeed: readKeySeed(environment['CHATD_BUZZ_KEY_SEED_PATH']?.trim()),
  };
}

function readKeySeed(keySeedPath: string | undefined): string | undefined {
  if (!keySeedPath) return undefined;
  try {
    return readFileSync(keySeedPath, 'utf8').trim() || undefined;
  } catch (error) {
    console.error(`chatd cannot read the buzz key seed at ${keySeedPath}, so it can only change its own messages: ${String(error)}`);
    return undefined;
  }
}

function deriveBaseURL(ingressURL: string | undefined): string | undefined {
  const trimmed = ingressURL?.trim();
  if (!trimmed) return undefined;
  try {
    return new URL(trimmed).origin;
  } catch {
    return undefined;
  }
}

function trimTrailingSlash(value: string): string {
  return value.endsWith('/') ? value.slice(0, -1) : value;
}

function requireValue(environment: Record<string, string | undefined>, key: string): string {
  const value = environment[key]?.trim();
  if (!value) throw new Error(`${key} is not configured`);
  return value;
}

function parseListenPort(value: string | undefined): number {
  const port = Number(value ?? '18090');
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error('CHATD_LISTEN_PORT must be a valid TCP port');
  }
  return port;
}
