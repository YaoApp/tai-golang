import { useState } from 'react';
import type { ToolCallItem } from '../../../types';

interface Props {
  item: ToolCallItem;
}

export default function ToolCallCard({ item }: Props) {
  const [expanded, setExpanded] = useState(false);
  const output = item.output.join('');

  return (
    <div style={card}>
      <div style={header} onClick={() => setExpanded(!expanded)}>
        <span style={{ marginRight: 6 }}>{expanded ? '▼' : '▶'}</span>
        <span style={{ marginRight: 8 }}>🔧</span>
        <span style={nameStyle}>{item.name || 'tool'}</span>
        {item.kind && <span style={kindBadge}>{item.kind}</span>}
        <span style={statusBadge(item.status)}>{item.status}</span>
        {item.locations && item.locations.length > 0 && (
          <span style={locationStyle}>{item.locations[0].path}</span>
        )}
      </div>
      {expanded && output && (
        <div style={outputBox}>
          <pre style={outputPre}>{output}</pre>
        </div>
      )}
    </div>
  );
}

const card: React.CSSProperties = {
  margin: '6px 0', border: '1px solid #e0e0e0', borderRadius: 6,
  background: '#fafafa', overflow: 'hidden',
};

const header: React.CSSProperties = {
  display: 'flex', alignItems: 'center', gap: 4,
  padding: '6px 10px', cursor: 'pointer', fontSize: 12,
  userSelect: 'none',
};

const nameStyle: React.CSSProperties = { fontWeight: 600, color: '#333' };

const kindBadge: React.CSSProperties = {
  display: 'inline-block', padding: '1px 5px', borderRadius: 3,
  background: '#e8f0fe', color: '#0066cc', fontSize: 10, fontWeight: 600,
};

function statusBadge(status: string): React.CSSProperties {
  const colors: Record<string, { bg: string; fg: string }> = {
    pending: { bg: '#fff3cd', fg: '#856404' },
    in_progress: { bg: '#cce5ff', fg: '#004085' },
    completed: { bg: '#d4edda', fg: '#155724' },
    failed: { bg: '#f8d7da', fg: '#721c24' },
    cancelled: { bg: '#e2e3e5', fg: '#383d41' },
  };
  const c = colors[status] || colors.pending;
  return {
    display: 'inline-block', padding: '1px 5px', borderRadius: 3,
    background: c.bg, color: c.fg, fontSize: 10, fontWeight: 600, marginLeft: 4,
  };
}

const locationStyle: React.CSSProperties = {
  marginLeft: 'auto', fontSize: 11, color: '#888',
  fontFamily: '"SF Mono", Consolas, monospace',
  overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
  maxWidth: 200,
};

const outputBox: React.CSSProperties = {
  borderTop: '1px solid #e0e0e0', padding: '6px 10px',
  maxHeight: 200, overflow: 'auto', background: '#fff',
};

const outputPre: React.CSSProperties = {
  margin: 0, fontSize: 11, whiteSpace: 'pre-wrap', wordBreak: 'break-all',
  fontFamily: '"SF Mono", Consolas, monospace', color: '#555',
};
