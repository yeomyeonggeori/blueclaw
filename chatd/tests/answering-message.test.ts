import { expect, test } from "bun:test";

import { parseReplySendRequest } from "../src/outbound-parse";

// The thread says where a reply belongs. The answered message says what it is a
// reply to, and without it somebody who wrote deep in a thread finds the answer
// at the top of that thread instead of under what they wrote.
test("a reply carries the message it answers", () => {
	const request = parseReplySendRequest({
		replyTargetID: "buzz:channel-1:root-1",
		answeringMessageID: "message-9",
		message: "answered",
	});

	expect(request.answeringMessageID).toBe("message-9");
});

test("a reply that answers no particular message still sends", () => {
	const request = parseReplySendRequest({
		replyTargetID: "buzz:channel-1:root-1",
		message: "announced",
	});

	expect(request.answeringMessageID).toBeUndefined();
});
