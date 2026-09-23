import type { Route } from './+types/docs';
import { DocsLayout } from 'fumadocs-ui/layouts/docs';
import {
  DocsBody,
  DocsPage,
  DocsTitle,
  MarkdownCopyButton,
  ViewOptionsPopover,
} from 'fumadocs-ui/layouts/docs/page';
import { docs, source } from '@/lib/source';
import { baseOptions } from '@/lib/layout.shared';
import { useFumadocsLoader } from 'fumadocs-core/source/client';
import { useMDXComponents } from '@/components/mdx';
import { use } from 'react';
import { appName, docsOrigin, getPageMarkdownUrl, gitConfig } from '@/lib/shared';
import sources from '../../content/docs/sources.json';

export async function loader({ params }: Route.LoaderArgs) {
  const slugs = params['*'].split('/').filter((v) => v.length > 0);
  const page = source.getPage(slugs);
  if (!page) throw new Response('Not found', { status: 404 });

  const top = page.path.split('/')[0].replace(/\.mdx$/, '');
  return {
    path: page.path,
    url: page.url,
    markdownUrl: getPageMarkdownUrl(page).url,
    sourceFile: (sources as Record<string, string>)[top] ?? 'DOCS.md',
    pageTree: await source.serializePageTree(source.getPageTree()),
  };
}

function Content({
  path,
  url,
  markdownUrl,
  sourceFile,
}: {
  path: string;
  url: string;
  markdownUrl: string;
  sourceFile: string;
}) {
  const page = docs.getPage(path);
  if (!page) throw new Error(`unknown page: ${path}`);

  const { toc } = use(page.load());
  const Mdx = page.body;
  const title = `${page.searchTitle ?? page.title} · ${appName}`;
  const canonical = `${docsOrigin}${url}/`;
  const subtitle = page.lead ?? page.description;

  return (
    <DocsPage toc={toc}>
      <title>{title}</title>
      <meta name="description" content={page.description} />
      <link rel="canonical" href={canonical} />
      <meta property="og:type" content="article" />
      <meta property="og:site_name" content={appName} />
      <meta property="og:title" content={title} />
      <meta property="og:description" content={page.description} />
      <meta property="og:url" content={canonical} />
      <meta property="og:image" content={`${docsOrigin}/og.png`} />
      <meta name="twitter:card" content="summary_large_image" />
      <meta name="twitter:title" content={title} />
      <meta name="twitter:description" content={page.description} />
      <meta name="twitter:image" content={`${docsOrigin}/og.png`} />
      <DocsTitle>{page.title}</DocsTitle>
      {subtitle ? <p className="-mt-4 text-lg text-fd-muted-foreground">{subtitle}</p> : null}
      <div
        className={`flex flex-row gap-2 items-center border-b pb-6 ${subtitle ? 'mt-2' : '-mt-4'}`}
      >
        <MarkdownCopyButton markdownUrl={markdownUrl} />
        <ViewOptionsPopover
          markdownUrl={markdownUrl}
          githubUrl={`https://github.com/${gitConfig.user}/${gitConfig.repo}/blob/${gitConfig.branch}/${sourceFile}`}
        />
      </div>
      <DocsBody>
        <Mdx components={useMDXComponents()} />
      </DocsBody>
    </DocsPage>
  );
}

export default function Page({ loaderData }: Route.ComponentProps) {
  const { pageTree, path, url, markdownUrl, sourceFile } = useFumadocsLoader(loaderData);

  return (
    <DocsLayout {...baseOptions()} tree={pageTree}>
      <Content path={path} url={url} markdownUrl={markdownUrl} sourceFile={sourceFile} />
    </DocsLayout>
  );
}
