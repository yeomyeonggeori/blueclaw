import { beforeEach, describe, expect, test } from 'bun:test';
import type { ChannelMapping, MessageMapping } from '../src/mirror/mapping-store.ts';
import {
	MirrorOrchestrator,
	type BuzzDelete,
	type BuzzEdit,
	type BuzzGateway,
	type BuzzPublish,
	type BuzzReaction,
	type MappingStoreLike,
	type MirrorIdentity,
	type PlatformDelete,
	type PlatformEdit,
	type PlatformGateway,
	type PlatformPost,
	type PlatformReaction,
} from '../src/mirror/orchestrator.ts';
import { EditTargetLost } from '../src/adapters/buzz/user-session.ts';

class FakeMappingStore implements MappingStoreLike {
	messages: MessageMapping[] = [];
	channels: ChannelMapping[] = [];
	async recordMessage(mapping: MessageMapping): Promise<void> {
		this.messages.push(mapping);
	}
	async messageByExternal(platform: string, externalId: string): Promise<MessageMapping | null> {
		return this.messages.find((m) => m.platform === platform && m.externalId === externalId) ?? null;
	}
	async messageByEvent(buzzEventId: string, platform: string): Promise<MessageMapping | null> {
		return this.messages.find((m) => m.buzzEventId === buzzEventId && m.platform === platform) ?? null;
	}
	async forgetMessage(platform: string, externalId: string): Promise<void> {
		this.messages = this.messages.filter((m) => !(m.platform === platform && m.externalId === externalId));
	}
	async recordChannel(mapping: ChannelMapping): Promise<void> {
		this.channels.push(mapping);
	}
	async channelByBuzz(buzzChannelId: string, platform: string): Promise<ChannelMapping | null> {
		return this.channels.find((c) => c.buzzChannelId === buzzChannelId && c.platform === platform) ?? null;
	}
}

class FakeBuzzGateway implements BuzzGateway {
	publishes: BuzzPublish[] = [];
	edits: BuzzEdit[] = [];
	removes: BuzzDelete[] = [];
	reactions: BuzzReaction[] = [];
	private serial = 0;
	async publish(publish: BuzzPublish): Promise<{ eventId: string }> {
		this.publishes.push(publish);
		this.serial += 1;
		return { eventId: `event-${this.serial}` };
	}
	lostTargets = new Set<string>();
	async edit(edit: BuzzEdit): Promise<void> {
		if (this.lostTargets.has(edit.targetEventId)) throw new EditTargetLost(edit.targetEventId);
		this.edits.push(edit);
	}
	async remove(remove: BuzzDelete): Promise<void> {
		this.removes.push(remove);
	}
	async react(react: BuzzReaction): Promise<void> {
		this.reactions.push(react);
	}
}

class FakePlatformGateway implements PlatformGateway {
	posts: PlatformPost[] = [];
	edits: PlatformEdit[] = [];
	removes: PlatformDelete[] = [];
	reactions: PlatformReaction[] = [];
	async post(post: PlatformPost): Promise<{ externalId: string }> {
		this.posts.push(post);
		return { externalId: `${post.target}-msg` };
	}
	async edit(edit: PlatformEdit): Promise<void> {
		this.edits.push(edit);
	}
	async remove(remove: PlatformDelete): Promise<void> {
		this.removes.push(remove);
	}
	async react(react: PlatformReaction): Promise<void> {
		this.reactions.push(react);
	}
}

class FakeIdentity implements MirrorIdentity {
	async secretForEmail(email: string): Promise<string> {
		return `secret-${email}`;
	}
	async buzzChannelForExternal(_platform: string, externalChannelId: string): Promise<string> {
		return `bc-${externalChannelId}`;
	}
}

