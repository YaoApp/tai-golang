import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { api } from '../../api';
import type { SessionInfo, NodeInfo } from '../../api';
import SessionList from './components/SessionList';

export default function SessionsPage() {
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [nodes, setNodes] = useState<NodeInfo[]>([]);
  const [selectedNode, setSelectedNode] = useState('');
  const [error, setError] = useState('');
  const navigate = useNavigate();

  const refresh = () => {
    api.sessions.list().then(setSessions).catch(() => {});
    api.nodes.list().then(setNodes).catch(() => {});
  };

  useEffect(() => { refresh(); }, []);

  const handleCreate = async () => {
    const nodeId = selectedNode || nodes[0]?.id;
    if (!nodeId) {
      setError('No nodes available. Register a node first.');
      return;
    }
    try {
      setError('');
      const s = await api.sessions.create(nodeId);
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
        <div style={{ display: 'flex', gap: 8, marginBottom: 16, alignItems: 'center' }}>
          <select
            value={selectedNode}
            onChange={(e) => setSelectedNode(e.target.value)}
            style={{ padding: '6px 10px', borderRadius: 4, border: '1px solid #ccc' }}
          >
            <option value="">Select node...</option>
            {nodes.map((n) => (
              <option key={n.id} value={n.id}>{n.id}</option>
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
                <td style={td}>{s.status}</td>
                <td style={td}>{new Date(s.createdAt).toLocaleTimeString()}</td>
                <td style={td}>
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
