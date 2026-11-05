import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { api } from '../../api';
import type { SessionInfo, WorkspaceEntry } from '../../api';
import { useCurrentNode } from '../../hooks/useCurrentNode';
import SessionList from './components/SessionList';

export default function SessionsPage() {
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [currentNode, setCurrentNode, nodes] = useCurrentNode();
  const [workspaces, setWorkspaces] = useState<WorkspaceEntry[]>([]);
  const [selectedWs, setSelectedWs] = useState('');
  const [error, setError] = useState('');
  const navigate = useNavigate();

  const refresh = () => {
    api.sessions.list().then(setSessions).catch(() => {});
  };

  useEffect(() => { refresh(); }, []);

  // Load workspaces when node changes
  useEffect(() => {
    if (!currentNode) { setWorkspaces([]); return; }
    api.workspaces.list(currentNode).then(setWorkspaces).catch(() => setWorkspaces([]));
  }, [currentNode]);

  const handleCreate = async () => {
    const nodeId = currentNode || nodes[0]?.id;
    if (!nodeId) {
      setError('No nodes available. Register a node first.');
      return;
    }
    try {
      setError('');
      const req: Record<string, unknown> = {};
      if (selectedWs) {
        req.workspaceId = selectedWs;
      }
      const s = await api.sessions.create(nodeId, Object.keys(req).length > 0 ? req : undefined);
      setSessions((prev) => [...prev, s]);
      navigate(`/sessions/${s.id}`);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed');
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

  return (
    <div style={{ display: 'flex', gap: 20 }}>
      <div>
        <SessionList
          sessions={sessions}
          selected={null}
          onSelect={(id) => navigate(`/sessions/${id}`)}
        />
      </div>
      <div style={{ flex: 1 }}>
        <h1>Sessions</h1>

        {/* Create */}
        <div style={{ display: 'flex', gap: 8, marginBottom: 16, alignItems: 'center', flexWrap: 'wrap' }}>
          <select
            value={currentNode}
            onChange={(e) => setCurrentNode(e.target.value)}
            style={{ padding: '6px 10px', borderRadius: 4, border: '1px solid #ccc' }}
          >
            <option value="">Select node...</option>
            {nodes.map((n) => (
              <option key={n.id} value={n.id}>{n.id}</option>
            ))}
          </select>
          <select
            value={selectedWs}
            onChange={(e) => setSelectedWs(e.target.value)}
            style={{ padding: '6px 10px', borderRadius: 4, border: '1px solid #ccc', minWidth: 160 }}
            disabled={!currentNode}
          >
            <option value="">Auto-create workspace</option>
            {workspaces.map((ws) => (
              <option key={ws.id} value={ws.id}>
                {ws.name || ws.id.slice(0, 12)}
              </option>
            ))}
          </select>
          <button
            onClick={handleCreate}
            style={{
              padding: '6px 16px',
              border: 'none',
              borderRadius: 4,
              background: '#0066cc',
              color: '#fff',
              cursor: 'pointer',
            }}
          >
            Create Session
          </button>
          <button
            onClick={refresh}
            style={{
              padding: '6px 12px',
              border: '1px solid #ccc',
              borderRadius: 4,
              background: '#fff',
              cursor: 'pointer',
            }}
          >
            Refresh
          </button>
        </div>

        {error && <p style={{ color: 'red' }}>{error}</p>}

        {/* Session table */}
        <table style={{ borderCollapse: 'collapse', width: '100%', fontSize: 13 }}>
          <thead>
            <tr style={{ borderBottom: '2px solid #ddd' }}>
              <th style={th}>ID</th>
              <th style={th}>Node</th>
              <th style={th}>Workspace</th>
              <th style={th}>Status</th>
              <th style={th}>Created</th>
              <th style={th}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {sessions.map((s) => (
              <tr key={s.id} style={{ borderBottom: '1px solid #eee' }}>
                <td style={td}>
                  <a
                    href="#"
                    onClick={(e) => { e.preventDefault(); navigate(`/sessions/${s.id}`); }}
                    style={{ color: '#0066cc' }}
                  >
                    {s.id}
                  </a>
                </td>
                <td style={td}>{s.nodeId}</td>
                <td style={td}>
                  {s.workspaceId ? (
                    <a
                      href="#"
                      onClick={(e) => { e.preventDefault(); navigate(`/workspaces/${s.workspaceId}?nodeId=${s.nodeId}`); }}
                      style={{ color: '#0066cc' }}
                      title={s.workspaceId}
                    >
                      {s.workspaceName || s.workspaceId.slice(0, 12)}
                    </a>
                  ) : (
                    <span style={{ color: '#999' }}>auto</span>
                  )}
                </td>
                <td style={td}>
                  <span style={{ color: statusColor(s.status), fontWeight: 600 }}>{s.status}</span>
                </td>
                <td style={td}>{new Date(s.createdAt).toLocaleTimeString()}</td>
                <td style={{ ...td, display: 'flex', gap: 4 }}>
                  {(s.status === 'disconnected' || s.status === 'resume_error') && (
                    <button
                      onClick={() => handleResume(s.id)}
                      style={{
                        padding: '2px 8px',
                        border: '1px solid #28a745',
                        borderRadius: 3,
                        background: 'transparent',
                        color: '#28a745',
                        cursor: 'pointer',
                        fontSize: 12,
                      }}
                    >
                      Resume
                    </button>
                  )}
                  <button
                    onClick={() => handleDelete(s.id)}
                    style={{
                      padding: '2px 8px',
                      border: '1px solid #dc3545',
                      borderRadius: 3,
                      background: 'transparent',
                      color: '#dc3545',
                      cursor: 'pointer',
                      fontSize: 12,
                    }}
                  >
                    Close
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

const th: React.CSSProperties = { textAlign: 'left', padding: '6px 8px' };
const td: React.CSSProperties = { padding: '6px 8px' };

function statusColor(status: string): string {
  switch (status) {
    case 'running': return '#28a745';
    case 'disconnected': return '#fd7e14';
    case 'closed': return '#6c757d';
    case 'resume_error': return '#dc3545';
    case 'recovery_gap': return '#dc3545';
    default: return '#333';
  }
}
