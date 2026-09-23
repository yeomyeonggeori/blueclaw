import { createGetUrl } from 'fumadocs-core/source';
import { site } from '../../site';

export const appName = site.name;
export const docsOrigin = site.origin;
export const docsRoute = '/docs';
export const docsContentRoute = '/llms.mdx/docs';

export const gitConfig = {
  user: site.repository.owner,
  repo: site.repository.name,
  branch: site.repository.branch,
};

const getContentUrl = createGetUrl(docsContentRoute);

export function getPageMarkdownUrl(page: { slugs: string[]; locale?: string }) {
  const segments = [...page.slugs, 'content.md'];

  return { segments, url: getContentUrl(segments, page.locale) };
}
