import { llms, loader } from 'fumadocs-core/source';
import * as lucide from 'lucide-react';
import { createElement } from 'react';
import { frontmatterSchema } from 'fumadocs-mdx/config';
import { z } from 'zod';
import { defineDocs } from 'fumadocs-mdx/macro';
import { docsRoute } from './shared';

export const docs = defineDocs({
  dir: 'content/docs',
  docs: {
    async: true,
    schema: frontmatterSchema.extend({
      lead: z.string().optional(),
      searchTitle: z.string().optional(),
    }),
    postprocess: {
      includeProcessedMarkdown: true,
    },
  },
});

export const source = loader({
  source: docs.toFumadocsSource(),
  baseUrl: docsRoute,
  icon(name) {
    const icon = name && (lucide as Record<string, unknown>)[name];
    if (icon) return createElement(icon as React.ComponentType);
  },
});

export const docsLlms = llms(source, {
  renderPage: async (page) => `# ${page.data.title} (${page.url})

${await page.data.getText('processed')}`,
});
