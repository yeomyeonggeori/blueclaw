import { expect, test } from "bun:test";

import { loadConfiguration } from "../src/configuration";

test("chatd binds where it is told, so a caller off this machine can reach it", () => {
	const configuration = loadConfiguration({
		CHATD_BOT_USER_NAME: "internkim",
		CHATD_MATTERMOST_BASE_URL: "http://127.0.0.1:8065",
		CHATD_MATTERMOST_BOT_TOKEN: "bot",
		CHATD_BLUECLAW_INGRESS_URL: "http://127.0.0.1:8080/connectors/mattermost/events",
		CHATD_LISTEN_HOSTNAME: "172.31.0.1",
	});

	expect(configuration.listenHostname).toBe("172.31.0.1");
});

test("chatd keeps to this machine when nobody says otherwise", () => {
	const configuration = loadConfiguration({
		CHATD_BOT_USER_NAME: "internkim",
		CHATD_MATTERMOST_BASE_URL: "http://127.0.0.1:8065",
		CHATD_MATTERMOST_BOT_TOKEN: "bot",
		CHATD_BLUECLAW_INGRESS_URL: "http://127.0.0.1:8080/connectors/mattermost/events",
	});

	expect(configuration.listenHostname).toBe("127.0.0.1");
});
