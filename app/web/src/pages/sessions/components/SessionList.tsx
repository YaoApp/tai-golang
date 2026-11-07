import type { SessionInfo } from '../../../api';
import StatusDot from '../../../components/ui/StatusDot';

interface Props {
  sessions: SessionInfo[];
  selected: string | null;
  onSelect: (id: string) => void;
}

export default function SessionList({ sessions, selected, onSelect }: Props) {
  return (
    <div className="session-list">
      <h3 className="session-list__title">Sessions</h3>
      {sessions.length === 0 && <p className="faint" style={{ fontSize: 'var(--text-sm)' }}>No active sessions</p>}
      <div className="session-list__rows">
        {sessions.map((s) => (
          <button
            key={s.id}
            type="button"
            onClick={() => onSelect(s.id)}
            className={`session-list__item${selected === s.id ? ' session-list__item--active' : ''}`}
          >
            <span className="session-list__id mono truncate">{s.id}</span>
            <span className="session-list__meta">
              <span className="mono truncate">{s.nodeId}</span>
              <span className="mono truncate" title={s.teamId ? `team: ${s.teamId}` : 'no user'}>
                {s.userId || '—'}
              </span>
              <StatusDot status={s.status} />
            </span>
          </button>
        ))}
      </div>
    </div>
  );
}
