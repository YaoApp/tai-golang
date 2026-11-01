import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { api } from '../../api';
import type { NodeInfo, WorkspaceEntry } from '../../api';

export default function WorkspacesPage() {
  const [nodes, setNodes] = useState<NodeInfo[]>([]);
  const [selectedNode, setSelectedNode] = useState('');
  const [workspaces, setWorkspaces] = useState<WorkspaceEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [showCreate, setShowCreate] = useState(false);
  const [createName, setCreateName] = useState('');
  const navigate = useNavigate();

  useEffect(() => {
    api.nodes.list().then(setNodes).catch(() => {});
  }, []);

  useEffect(() => {
    if (!selectedNode) return;
    setLoading(true);
    setError('');
    api.workspaces
      .list(selectedNode)
      .then(setWorkspaces)
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false));
  }, [selectedNode]);

  const handleCreate = async () => {
    if (!createName) return;
    try {
      await api.workspaces.create(selectedNode, createName);
      setShowCreate(false);
      setCreateName('');
      const updated = await api.workspaces.list(selectedNode);
      setWorkspaces(updated);
    } catch (e: any) {
      setError(e.message);
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm(`Delete workspace ${id}?`)) return;
    try {
      await api.workspaces.delete(selectedNode, id);
      setWorkspaces((ws) => ws.filter((w) => w.id !== id));
    } catch (e: any) {
      setError(e.message);
    }
  };

  return (
    <div>
      <h1>Workspaces</h1>

      <div style={{ marginBottom: 16 }}>
        <label>Node: </label>
        <select
          value={selectedNode}
          onChange={(e) => setSelectedNode(e.target.value)}
          style={{ padding: '4px 8px', minWidth: 200 }}
        >
          <option value="">Select a node...</option>
          {nodes.map((n) => (
            <option key={n.id} value={n.id}>{n.id}</option>
          ))}
        </select>
      </div>

      {error && <p style={{ color: 'red' }}>{error}</p>}

      {selectedNode && (
        <>
          <div style={{ marginBottom: 12 }}>
            <button onClick={() => setShowCreate(!showCreate)} style={btnStyle}>
              + Create Workspace
            </button>
          </div>

          {showCreate && (
            <div style={{ marginBottom: 16, padding: 12, border: '1px solid #ddd', borderRadius: 6 }}>
              <div style={{ marginBottom: 8 }}>
                <label>Name: </label>
                <input value={createName} onChange={(e) => setCreateName(e.target.value)}
                  placeholder="my-project" style={inputStyle} />
              </div>
              <button onClick={handleCreate} style={btnStyle}>Create</button>
              <button onClick={() => setShowCreate(false)} style={{ ...btnStyle, marginLeft: 8, background: '#6c757d' }}>Cancel</button>
            </div>
          )}

          {loading ? (
            <p>Loading workspaces...</p>
          ) : workspaces.length === 0 ? (
            <p style={{ color: '#666' }}>No workspaces found.</p>
          ) : (
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr style={{ borderBottom: '2px solid #ddd', textAlign: 'left' }}>
                  <th style={thStyle}>ID</th>
                  <th style={thStyle}>Name</th>
                  <th style={thStyle}>Owner</th>
                  <th style={thStyle}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {workspaces.map((ws) => (
                  <tr key={ws.id} style={{ borderBottom: '1px solid #eee' }}>
                    <td style={tdStyle}>
                      <a href="#" onClick={(e) => { e.preventDefault(); navigate(`/workspaces/${ws.id}?nodeId=${selectedNode}`); }}
                        style={{ color: '#0066cc' }}>{ws.id.slice(0, 12)}...</a>
                    </td>
                    <td style={tdStyle}>{ws.name || '—'}</td>
                    <td style={tdStyle}>{ws.owner || '—'}</td>
                    <td style={tdStyle}>
                      <button onClick={() => handleDelete(ws.id)}
                        style={{ ...btnStyle, background: '#dc3545', fontSize: 12, padding: '2px 8px' }}>Delete</button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </>
      )}
    </div>
  );
}

const btnStyle: React.CSSProperties = {
  padding: '6px 14px', background: '#0066cc', color: '#fff',
  border: 'none', borderRadius: 4, cursor: 'pointer', fontSize: 14,
};
const inputStyle: React.CSSProperties = {
  padding: '4px 8px', border: '1px solid #ccc', borderRadius: 4, width: 300,
};
const thStyle: React.CSSProperties = { padding: '8px 12px' };
const tdStyle: React.CSSProperties = { padding: '8px 12px' };
