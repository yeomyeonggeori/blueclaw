import { threadTagsOf, type BuzzEvent } from "./types.ts";

const STREAM_MESSAGE_KIND = 9;
const mostRepliesInAThread = 500;

export type QueryingRelay = {
	queryComplete: (filter: object) => Promise<{ events: BuzzEvent[]; complete: boolean }>;
};

export type ThreadDeletion = {
	relay: QueryingRelay;
	channelID: string;
	rootEventId: string;
	mayClearReplies: () => Promise<boolean>;
	deleteRoot: () => Promise<void>;
	deleteReply: (reply: BuzzEvent) => Promise<void>;
};

export class ThreadPartlyDeleted extends Error {
	readonly reason = "thread-partly-deleted";

	constructor(
		readonly remaining: number,
		cause?: unknown,
	) {
		super(`the message is gone and ${remaining} of its replies could not be taken back with it`, { cause });
		this.name = "ThreadPartlyDeleted";
	}
}

export async function repliesUnder(
	relay: QueryingRelay,
	channelID: string,
	rootEventId: string,
): Promise<{ replies: BuzzEvent[]; wholeThread: boolean }> {
	const { events, complete } = await relay.queryComplete({
		kinds: [STREAM_MESSAGE_KIND],
		"#h": [channelID],
		"#e": [rootEventId],
		limit: mostRepliesInAThread,
	});
	return {
		replies: events
			.filter((event) => event.id !== rootEventId && threadTagsOf(event).rootEventId === rootEventId)
			.sort((first, second) => first.created_at - second.created_at),
		wholeThread: complete && events.length < mostRepliesInAThread,
	};
}

export async function deleteThread(deletion: ThreadDeletion): Promise<void> {
	await deletion.deleteRoot();
	const { replies, wholeThread } = await repliesUnder(deletion.relay, deletion.channelID, deletion.rootEventId);
	if (!(await deletion.mayClearReplies())) {
		if (replies.length > 0) throw new ThreadPartlyDeleted(replies.length);
		return;
	}
	let remaining = wholeThread ? 0 : 1;
	let firstRefusal: unknown;
	for (const reply of replies) {
		try {
			await deletion.deleteReply(reply);
		} catch (refusal) {
			firstRefusal = firstRefusal ?? refusal;
			remaining += 1;
		}
	}
	if (remaining > 0) throw new ThreadPartlyDeleted(remaining, firstRefusal);
}
