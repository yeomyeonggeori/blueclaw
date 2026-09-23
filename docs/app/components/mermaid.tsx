import { useTheme } from 'next-themes';
import { useEffect, useId, useRef, useState } from 'react';

/**
 * A ```mermaid fence in DOCS.md, which GitHub renders on its own and the
 * generator turns into this component for the site.
 */
export function Mermaid({ chart }: { chart: string }) {
  const container = useRef<HTMLDivElement>(null);
  const [svg, setSvg] = useState('');
  const { resolvedTheme } = useTheme();
  const id = 'mermaid' + useId().replace(/[^a-zA-Z0-9]/g, '');

  useEffect(() => {
    let current = true;
    void (async () => {
      const { default: mermaid } = await import('mermaid');
      mermaid.initialize({
        startOnLoad: false,
        securityLevel: 'loose',
        fontFamily: 'inherit',
        theme: resolvedTheme === 'dark' ? 'dark' : 'neutral',
      });
      try {
        const { svg: drawn } = await mermaid.render(id, chart.trim());
        if (current) setSvg(drawn);
      } catch (failure) {
        console.error('mermaid', failure);
      }
    })();
    return () => {
      current = false;
    };
  }, [chart, id, resolvedTheme]);

  return (
    <div
      ref={container}
      className="my-6 flex justify-center overflow-x-auto rounded-lg border bg-fd-card p-4 [&_svg]:max-w-full"
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
}
