import { type BuzzRelayClient, createBuzzRelayClient } from "./relay-client.ts";

// Every send, edit, reaction and lookup used to open a websocket, authenticate,
// do one thing and close. On the device that is a TLS handshake and a NIP-42
// exchange per message, which is most of the two seconds a reply took to appear.
//
// The window is the length of a conversation, not of a request: a company is a
// few dozen keys, and an idle socket each costs less than reconnecting between
// one message and the next.
const idleMilliseconds = 15 * 60_000;

type PooledConnection = {
	client: BuzzRelayClient;
	connecting: Promise<void>;
	borrowers: number;
	idleTimer?: ReturnType<typeof setTimeout>;
};

const connections = new Map<string, PooledConnection>();

export async function withRelayAs<Result>(
	relayURL: string,
	userSecretHex: string,
	authTagJSON: string | undefined,
	work: (relay: BuzzRelayClient) => Promise<Result>,
): Promise<Result> {
	const key = `${relayURL}|${userSecretHex}|${authTagJSON ?? ""}`;
	const connection = borrow(key, relayURL, userSecretHex, authTagJSON);
	try {
		await connection.connecting;
		return await work(connection.client);
	} catch (error) {
		// A connection that has just failed may be the reason it failed, and a
		// pool that keeps it hands the same fault to the next caller.
		discard(key, connection);
		throw error;
	} finally {
		release(key, connection);
	}
}

function borrow(
	key: string,
	relayURL: string,
	userSecretHex: string,
	authTagJSON: string | undefined,
): PooledConnection {
	const existing = connections.get(key);
	if (existing) {
		if (existing.idleTimer) clearTimeout(existing.idleTimer);
		existing.idleTimer = undefined;
		existing.borrowers += 1;
		return existing;
	}
	const client = createBuzzRelayClient(relayURL, userSecretHex, authTagJSON);
	const connection: PooledConnection = { client, connecting: client.connect(), borrowers: 1 };
	connections.set(key, connection);
	return connection;
}

function release(key: string, connection: PooledConnection): void {
	connection.borrowers -= 1;
	if (connection.borrowers > 0 || connections.get(key) !== connection) return;
	connection.idleTimer = setTimeout(() => {
		if (connections.get(key) !== connection || connection.borrowers > 0) return;
		connections.delete(key);
		connection.client.disconnect();
	}, idleMilliseconds);
	connection.idleTimer.unref?.();
}

function discard(key: string, connection: PooledConnection): void {
	if (connections.get(key) !== connection) return;
	connections.delete(key);
	if (connection.idleTimer) clearTimeout(connection.idleTimer);
	connection.client.disconnect();
}

export function closeEveryPooledRelay(): void {
	for (const [key, connection] of connections) {
		connections.delete(key);
		if (connection.idleTimer) clearTimeout(connection.idleTimer);
		connection.client.disconnect();
	}
}
