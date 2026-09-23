export const site = {
	name: 'blueclaw',
	tagline: 'A self-hosted agent host that runs each person’s tool calls as their own POSIX user.',
	description:
		'blueclaw is a self-hosted Go daemon that runs an AI agent for the several people sharing one machine: each requester’s tool calls run as their own unprivileged POSIX user, side effects wait at an approval gate, and every step lands in a durable ledger.',
	origin: 'https://blueclaw.intern.kim',
	repository: { owner: 'yeomyeonggeori', name: 'blueclaw', branch: 'main' },
	color: { light: '#0369a1', dark: '#38bdf8' },
	gettingStarted: ['index', 'quickstart', 'architecture'],
	icons: {
		index: 'Compass',
		quickstart: 'Rocket',
		architecture: 'Layers',
		concepts: 'Shapes',
		boundaries: 'ShieldCheck',
		operations: 'Wrench',
		questions: 'MessageCircleQuestion',
	} as Record<string, string>,
	groupDescriptions: {} as Record<string, string>,
};
