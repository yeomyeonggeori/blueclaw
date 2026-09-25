import { copyFileSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { site } from '../site';

const docsRoute = '/docs';
const repository = join(import.meta.dirname, '..', '..');
const content = join(import.meta.dirname, '..', 'content', 'docs');
const assets = join(import.meta.dirname, '..', 'public');
const app = join(import.meta.dirname, '..', 'app');

const slugFor: Record<string, string> = { 'Q&A': 'questions' };

const slugOf = (title: string) =>
	slugFor[title] ??
	title
		.toLowerCase()
		.replace(/[^a-z0-9]+/g, '-')
		.replace(/^-|-$/g, '');

const cleanTitle = (heading: string) => heading.replace(/^#+ /, '').replace(/`/g, '');

const githubAnchor = (title: string) =>
	title
		.toLowerCase()
		.replace(/[^a-z0-9 _-]/g, '')
		.trim()
		.replace(/ +/g, '-');

function asFileTree(block: string) {
	const rows = block
		.split('\n')
		.filter((line) => line.trim())
		.map((line) => {
			const connector = line.search(/[├└]── /);
			const depth = connector < 0 ? 0 : line.slice(0, connector).length / 4 + 1;
			const name = connector < 0 ? line.trim() : line.slice(connector + 4).trim();
			return { depth, name };
		});
	const render = (start: number, depth: number): [string, number] => {
		let index = start;
		let out = '';
		while (index < rows.length && rows[index].depth === depth) {
			const { name } = rows[index];
			const pad = '\t'.repeat(depth + 1);
			index += 1;
			if (!name.endsWith('/')) {
				out += `${pad}<File name="${name}" />\n`;
				continue;
			}
			const [children, next] = render(index, depth + 1);
			index = next;
			out += `${pad}<Folder name="${name.slice(0, -1)}" defaultOpen>\n${children}${pad}</Folder>\n`;
		}
		return [out, index];
	};
	return `<Files>\n${render(0, 0)[0]}</Files>`;
}

function asCallout(kind: string, block: string) {
	const types: Record<string, string> = { NOTE: 'info', TIP: 'idea', IMPORTANT: 'info', WARNING: 'warn', CAUTION: 'error' };
	const body = block
		.split('\n')
		.map((line) => line.replace(/^> ?/, ''))
		.join('\n')
		.trim();
	return `<Callout type="${types[kind]}" title="${kind}">\n${body}\n</Callout>\n`;
}

function asMdx(markdown: string, shift: number) {
	let inFence = false;
	const lines = markdown.split('\n').map((line) => {
		if (line.startsWith('```')) {
			inFence = !inFence;
			return line;
		}
		if (inFence) return line;
		const heading = /^(#{1,6}) /.exec(line);
		if (!heading) return line.replace(/\{/g, '\\{').replace(/</g, (match, offset: number) => (/^<\/?[A-Za-z]/.test(line.slice(offset)) ? match : '&lt;'));
		return '#'.repeat(Math.max(2, heading[1].length - shift)) + line.slice(heading[1].length);
	});
	return lines
		.join('\n')
		.replace(/```tree\n([\s\S]*?)```/g, (_, block: string) => asFileTree(block))
		.replace(/```mermaid\n([\s\S]*?)```/g, (_, chart: string) => `<Mermaid chart={\`${chart.trim().replace(/`/g, '\\`')}\`} />`)
		.replace(/^> \[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\n((?:>.*\n?)*)/gm, (_, kind: string, block: string) => asCallout(kind, block))
		.replace(/<(https?:\/\/[^>\s]+)>/g, '[$1]($1)');
}

type Frontmatter = { title: string; description?: string; icon?: string; lead?: string };

const frontmatter = ({ title, description, icon, lead }: Frontmatter) =>
	[
		'---',
		`title: ${JSON.stringify(title)}`,
		...(description ? [`description: ${JSON.stringify(description)}`] : []),
		...(lead ? [`lead: ${JSON.stringify(lead)}`] : []),
		...(icon ? [`icon: ${icon}`] : []),
		'---',
		'',
	].join('\n');

const plainText = (markdown: string) =>
	markdown
		.replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
		.replace(/<(https?:\/\/[^>\s]+)>/g, '$1')
		.replace(/[`*_]/g, '')
		.replace(/\s+/g, ' ')
		.trim();

function firstParagraph(markdown: string) {
	let inFence = false;
	const paragraph: string[] = [];
	for (const line of markdown.split('\n')) {
		if (line.startsWith('```')) {
			inFence = !inFence;
			continue;
		}
		if (inFence) continue;
		const isProse = line.trim() && !/^(#|>|\||-|\d+\.|<)/.test(line);
		if (isProse) paragraph.push(line);
		else if (paragraph.length) break;
	}
	const text = plainText(paragraph.join(' ')).replace(/:$/, '.');
	if (text.length <= 160) return text;
	return text.slice(0, text.lastIndexOf(' ', 157)) + '…';
}

const written: string[] = [];
const titles = new Map<string, string>();

function takeLead(markdown: string) {
	const end = markdown.indexOf('\n\n');
	if (end < 0) return { lead: plainText(markdown), rest: '' };
	return { lead: plainText(markdown.slice(0, end)), rest: markdown.slice(end).trim() };
}

function writePage(path: string, title: string, text: string, options: { description?: string; shift?: number; lead?: boolean } = {}) {
	const { description, shift = 1, lead } = options;
	mkdirSync(join(content, path, '..'), { recursive: true });
	const page = asMdx(text, shift).trim();
	const { lead: subtitle, rest: body } = lead ? takeLead(page) : { lead: undefined, rest: page };
	writeFileSync(
		join(content, `${path}.mdx`),
		frontmatter({ title, description: description ?? subtitle ?? firstParagraph(body), icon: site.icons[path], lead: subtitle }) + body + '\n'
	);
	written.push(path);
	titles.set(path, title);
}

function linkAcrossPages() {
	const pageOf = new Map<string, string>();
	for (const path of written) {
		pageOf.set(path.split('/').pop()!, path);
		const anchor = githubAnchor(titles.get(path) ?? '');
		if (anchor && !pageOf.has(anchor)) pageOf.set(anchor, path);
	}
	for (const path of written) {
		const file = join(content, `${path}.mdx`);
		const before = readFileSync(file, 'utf8');
		const after = before.replace(/\]\(#([a-z0-9-]+)\)/g, (whole, anchor: string) => {
			const target = pageOf.get(anchor);
			if (!target || target === path) return whole;
			return `](${docsRoute}/${target === 'index' ? '' : target})`;
		});
		if (after !== before) writeFileSync(file, after);
	}
}

function splitOn(markdown: string, level: number) {
	const marker = '#'.repeat(level) + ' ';
	const parts: string[][] = [[]];
	let inFence = false;
	for (const line of markdown.split('\n')) {
		if (line.startsWith('```')) inFence = !inFence;
		if (!inFence && line.startsWith(marker)) parts.push([]);
		parts[parts.length - 1].push(line);
	}
	const [intro, ...chunks] = parts.map((lines) => lines.join('\n'));
	return {
		intro,
		chunks: chunks.map((chunk) => {
			const [heading, ...rest] = chunk.split('\n');
			return { title: cleanTitle(heading), text: rest.join('\n') };
		}),
	};
}

function writeGroup(slug: string, title: string, text: string) {
	const { intro, chunks: pages } = splitOn(text, 2);
	mkdirSync(join(content, slug), { recursive: true });
	const rows = pages.map((page) => {
		const body = asMdx(page.text, 1).trim();
		const lead = plainText(body.slice(0, Math.max(body.indexOf('\n\n'), 0) || body.length));
		return `| [${page.title}](${docsRoute}/${slug}/${slugOf(page.title)}) | ${lead} |`;
	});
	const table = ['| page | what it covers |', '| --- | --- |', ...rows].join('\n');
	const opening = intro.trim() ? `${asMdx(intro, 1).trim()}\n\n` : '';
	writeFileSync(
		join(content, slug, 'index.mdx'),
		frontmatter({ title, description: site.groupDescriptions[slug] ?? firstParagraph(intro), icon: site.icons[slug] }) + opening + table + '\n'
	);
	written.push(join(slug, 'index'));
	titles.set(join(slug, 'index'), title);
	for (const page of pages) writePage(join(slug, slugOf(page.title)), page.title, page.text, { lead: true });
	writeFileSync(
		join(content, slug, 'meta.json'),
		JSON.stringify({ title, ...(site.icons[slug] ? { icon: site.icons[slug] } : {}), pages: pages.map((page) => slugOf(page.title)) }, null, 2) + '\n'
	);
}

function writeTheme() {
	writeFileSync(
		join(app, 'theme.css'),
		`:root {\n  --color-fd-primary: ${site.color.light};\n  --color-fd-primary-foreground: #ffffff;\n}\n.dark {\n  --color-fd-primary: ${site.color.dark};\n  --color-fd-primary-foreground: #0b1020;\n}\n`
	);
	const logo = join(repository, 'assets', 'blueclaw.logo.svg');
	copyFileSync(logo, join(assets, 'logo.svg'));
	copyFileSync(logo, join(assets, 'favicon.svg'));
}

function writeCrawlerFiles() {
	const urls = written.map((path) => `${site.origin}${docsRoute}${path === 'index' ? '' : '/' + path.replace(/\/index$/, '')}/`);
	const entries = urls.map((url) => `  <url><loc>${url}</loc></url>`).join('\n');
	writeFileSync(join(assets, 'sitemap.xml'), `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${entries}\n</urlset>\n`);
	writeFileSync(join(assets, 'robots.txt'), `User-agent: *\nAllow: /\n\nSitemap: ${site.origin}/sitemap.xml\n`);
	writeFileSync(join(assets, '_redirects'), `/ ${docsRoute} 302\n`);
}

rmSync(content, { recursive: true, force: true });
mkdirSync(content, { recursive: true });

const reference = readFileSync(join(repository, 'DOCS.md'), 'utf8');
const [overview, ...sections] = splitOn(reference, 1).chunks;
writePage('index', overview.title, overview.text, { description: site.description });

const gettingStarted: string[] = ['index'];
const lookup: string[] = [];
const rest: string[] = [];
for (const section of sections) {
	const slug = slugOf(section.title);
	const { intro, chunks } = splitOn(section.text, 2);
	if (chunks.length === 0) writePage(slug, section.title, intro, { description: site.groupDescriptions[slug] });
	else writeGroup(slug, section.title, section.text);
	if (slug === 'index') continue;
	if (site.gettingStarted.includes(slug)) gettingStarted.push(slug);
	else if (chunks.length > 0) lookup.push(slug);
	else rest.push(slug);
}

const sidebar = ['---Getting started---', ...gettingStarted, '---Reference---', ...lookup, ...(rest.length ? ['---More---', ...rest] : [])];
linkAcrossPages();
writeFileSync(join(content, 'meta.json'), JSON.stringify({ title: site.name, pages: sidebar }, null, 2) + '\n');
writeFileSync(join(content, 'sources.json'), JSON.stringify(Object.fromEntries(sidebar.filter((entry) => !entry.startsWith('---')).map((slug) => [slug, 'DOCS.md'])), null, 2) + '\n');
writeTheme();
writeCrawlerFiles();
console.log(`wrote ${written.length} pages: ${sidebar.filter((entry) => !entry.startsWith('---')).join(', ')}`);
