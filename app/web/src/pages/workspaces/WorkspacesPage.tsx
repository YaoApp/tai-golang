import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { api } from '../../api';
import type { WorkspaceEntry } from '../../api';
import { useCurrentNode } from '../../hooks/useCurrentNode';
import Button from '../../components/ui/Button';
import DataTable, { type Column } from '../../components/ui/DataTable';
import Field from '../../components/ui/Field';
import PageHeader from '../../components/ui/PageHeader';

export default function WorkspacesPage() {
  const [currentNode, setCurrentNode, nodes] = useCurrentNode();
  const [workspaces, setWorkspaces] = useState<WorkspaceEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [showCreate, setShowCreate] = useState(false);
  const [createName, setCreateName] = useState('');
  const navigate = useNavigate();

  useEffect(() => {
    if (!currentNode) return;
    setLoading(true);
    setError('');
    api.workspaces
      .list(currentNode)
      .then(setWorkspaces)
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false));
  }, [currentNode]);

  const handleCreate = async () => {
    if (!createName) return;
    try {
      await api.workspaces.create(currentNode, createName);
      setShowCreate(false);
      setCreateName('');
      const updated = await api.workspaces.list(currentNode);
      setWorkspaces(updated);
    } catch (e: any) {
      setError(e.message);
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm(`Delete workspace ${id}?`)) return;
    try {
      await api.workspaces.delete(currentNode, id);
      setWorkspaces((ws) => ws.filter((w) => w.id !== id));
    } catch (e: any) {
      setError(e.message);
    }
  };

  const columns: Column<WorkspaceEntry>[] = [
    {
      key: 'id',
      header: 'ID',
      mono: true,
      render: (ws) => (
        <a
          href={`/workspaces/${ws.id}?nodeId=${currentNode}`}
          onClick={(e) => {
            e.preventDefault();
            navigate(`/workspaces/${ws.id}?nodeId=${currentNode}`);
          }}
          title={ws.id}
        >
          {ws.id.slice(0, 12)}…
        </a>
      ),
    },
    {
      key: 'name',
      header: 'Name',
      primary: true,
      render: (ws) => ws.name || <span className="faint">—</span>,
    },
    {
      key: 'owner',
      header: 'Owner',
      render: (ws) => ws.owner || <span className="faint">—</span>,
    },
    {
      key: 'actions',
      header: '',
      width: 100,
      render: (ws) => (
        <Button size="sm" variant="danger" onClick={() => void handleDelete(ws.id)}>Delete</Button>
      ),
    },
  ];

  return (
    <div>
      <PageHeader title="Workspaces" subtitle="Remote filesystem and Git branches" />

      <div className="toolbar">
        <Field label="Node">
          <select
            className="select"
            value={currentNode}
            onChange={(e) => setCurrentNode(e.target.value)}
            style={{ minWidth: 200 }}
          >
            <option value="">Select a node…</option>
            {nodes.map((n) => (
              <option key={n.id} value={n.id}>{n.id}</option>
            ))}
          </select>
        </Field>
        <span className="spacer" />
        {currentNode && (
          <Button variant="primary" size="sm" onClick={() => setShowCreate(!showCreate)}>
            + Create workspace
          </Button>
        )}
      </div>

      {error && <div className="notice notice--error" style={{ marginBottom: 'var(--space-3)' }}>{error}</div>}

      {showCreate && (
        <div className="inline-form">
          <input
            value={createName}
            onChange={(e) => setCreateName(e.target.value)}
            placeholder="my-project"
            className="input"
            style={{ width: 260 }}
          />
          <Button size="sm" variant="primary" onClick={handleCreate}>Create</Button>
          <Button size="sm" variant="ghost" onClick={() => setShowCreate(false)}>Cancel</Button>
        </div>
      )}

      {!currentNode ? (
        <div className="empty">
          <div className="empty__title">No node selected</div>
          <div>Pick a node above to list its workspaces.</div>
        </div>
      ) : loading ? (
        <div className="empty">Loading workspaces…</div>
      ) : (
        <DataTable
          columns={columns}
          rows={workspaces}
          rowKey={(ws) => ws.id}
          empty="No workspaces found."
        />
      )}
    </div>
  );
}
