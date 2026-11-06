import { useState } from 'react';
import type { ToolCallItem } from '../../../types';
import Badge from '../../../components/ui/Badge';
import Caret from '../../../components/ui/Caret';

interface Props {
  item: ToolCallItem;
}

const TONE: Record<string, 'default' | 'success' | 'warning' | 'danger' | 'info'> = {
  pending: 'warning',
  in_progress: 'info',
  completed: 'success',
  failed: 'danger',
  cancelled: 'default',
};

export default function ToolCallCard({ item }: Props) {
  const [expanded, setExpanded] = useState(false);
  const output = item.output.join('');

  return (
    <div className={`tool${expanded ? ' tool--open' : ''}`}>
      <button type="button" className="tool__head" onClick={() => setExpanded(!expanded)}>
        <span className="tool__caret">
          <Caret direction={expanded ? 'down' : 'right'} size={10} />
        </span>
        <span className="tool__name mono">{item.name || 'tool'}</span>
        {item.kind && <Badge>{item.kind}</Badge>}
        <Badge tone={TONE[item.status] ?? 'default'}>{item.status}</Badge>
        {item.locations && item.locations.length > 0 && (
          <span className="tool__location mono truncate">{item.locations[0].path}</span>
        )}
      </button>
      {expanded && output && (
        <pre className="tool__output mono">{output}</pre>
      )}
    </div>
  );
}
