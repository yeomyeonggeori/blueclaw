import { Renderer, type ContainerNode, type TextNode } from '@takumi-rs/core';
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { site } from '../site';

const statics = join(import.meta.dirname, '..', 'public');
const ink = '#17181c';

const text = (value: string, fontSize: number, color: string, fontWeight = 600): TextNode => ({
	type: 'text',
	text: value,
	style: { fontFamily: 'Cascadia Code', fontSize, fontWeight, color, textAlign: 'center', maxWidth: 1000 },
});

const card: ContainerNode = {
	type: 'container',
	style: {
		width: 1200,
		height: 630,
		display: 'flex',
		flexDirection: 'column',
		alignItems: 'center',
		justifyContent: 'center',
		gap: 36,
		backgroundColor: '#ffffff',
	},
	children: [
		text(site.name, 112, site.color.light, 700),
		text(site.tagline, 40, ink),
		text(new URL(site.origin).host, 26, '#8a8d97', 400),
	],
};

const png = await new Renderer().render(card, {
	width: 1200,
	height: 630,
	fonts: [readFileSync(join(statics, 'fonts', 'CascadiaCode.woff2')), readFileSync(join(statics, 'fonts', 'CascadiaCodeItalic.woff2'))],
});
writeFileSync(join(statics, 'og.png'), png);
console.log(`wrote og.png, ${png.length} bytes`);
