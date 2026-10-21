import { timingSafeEqual } from 'node:crypto';
import { readFileSync } from 'node:fs';

const shortestServiceTokenLength = 32;

export function serviceTokenFrom(environment: Record<string, string | undefined>): string {
  const path = environment['CHATD_SERVICE_TOKEN_PATH']?.trim();
  if (!path) {
    throw new Error('CHATD_SERVICE_TOKEN_PATH is not configured, so chatd cannot tell the host services from anything else on this machine');
  }
  const token = readFileSync(path, 'utf8').trim();
  if (token.length < shortestServiceTokenLength) {
    throw new Error(`${path} holds no service token of at least ${shortestServiceTokenLength} characters`);
  }
  return token;
}

export function carriesServiceToken(request: Request, serviceToken: string): boolean {
  const authorization = request.headers.get('Authorization') ?? '';
  if (!authorization.startsWith('Bearer ')) return false;
  const offered = Buffer.from(authorization.slice('Bearer '.length));
  const expected = Buffer.from(serviceToken);
  return offered.length === expected.length && timingSafeEqual(offered, expected);
}
