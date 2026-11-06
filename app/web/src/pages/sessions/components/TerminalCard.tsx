import { useState } from 'react';
import type { TerminalItem } from '../../../types';
import Badge from '../../../components/ui/Badge';
import Caret from '../../../components/ui/Caret';

interface Props {
  item: TerminalItem;
}

export default function TerminalCard({ item }: Props) {
  const [expanded, setExpanded] = useState(true);
  const output = item.output.join('');
  const hasExit = item.exitCode !== undefined;
  const exitOk = item.exitCode === 0;

  return (
    <div className="term">
      <button type="button" className="term__head" onClick={() => setExpanded(!expanded)}>
        <span className="term__caret">
          <Caret direction={expanded ? 'down' : 'right'} size={10} />
        </span>
        <span className="term__title mono">{item.title || item.command || 'Terminal'}</span>
        {item.cwd && <span className="term__cwd mono truncate">{item.cwd}</span>}
        {hasExit && (
          <Badge tone={exitOk ? 'success' : 'danger'}>exit {item.exitCode}</Badge>
        )}
      </button>
      {expanded && output && (
        <pre className="term__output mono">{atob(output)}</pre>
      )}
    </div>
  );
}

function atob(s: string): string {
  try { return globalThis.atob(s); } catch { return s; }
}