describe('platform -> Buzz', () => {
	let store: FakeMappingStore;
	let buzz: FakeBuzzGateway;
	let orchestrator: MirrorOrchestrator;

	beforeEach(() => {
		store = new FakeMappingStore();
		buzz = new FakeBuzzGateway();
		orchestrator = new MirrorOrchestrator(store, ['mattermost', 'slack'], buzz, {}, new FakeIdentity());
	});

	test('publishes a platform message to Buzz with an origin and records the mapping', async () => {
		await orchestrator.onPlatformMessage({
			platform: 'mattermost',
			externalId: 'post-1',
			externalChannelId: 'mm-chan',
			text: 'hello',
			sender: { platform: 'mattermost', platformUserId: 'u1', email: 'a@example.com' },
		});
		expect(buzz.publishes).toHaveLength(1);
		expect(buzz.publishes[0]?.origin).toEqual({ platform: 'mattermost', externalId: 'post-1' });
		expect(await store.messageByExternal('mattermost', 'post-1')).not.toBeNull();
	});

	test('skips a message it already mirrored, so a fanned-out copy never re-publishes', async () => {
		await store.recordMessage({ buzzEventId: 'event-x', platform: 'mattermost', externalId: 'post-1', externalChannelId: 'mm-chan' });
		await orchestrator.onPlatformMessage({
			platform: 'mattermost',
			externalId: 'post-1',
			externalChannelId: 'mm-chan',
			text: 'echo',
			sender: { platform: 'mattermost', platformUserId: 'u1', email: 'a@example.com' },
		});
		expect(buzz.publishes).toHaveLength(0);
	});

	test('mirrors an edit to a Buzz edit against the mapped event', async () => {
		await store.recordMessage({ buzzEventId: 'event-1', platform: 'mattermost', externalId: 'post-1', externalChannelId: 'mm-chan' });
		await orchestrator.onPlatformEdit({
			platform: 'mattermost',
			externalId: 'post-1',
			externalChannelId: 'mm-chan',
			text: 'edited',
			sender: { platform: 'mattermost', platformUserId: 'u1', email: 'a@example.com' },
		});
		expect(buzz.edits).toHaveLength(1);
		expect(buzz.edits[0]?.targetEventId).toBe('event-1');
		expect(buzz.edits[0]?.text).toBe('edited');
	});

	test('forgets a mapping the relay has lost instead of retrying it forever', async () => {
		await store.recordMessage({ buzzEventId: 'event-gone', platform: 'mattermost', externalId: 'post-1', externalChannelId: 'mm-chan' });
		buzz.lostTargets.add('event-gone');
		const edit = {
			platform: 'mattermost',
			externalId: 'post-1',
			externalChannelId: 'mm-chan',
			text: 'edited',
			sender: { platform: 'mattermost', platformUserId: 'u1', email: 'a@example.com' },
		};

		await orchestrator.onPlatformEdit(edit);

		expect(await store.messageByExternal('mattermost', 'post-1')).toBeNull();
		await orchestrator.onPlatformEdit({ ...edit, text: 'edited again' });
		expect(buzz.edits).toHaveLength(0);
	});

	test('a refusal that is not a lost target still surfaces', async () => {
		await store.recordMessage({ buzzEventId: 'event-1', platform: 'mattermost', externalId: 'post-2', externalChannelId: 'mm-chan' });
		buzz.edit = async () => {
			throw new Error('restricted: not a channel member');
		};

		await expect(orchestrator.onPlatformEdit({
			platform: 'mattermost',
			externalId: 'post-2',
			externalChannelId: 'mm-chan',
			text: 'edited',
			sender: { platform: 'mattermost', platformUserId: 'u1', email: 'a@example.com' },
		})).rejects.toThrow('restricted');
		expect(await store.messageByExternal('mattermost', 'post-2')).not.toBeNull();
	});

	test('drops an edit with no mapping', async () => {
		await orchestrator.onPlatformEdit({
			platform: 'mattermost',
			externalId: 'ghost',
			externalChannelId: 'mm-chan',
			text: 'x',
			sender: { platform: 'mattermost', platformUserId: 'u1', email: 'a@example.com' },
		});
		expect(buzz.edits).toHaveLength(0);
	});

	test('mirrors a delete and a reaction against the mapped event', async () => {
		await store.recordMessage({ buzzEventId: 'event-1', platform: 'mattermost', externalId: 'post-1', externalChannelId: 'mm-chan' });
		await orchestrator.onPlatformReaction({
			platform: 'mattermost',
			externalId: 'post-1',
			externalChannelId: 'mm-chan',
			emoji: 'thumbsup',
			sender: { platform: 'mattermost', platformUserId: 'u1', email: 'a@example.com' },
		});
		await orchestrator.onPlatformDelete({
			platform: 'mattermost',
			externalId: 'post-1',
			externalChannelId: 'mm-chan',
			sender: { platform: 'mattermost', platformUserId: 'u1', email: 'a@example.com' },
		});
		expect(buzz.reactions[0]?.targetEventId).toBe('event-1');
		expect(buzz.reactions[0]?.emoji).toBe('thumbsup');
		expect(buzz.removes[0]?.targetEventId).toBe('event-1');
	});
});

