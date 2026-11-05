import { useState } from 'react';
import type { TerminalItem } from '../../../types';

interface Props {
  item: TerminalItem;
}

export default function TerminalCard({ item }: Props) {
  const [expanded, setExpanded] = useState(true);
  const output = item.output.join('');
  const hasExit = item.exitCode !== undefined;
  const exitOk = item.exitCode === 0;

  return (
    <div style={card}>
      <div style={header} onClick={() => setExpanded(!expanded)}>
        <span style={{ marginRight: 6 }}>{expanded ? '▼' : '▶'}</span>
        <span style={{ marginRight: 6 }}>⬛</span>
        <span style={titleStyle}>{item.title || item.command || 'Terminal'}</span>
        {item.cwd && <span style={cwdStyle}>{item.cwd}</span>}
        {hasExit && (
          <span style={{ ...exitBadge, color: exitOk ? '#155724' : '#721c24', background: exitOk ? '#d4edda' : '#f8d7da' }}>
            exit {item.exitCode}
          </span>
        )}
      </div>
      {expanded && output && (
        <div style={outputBox}>
          <pre style={outputPre}>{atob(output)}</pre>
        </div>
      )}
    </div>
  );
}

function atob(s: string): string {
  try { return globalThis.atob(s); } catch { return s; }
}

const card: React.CSSProperties = {
  margin: '6px 0', border: '1px solid #dee2e6', borderRadius: 6,
  background: '#1e1e1e', overflow: 'hidden',
};

const header: React.CSSProperties = {
  display: 'flex', alignItems: 'center', gap: 4,
  padding: '6px 10px', cursor: 'pointer', fontSize: 12,
  color: '#ccc', userSelect: 'none',
};

const titleStyle: React.CSSProperties = { fontWeight: 600, fontFamily: '"SF Mono", Consolas, monospace' };

const cwdStyle: React.CSSProperties = {
  marginLeft: 'auto', fontSize: 10, color: '#888',
  fontFamily: '"SF Mono", Consolas, monospace',
};

const exitBadge: React.CSSProperties = {
  display: 'inline-block', padding: '1px 5px', borderRadius: 3,
  fontSize: 10, fontWeight: 600, marginLeft: 8,
};

const outputBox: React.CSSProperties = {
  padding: '6px 10px', maxHeight: 300, overflow: 'auto',
};

const outputPre: React.CSSProperties = {
  margin: 0, fontSize: 11, whiteSpace: 'pre-wrap', wordBreak: 'break-all',
  fontFamily: '"SF Mono", Consolas, monospace', color: '#d4d4d4',
};
