import { describe, expect, test } from 'bun:test';
import { createBuzzGateway } from '../src/mirror/buzz-publisher.ts';

const noopSession = {
	edit: async () => 'edit-event',
	remove: async () => undefined,
	react: async () => undefined,
};

describe('buzz gateway', () => {
	test('publishes as the person with an origin tag and returns the event id', async () => {
		const calls: Array<Record<string, unknown>> = [];
		const gateway = createBuzzGateway('wss://relay', 'auth-json', {
			send: async (request) => {
				calls.push(request as unknown as Record<string, unknown>);
				return { id: 'event-1', body: request.message, attachments: [] };
			},
			...noopSession,
		});

		const result = await gateway.publish({
			userSecretHex: 'deadbeef',
			buzzChannelId: 'chan-1',
			text: 'hi',
			origin: { platform: 'mattermost', externalId: 'post-9' },
			replyToBuzzEventId: 'root-5',
		});

		expect(result).toEqual({ eventId: 'event-1' });
		expect(calls[0]).toMatchObject({
			relayURL: 'wss://relay',
			authTagJSON: 'auth-json',
			userSecretHex: 'deadbeef',
			channelID: 'chan-1',
			message: 'hi',
			replyToRootId: 'root-5',
			extraTags: [['origin', 'mattermost', 'post-9']],
		});
	});
});
