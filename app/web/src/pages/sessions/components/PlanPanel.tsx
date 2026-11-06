import type { PlanItem } from '../../../types';
import Badge from '../../../components/ui/Badge';

interface Props {
  item: PlanItem;
}

export default function PlanPanel({ item }: Props) {
  if (item.removed) return null;

  return (
    <div className="plan">
      <div className="plan__header">
        <span className="mono">plan</span>
        <span className="badge">{item.entries.length}</span>
      </div>
      <ul className="plan__body">
        {item.entries.map((entry, i) => (
          <li key={i} className={`plan__row plan__row--${entry.status ?? 'pending'}`}>
            <span className="plan__mark">{statusChar(entry.status)}</span>
            <span className="plan__content">{entry.content}</span>
            {entry.priority && (
              <Badge tone={entry.priority === 'high' ? 'danger' : entry.priority === 'low' ? 'default' : 'warning'}>
                {entry.priority}
              </Badge>
            )}
          </li>
        ))}
      </ul>
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
