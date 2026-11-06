import { useEffect, useRef, useState } from 'react';
import mermaid from 'mermaid';

// Diagrams render once per unique source, so the id only needs to be unique
// within the page. A counter keeps the ids stable across re-renders and avoids
// the collision mermaid reports when two diagrams share an id.
let nextId = 0;

interface Props {
  chart: string;
}

export default function MermaidDiagram({ chart }: Props) {
  const [svg, setSvg] = useState('');
  const [error, setError] = useState('');
  const idRef = useRef(`mermaid-${nextId++}`);

  useEffect(() => {
    let cancelled = false;
    setSvg('');
    setError('');

    mermaid
      .render(idRef.current, chart)
      .then(({ svg: rendered }) => {
        if (!cancelled) setSvg(rendered);
      })
      .catch((err: unknown) => {
        // A syntax error must not blank the page: show the source instead so
        // the reader can still use the diagram's text.
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err));
        }
      });

    return () => {
      cancelled = true;
    };
  }, [chart]);

  if (error) {
    return (
      <div className="diagram diagram--error">
        <div>Diagram could not be rendered: {error}</div>
        <pre className="code-block">{chart}</pre>
      </div>
    );
  }

  if (!svg) {
    return <div className="diagram faint">Rendering diagram…</div>;
  }

  return (
    <div
      className="diagram"
      // The SVG comes from mermaid's own renderer on the same page, not from
      // user input, so injecting it is safe.
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
}
