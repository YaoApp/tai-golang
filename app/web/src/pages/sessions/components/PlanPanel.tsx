import type { PlanItem } from '../../../types';

interface Props {
  item: PlanItem;
}

export default function PlanPanel({ item }: Props) {
  if (item.removed) return null;

  return (
    <div style={card}>
      <div style={header}>📋 Plan</div>
      <div style={body}>
        {item.entries.map((entry, i) => (
          <div key={i} style={entryRow}>
            <span style={statusIcon(entry.status)}>{statusChar(entry.status)}</span>
            <span style={{ flex: 1 }}>{entry.content}</span>
            {entry.priority && <span style={priorityBadge(entry.priority)}>{entry.priority}</span>}
          </div>
        ))}
      </div>
    </div>
  );
}

function statusChar(status?: string): string {
  switch (status) {
    case 'completed': return '✓';
    case 'in_progress': return '●';
    case 'failed': return '✗';
    case 'cancelled': return '○';
    default: return '○';
  }
}

function statusIcon(status?: string): React.CSSProperties {
  const color = {
    completed: '#28a745',
    in_progress: '#0066cc',
    failed: '#dc3545',
    cancelled: '#6c757d',
  }[status ?? ''] ?? '#999';
  return { marginRight: 8, color, fontWeight: 700, fontSize: 14, width: 16, textAlign: 'center' };
}

function priorityBadge(priority: string): React.CSSProperties {
  const bg = priority === 'high' ? '#f8d7da' : priority === 'low' ? '#e2e3e5' : '#fff3cd';
  return {
    display: 'inline-block', padding: '1px 5px', borderRadius: 3,
    background: bg, fontSize: 10, fontWeight: 600, marginLeft: 8,
  };
}

const card: React.CSSProperties = {
  margin: '8px 0', border: '1px solid #d4edda', borderRadius: 6,
  background: '#f8fff8', overflow: 'hidden',
};

const header: React.CSSProperties = {
  padding: '6px 10px', fontWeight: 600, fontSize: 12,
  background: '#e8f5e9', borderBottom: '1px solid #d4edda',
};

const body: React.CSSProperties = { padding: '6px 10px' };

const entryRow: React.CSSProperties = {
  display: 'flex', alignItems: 'center', padding: '4px 0',
  fontSize: 12, borderBottom: '1px solid #eef5ee',
};
