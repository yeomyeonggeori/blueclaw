export type MessageMapping = {
	buzzEventId: string;
	platform: string;
	externalId: string;
	externalChannelId: string;
};

export type ChannelMapping = {
	buzzChannelId: string;
	platform: string;
	externalChannelId: string;
};

type FetchImpl = typeof fetch;

export class MappingStore {
	private readonly baseURL: string;
	private readonly fetchImpl: FetchImpl;

	constructor(baseURL: string, fetchImpl: FetchImpl = fetch) {
		this.baseURL = baseURL.endsWith('/') ? baseURL.slice(0, -1) : baseURL;
		this.fetchImpl = fetchImpl;
	}

	async recordMessage(mapping: MessageMapping): Promise<void> {
		await this.post('/bridge/api/message', mapping);
	}

	async messageByExternal(platform: string, externalId: string): Promise<MessageMapping | null> {
		return this.lookupMessage({ platform, externalId });
	}

	async messageByEvent(buzzEventId: string, platform: string): Promise<MessageMapping | null> {
		return this.lookupMessage({ platform, buzzEventId });
	}

	async forgetMessage(platform: string, externalId: string): Promise<void> {
		await this.remove('/bridge/api/message', { platform, externalId });
	}

	async recordChannel(mapping: ChannelMapping): Promise<void> {
		await this.post('/bridge/api/channel', mapping);
	}

	async secretForEmail(email: string): Promise<string> {
		const document = await this.get<{ secretHex: string }>('/bridge/api/identity', { email });
		return document.secretHex;
	}

	async buzzChannelForExternal(platform: string, externalChannelId: string): Promise<string> {
		const document = await this.postJSON<{ buzzChannelId: string }>('/bridge/api/channel/resolve', {
			platform,
			externalChannelId,
		});
		return document.buzzChannelId;
	}

	async channelByExternal(platform: string, externalChannelId: string): Promise<ChannelMapping | null> {
		return this.lookupChannel({ platform, externalChannelId });
	}

	async channelByBuzz(buzzChannelId: string, platform: string): Promise<ChannelMapping | null> {
		return this.lookupChannel({ platform, buzzChannelId });
	}

	private async lookupMessage(params: Record<string, string>): Promise<MessageMapping | null> {
		const document = await this.get<{ found: boolean; mapping: MessageMapping }>('/bridge/api/message', params);
		return document.found ? document.mapping : null;
	}

	private async lookupChannel(params: Record<string, string>): Promise<ChannelMapping | null> {
		const document = await this.get<{ found: boolean; mapping: ChannelMapping }>('/bridge/api/channel', params);
		return document.found ? document.mapping : null;
	}

	private async remove(path: string, params: Record<string, string>): Promise<void> {
		const query = new URLSearchParams(params).toString();
		const response = await this.fetchImpl(`${this.baseURL}${path}?${query}`, { method: 'DELETE' });
		if (!response.ok) {
			throw new Error(`bridge map ${path} returned ${response.status}`);
		}
	}

	private async post(path: string, body: unknown): Promise<void> {
		const response = await this.fetchImpl(`${this.baseURL}${path}`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(body),
		});
		if (!response.ok) {
			throw new Error(`bridge map ${path} returned ${response.status}`);
		}
	}

	private async postJSON<T>(path: string, body: unknown): Promise<T> {
		const response = await this.fetchImpl(`${this.baseURL}${path}`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(body),
		});
		if (!response.ok) {
			throw new Error(`bridge map ${path} returned ${response.status}`);
		}
		return (await response.json()) as T;
	}

	private async get<T>(path: string, params: Record<string, string>): Promise<T> {
		const query = new URLSearchParams(params).toString();
		const response = await this.fetchImpl(`${this.baseURL}${path}?${query}`);
		if (!response.ok) {
			throw new Error(`bridge map ${path} returned ${response.status}`);
		}
		return (await response.json()) as T;
	}
}