describe('Buzz -> platforms fan-out', () => {
	let store: FakeMappingStore;
	let slack: FakePlatformGateway;
	let orchestrator: MirrorOrchestrator;

	beforeEach(() => {
		store = new FakeMappingStore();
		slack = new FakePlatformGateway();
		orchestrator = new MirrorOrchestrator(store, ['mattermost', 'slack'], new FakeBuzzGateway(), {
			slack: { post: slack.post.bind(slack), edit: slack.edit.bind(slack), remove: slack.remove.bind(slack), react: slack.react.bind(slack) },
		}, new FakeIdentity());
	});

	test('fans out a message to other platforms but never back to the origin platform', async () => {
		await store.recordChannel({ buzzChannelId: 'bc', platform: 'slack', externalChannelId: 'slack-chan' });
		await orchestrator.onBuzzMessage({
			buzzEventId: 'e1',
			buzzChannelId: 'bc',
			text: 'hi',
			origin: { platform: 'mattermost', externalId: 'post-1' },
			senderName: 'Alice',
		});
		expect(slack.posts.map((p) => p.target)).toEqual(['slack']);
	});

	test('fans out edit/delete/reaction to the mapped message on other platforms', async () => {
		await store.recordMessage({ buzzEventId: 'e1', platform: 'slack', externalId: 'slack-1', externalChannelId: 'slack-chan' });
		await orchestrator.onBuzzEdit({ targetEventId: 'e1', buzzChannelId: 'bc', text: 'edited', origin: { platform: 'mattermost', externalId: 'p' } });
		await orchestrator.onBuzzReaction({ targetEventId: 'e1', buzzChannelId: 'bc', emoji: 'tada', origin: { platform: 'mattermost', externalId: 'p' } });
		await orchestrator.onBuzzDelete({ targetEventId: 'e1', buzzChannelId: 'bc', origin: { platform: 'mattermost', externalId: 'p' } });
		expect(slack.edits[0]?.externalId).toBe('slack-1');
		expect(slack.reactions[0]?.emoji).toBe('tada');
		expect(slack.removes[0]?.externalId).toBe('slack-1');
	});
});

describe('echo suppression across the round trip', () => {
	test('an edit the mirror applies to a platform is not republished when it echoes back', async () => {
		const store = new FakeMappingStore();
		await store.recordMessage({ buzzEventId: 'e1', platform: 'mattermost', externalId: 'post-1', externalChannelId: 'mm-chan' });
		const buzz = new FakeBuzzGateway();
		const mattermost = new FakePlatformGateway();
		const orchestrator = new MirrorOrchestrator(store, ['mattermost'], buzz, {
			mattermost: { post: mattermost.post.bind(mattermost), edit: mattermost.edit.bind(mattermost), remove: mattermost.remove.bind(mattermost), react: mattermost.react.bind(mattermost) },
		}, new FakeIdentity());

		// A Buzz edit originating from slack fans out to mattermost...
		await orchestrator.onBuzzEdit({ targetEventId: 'e1', buzzChannelId: 'bc', text: 'edited', origin: { platform: 'slack', externalId: 's1' } });
		expect(mattermost.edits).toHaveLength(1);

		// ...mattermost emits the edit for the mirror's own apply; it must not bounce back to Buzz.
		await orchestrator.onPlatformEdit({
			platform: 'mattermost',
			externalId: 'post-1',
			externalChannelId: 'mm-chan',
			text: 'edited',
			sender: { platform: 'mattermost', platformUserId: 'u1', email: 'a@example.com' },
		});
		expect(buzz.edits).toHaveLength(0);
	});
});
