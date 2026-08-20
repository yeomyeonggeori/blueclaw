import type {
	MirrorBuzzDelete,
	MirrorBuzzEdit,
	MirrorBuzzInbound,
	MirrorBuzzReaction,
	MirrorPlatformDelete,
	MirrorPlatformEdit,
	MirrorPlatformInbound,
	MirrorPlatformReaction,
} from './inbound.ts';

export const MIRROR_SUMMARY_INTERVAL_MILLISECONDS = 5 * 60 * 1000;

export type MirrorSubject = {
	post?: string;
	channel?: string;
	person?: string;
};

export function subjectOfPlatformEvent(
	inbound: MirrorPlatformInbound | MirrorPlatformEdit | MirrorPlatformDelete | MirrorPlatformReaction
): MirrorSubject {
	return { post: inbound.externalId, channel: inbound.externalChannelId, person: inbound.senderEmail };
}

export function subjectOfBuzzEvent(
	inbound: MirrorBuzzInbound | MirrorBuzzEdit | MirrorBuzzDelete | MirrorBuzzReaction
): MirrorSubject {
	const post = 'buzzEventId' in inbound ? inbound.buzzEventId : inbound.targetEventId;
	return { post, channel: inbound.buzzChannelId, person: inbound.senderEmail };
}

// A Bun single-file build resolves stack frames to bundle offsets, so printing
// the Error prints nine lines of somebody else's source and identifies nothing.
// The message is the only part that says what the relay refused.
export function describeMirrorFailure(subject: MirrorSubject, failure: unknown): string {
	const named = Object.entries(subject)
		.filter(([, value]) => typeof value === 'string' && value.length > 0)
		.map(([name, value]) => `${name}=${value}`);
	const reason = failure instanceof Error ? failure.message : String(failure);
	return [...named, `reason=${reason}`].join(' ');
}

// Silence has to mean nothing happened, which it cannot while the mirror logs
// only failures: a working mirror and a dead one leave the same empty journal.
export class MirrorTally {
	private readonly successes = new Map<string, number>();
	private readonly failures = new Map<string, number>();

	succeeded(context: string): void {
		count(this.successes, context);
	}

	failed(context: string): void {
		count(this.failures, context);
	}

	take(): string {
		const parts = [...describeCounts('ok', this.successes), ...describeCounts('failed', this.failures)];
		this.successes.clear();
		this.failures.clear();
		return parts.join(', ');
	}
}

function count(counts: Map<string, number>, context: string): void {
	counts.set(context, (counts.get(context) ?? 0) + 1);
}

function describeCounts(outcome: string, counts: Map<string, number>): string[] {
	return [...counts.entries()]
		.sort(([left], [right]) => left.localeCompare(right))
		.map(([context, total]) => `${context} ${outcome} ${total}`);
}
