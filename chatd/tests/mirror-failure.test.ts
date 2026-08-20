import { describe, expect, test } from 'bun:test';
import {
	MirrorTally,
	describeMirrorFailure,
	subjectOfBuzzEvent,
	subjectOfPlatformEvent,
} from '../src/mirror/failure.ts';

describe('what a mirror failure says', () => {
	test('names the post, the channel and the person', () => {
		const line = describeMirrorFailure(
			subjectOfPlatformEvent({
				externalId: 'post-1',
				externalChannelId: 'channel-1',
				text: '안녕',
				senderPlatformUserId: 'user-1',
				senderEmail: 'lee@example.test'
			}),
			new Error('relay rejected event: restricted: not a channel member')
		);

		expect(line).toBe(
			'post=post-1 channel=channel-1 person=lee@example.test reason=relay rejected event: restricted: not a channel member'
		);
	});

	test('carries the message and not the Error, so a bundled build prints no source', () => {
		const failure = new Error('edit target event not found');
		failure.stack = 'at handleFrame (/$bunfs/root/chatd:19265:30)';

		const line = describeMirrorFailure({ post: 'post-1' }, failure);

		expect(line).not.toContain('bunfs');
		expect(line).toBe('post=post-1 reason=edit target event not found');
	});

	test('takes the buzz event id from whichever field carries it', () => {
		expect(subjectOfBuzzEvent({
			buzzEventId: 'event-1', buzzChannelId: 'buzz-1', text: '안녕',
			senderPubkey: 'pub', senderName: '이샘플', origin: null
		}).post).toBe('event-1');

		expect(subjectOfBuzzEvent({
			targetEventId: 'event-2', buzzChannelId: 'buzz-1', text: '고침', origin: null
		}).post).toBe('event-2');
	});

	test('leaves out what it does not know', () => {
		expect(describeMirrorFailure({ channel: 'channel-1' }, 'refused')).toBe('channel=channel-1 reason=refused');
	});
});

describe('the summary that makes silence mean nothing happened', () => {
	test('counts each direction and operation separately', () => {
		const tally = new MirrorTally();
		tally.succeeded('mattermost -> buzz message');
		tally.succeeded('mattermost -> buzz message');
		tally.failed('mattermost -> buzz edit');

		expect(tally.take()).toBe('mattermost -> buzz message ok 2, mattermost -> buzz edit failed 1');
	});

	test('says nothing when nothing happened', () => {
		expect(new MirrorTally().take()).toBe('');
	});

	test('forgets what it has already reported', () => {
		const tally = new MirrorTally();
		tally.succeeded('buzz -> platforms message');
		tally.take();

		expect(tally.take()).toBe('');
	});
});
