import { useEffect, useRef, useState } from 'react';
import mermaid from 'mermaid';

// Diagrams render once per unique source, so the id only needs to be unique
// within the page. A counter keeps the ids stable across re-renders and avoids
// the collision mermaid reports when two diagrams share an id.
let nextId = 0;

interface Props {
  chart: string;
}

const containerStyle: React.CSSProperties = {
  margin: '16px 0',
  padding: '16px',
  background: '#fff',
  border: '1px solid #d0d7de',
  borderRadius: 8,
  overflowX: 'auto' as const,
};

const errorStyle: React.CSSProperties = {
  ...containerStyle,
  borderColor: '#f5c2c7',
  background: '#fff5f5',
  color: '#b42318',
  fontSize: 13,
};

const codeStyle: React.CSSProperties = {
  margin: '8px 0 0',
  padding: 12,
  background: '#f6f8fa',
  borderRadius: 6,
  fontSize: 12,
  color: '#1a1a2e',
  overflowX: 'auto' as const,
  fontFamily: '"SF Mono", "Fira Code", Consolas, "Liberation Mono", Menlo, monospace',
};

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
      <div style={errorStyle}>
        <div>Diagram could not be rendered: {error}</div>
        <pre style={codeStyle}>{chart}</pre>
      </div>
    );
  }

  if (!svg) {
    return <div style={{ ...containerStyle, color: '#999' }}>Rendering diagram…</div>;
  }

  return (
    <div
      style={containerStyle}
      // The SVG comes from mermaid's own renderer on the same page, not from
      // user input, so injecting it is safe.
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
}
