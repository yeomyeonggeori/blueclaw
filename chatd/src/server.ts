import { carriesServiceToken } from './service-credential.ts';

export const healthPath = '/healthz';

export type RequestRoutes = {
  isReady: () => boolean;
  serviceToken: string;
  mattermostWebhook?: (request: Request) => Response | Promise<Response>;
  outbound: (request: Request) => Response | Promise<Response>;
};

export function createRequestHandler(routes: RequestRoutes) {
  return async (request: Request): Promise<Response> => {
    const requestPath = new URL(request.url).pathname;
    if (requestPath === healthPath) {
      return routes.isReady()
        ? new Response('ready', { status: 200 })
        : new Response('starting', { status: 503 });
    }
    if (requestPath === '/webhooks/mattermost' && routes.mattermostWebhook) {
      return routes.mattermostWebhook(request);
    }
    if (requestPath.startsWith('/v1/platform/')) {
      if (!carriesServiceToken(request, routes.serviceToken)) {
        return new Response(JSON.stringify({ error: 'chatd answers only the host services that hold its service token' }), {
          status: 401,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      return routes.outbound(request);
    }
    return new Response('Not Found', { status: 404 });
  };
}
