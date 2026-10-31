import type { SessionInfo } from '../../../api';

interface Props {
  sessions: SessionInfo[];
  selected: string | null;
  onSelect: (id: string) => void;
}

export default function SessionList({ sessions, selected, onSelect }: Props) {
  return (
    <div style={{ borderRight: '1px solid #ddd', width: 240, paddingRight: 12 }}>
      <h3 style={{ margin: '0 0 12px' }}>Sessions</h3>
      {sessions.length === 0 && (
        <p style={{ color: '#999', fontSize: 13 }}>No active sessions</p>
      )}
      {sessions.map((s) => (
        <div
          key={s.id}
          onClick={() => onSelect(s.id)}
          style={{
            padding: '8px 10px',
            marginBottom: 4,
            borderRadius: 6,
            cursor: 'pointer',
            background: selected === s.id ? '#e8f0fe' : 'transparent',
            border: selected === s.id ? '1px solid #0066cc' : '1px solid transparent',
          }}
        >
          <div style={{ fontSize: 13, fontWeight: 500 }}>{s.id}</div>
          <div style={{ fontSize: 11, color: '#888' }}>
            {s.nodeId} · {s.status}
          </div>
        </div>
      ))}
    </div>
  );
}
