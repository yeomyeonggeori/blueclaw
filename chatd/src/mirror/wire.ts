import { createBuzzGateway } from './buzz-publisher.ts';
import {
	MIRROR_SUMMARY_INTERVAL_MILLISECONDS,
	MirrorTally,
	describeMirrorFailure,
	subjectOfBuzzEvent,
	subjectOfPlatformEvent,
	type MirrorSubject,
} from './failure.ts';
import type { BuzzMirrorSink, PlatformMirrorSink } from './inbound.ts';
import { MappingStore } from './mapping-store.ts';
import { createMattermostGateway } from './mattermost-puppet.ts';
import { MirrorOrchestrator, type PlatformGateway } from './orchestrator.ts';

export type MirrorWiring = {
	mattermost: PlatformMirrorSink;
	buzz: BuzzMirrorSink;
};

// Assembles the star-topology mirror: an admind-backed mapping store, a per-user
// Buzz gateway, and per-user platform gateways, driven by the orchestrator. The
// returned sinks are handed to each adapter's inbound tap.
export function createMirror(options: {
	admindBaseURL: string;
	connectedPlatforms: string[];
	buzz: { relayURL: string; authTagJSON?: string };
	mattermost?: { baseURL: string; adminToken: string };
	onError?: (context: string, detail: unknown) => void;
	onSummary?: (summary: string) => void;
}): MirrorWiring {
	const mapping = new MappingStore(options.admindBaseURL);
	const platforms: Record<string, PlatformGateway> = {};
	if (options.mattermost) {
		platforms.mattermost = createMattermostGateway(options.mattermost);
	}
	const orchestrator = new MirrorOrchestrator(
		mapping,
		options.connectedPlatforms,
		createBuzzGateway(options.buzz.relayURL, options.buzz.authTagJSON),
		platforms,
		mapping,
	);
	const tally = new MirrorTally();
	const run = (context: string, subject: MirrorSubject, work: Promise<void>): void => {
		void work.then(
			() => tally.succeeded(context),
			(error) => {
				tally.failed(context);
				options.onError?.(`${context} failed`, describeMirrorFailure(subject, error));
			},
		);
	};
	const skip = (context: string, detail: unknown): void => options.onError?.(context, detail);
	if (options.onSummary) {
		const report = options.onSummary;
		setInterval(() => {
			const summary = tally.take();
			if (summary) report(summary);
		}, MIRROR_SUMMARY_INTERVAL_MILLISECONDS).unref?.();
	}

	return {
		mattermost: {
			message(inbound) {
				if (!inbound.senderEmail) return skip('mattermost message skipped: no linked email', inbound.externalId);
				run('mattermost -> buzz message', subjectOfPlatformEvent(inbound), orchestrator.onPlatformMessage({
					platform: 'mattermost',
					externalId: inbound.externalId,
					externalChannelId: inbound.externalChannelId,
					text: inbound.text,
					sender: { platform: 'mattermost', platformUserId: inbound.senderPlatformUserId, email: inbound.senderEmail },
					replyToExternalId: inbound.replyToExternalId,
				}));
			},
			edit(inbound) {
				if (!inbound.senderEmail) return skip('mattermost edit skipped: no linked email', inbound.externalId);
				run('mattermost -> buzz edit', subjectOfPlatformEvent(inbound), orchestrator.onPlatformEdit({
					platform: 'mattermost',
					externalId: inbound.externalId,
					externalChannelId: inbound.externalChannelId,
					text: inbound.text,
					sender: { platform: 'mattermost', platformUserId: inbound.senderPlatformUserId, email: inbound.senderEmail },
				}));
			},
			remove(inbound) {
				if (!inbound.senderEmail) return skip('mattermost delete skipped: no linked email', inbound.externalId);
				run('mattermost -> buzz delete', subjectOfPlatformEvent(inbound), orchestrator.onPlatformDelete({
					platform: 'mattermost',
					externalId: inbound.externalId,
					externalChannelId: inbound.externalChannelId,
					sender: { platform: 'mattermost', platformUserId: inbound.senderPlatformUserId, email: inbound.senderEmail },
				}));
			},
			react(inbound) {
				if (!inbound.senderEmail) return skip('mattermost reaction skipped: no linked email', inbound.externalId);
				run('mattermost -> buzz reaction', subjectOfPlatformEvent(inbound), orchestrator.onPlatformReaction({
					platform: 'mattermost',
					externalId: inbound.externalId,
					externalChannelId: inbound.externalChannelId,
					emoji: inbound.emoji,
					sender: { platform: 'mattermost', platformUserId: inbound.senderPlatformUserId, email: inbound.senderEmail },
				}));
			},
		},
		buzz: {
			message(inbound) {
				run('buzz -> platforms message', subjectOfBuzzEvent(inbound), orchestrator.onBuzzMessage(inbound));
			},
			edit(inbound) {
				run('buzz -> platforms edit', subjectOfBuzzEvent(inbound), orchestrator.onBuzzEdit(inbound));
			},
			remove(inbound) {
				run('buzz -> platforms delete', subjectOfBuzzEvent(inbound), orchestrator.onBuzzDelete(inbound));
			},
			react(inbound) {
				run('buzz -> platforms reaction', subjectOfBuzzEvent(inbound), orchestrator.onBuzzReaction(inbound));
			},
		},
	};
}
