import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { api } from '../../api';
import type { SessionInfo, WorkspaceEntry } from '../../api';
import type { SessionRequest } from '../../types';
import { useCurrentNode } from '../../hooks/useCurrentNode';
import { eventBus } from '../../lib/events';
import SessionList from './components/SessionList';
import SessionCreateForm from './components/SessionCreateForm';
import Button from '../../components/ui/Button';
import DataTable, { type Column } from '../../components/ui/DataTable';
import PageHeader from '../../components/ui/PageHeader';
import StatusDot from '../../components/ui/StatusDot';

export default function SessionsPage() {
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [currentNode, setCurrentNode, nodes] = useCurrentNode();
  const [workspaces, setWorkspaces] = useState<WorkspaceEntry[]>([]);
  const [error, setError] = useState('');
  const [creating, setCreating] = useState(false);
  const navigate = useNavigate();

  const refresh = () => {
    api.sessions.list().then(setSessions).catch(() => {});
  };

  useEffect(() => { refresh(); }, []);

  // session.status patches just that row instead of refetching the list;
  // session.closed means the session is gone, so drop the row.
  // This page is manual-action first with event correction, not a live feed.
  useEffect(() => {
    const offStatus = eventBus.on('session.status', (d) => {
      const id = d.sessionId as string | undefined;
      const status = d.status as string | undefined;
      if (!id || !status) return;
      setSessions((prev) =>
        prev.map((s) => (s.id === id ? { ...s, status } : s)),
      );
    });
    const offClosed = eventBus.on('session.closed', (d) => {
      const id = d.sessionId as string | undefined;
      if (!id) return;
      setSessions((prev) => prev.filter((s) => s.id !== id));
    });
    return () => {
      offStatus();
      offClosed();
    };
  }, []);

  useEffect(() => {
    if (!currentNode) { setWorkspaces([]); return; }
    api.workspaces.list(currentNode).then(setWorkspaces).catch(() => setWorkspaces([]));
  }, [currentNode]);

  const handleCreate = async (nodeId: string, req: SessionRequest) => {
    try {
      setError('');
      setCreating(true);
      const s = await api.sessions.create(nodeId, req as unknown as Record<string, unknown>);
      setSessions((prev) => [...prev, s]);
      navigate(`/sessions/${s.id}`);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed');
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (id: string) => {
    await api.sessions.delete(id).catch(() => {});
    setSessions((prev) => prev.filter((s) => s.id !== id));
  };

  const handleResume = async (id: string) => {
    try {
      setError('');
      const result = await api.sessions.resume(id);
      setSessions((prev) => prev.map((s) => s.id === id ? { ...s, status: result.status } : s));
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Resume failed');
    }
  };

  const columns: Column<SessionInfo>[] = [
    {
      key: 'id',
      header: 'Session',
      primary: true,
      render: (s) => (
        <a
          href={`/sessions/${s.id}`}
          className="mono"
          onClick={(e) => { e.preventDefault(); navigate(`/sessions/${s.id}`); }}
        >
          {s.id}
        </a>
      ),
    },
    {
      key: 'node',
      header: 'Node',
      mono: true,
      render: (s) => s.nodeId,
    },
    {
      key: 'workspace',
      header: 'Workspace',
      render: (s) =>
        s.workspaceId ? (
          <a
            href={`/workspaces/${s.workspaceId}?nodeId=${s.nodeId}`}
            onClick={(e) => {
              e.preventDefault();
              navigate(`/workspaces/${s.workspaceId}?nodeId=${s.nodeId}`);
            }}
            title={s.workspaceId}
          >
            {s.workspaceName || s.workspaceId.slice(0, 12)}
          </a>
        ) : (
          <span className="faint">auto</span>
        ),
    },
    {
      key: 'status',
      header: 'Status',
      render: (s) => <StatusDot status={s.status} />,
    },
    {
      key: 'created',
      header: 'Created',
      mono: true,
      render: (s) => new Date(s.createdAt).toLocaleTimeString(),
    },
    {
      key: 'actions',
      header: '',
      width: 150,
      render: (s) => (
        <div className="row" style={{ gap: 'var(--space-1)' }}>
          {RESUMABLE.has(s.status) && (
            <Button size="sm" onClick={() => void handleResume(s.id)}>Resume</Button>
          )}
          <Button size="sm" variant="danger" onClick={() => void handleDelete(s.id)}>Close</Button>
        </div>
      ),
    },
  ];

  return (
    <div className="sessions-layout">
      <SessionList
        sessions={sessions}
        selected={null}
        onSelect={(id) => navigate(`/sessions/${id}`)}
      />

      <div style={{ flex: 1, minWidth: 0 }}>
        <PageHeader
          title="Sessions"
          actions={<Button onClick={refresh}>Refresh</Button>}
        />

        <SessionCreateForm
          nodes={nodes}
          currentNode={currentNode}
          onNodeChange={setCurrentNode}
          workspaces={workspaces}
          onSubmit={handleCreate}
          onError={setError}
          disabled={creating}
        />

        {error && <div className="notice notice--error" style={{ marginTop: 'var(--space-3)' }}>{error}</div>}

        <div className="section-title" style={{ marginTop: 'var(--space-4)' }}>
          Active sessions
        </div>
        <DataTable
          columns={columns}
          rows={sessions}
          rowKey={(s) => s.id}
          empty="No sessions yet — create one above."
        />
      </div>
    </div>
  );
}

/** Recoverable states that show a Resume button; mirrors SessionStatus.CanResume(). */
const RESUMABLE = new Set(['disconnected', 'recovery_gap', 'resume_error']);
