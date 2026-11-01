import { useEffect, useState, useCallback } from 'react';
import { useParams, useSearchParams } from 'react-router-dom';
import { api } from '../../api';
import type { BranchInfo, FileStatEntry, CommitEntry } from '../../api';
import FilePreview from './components/FilePreview';

export default function WorkspaceDetailPage() {
  const { id: wsId } = useParams<{ id: string }>();
  const [searchParams] = useSearchParams();
  const nodeId = searchParams.get('nodeId') || '';

  const [branches, setBranches] = useState<BranchInfo[]>([]);
  const [selectedBranch, setSelectedBranch] = useState('main');
  const [currentPath, setCurrentPath] = useState('.');
  const [entries, setEntries] = useState<FileStatEntry[]>([]);
  const [commits, setCommits] = useState<CommitEntry[]>([]);
  const [previewPath, setPreviewPath] = useState('');
  const [error, setError] = useState('');
  const [tab, setTab] = useState<'files' | 'history'>('files');

  // Branch management
  const [showCreateBranch, setShowCreateBranch] = useState(false);
  const [newBranchName, setNewBranchName] = useState('');

  // Commit
  const [commitMsg, setCommitMsg] = useState('');

  // Merge
  const [showMerge, setShowMerge] = useState(false);
  const [mergeFrom, setMergeFrom] = useState('');
  const [mergeInto, setMergeInto] = useState('main');

  // Upload
  const [uploading, setUploading] = useState(false);

  const loadBranches = useCallback(async () => {
    if (!wsId || !nodeId) return;
    try {
      const b = await api.workspaces.branches.list(nodeId, wsId);
      setBranches(b);
    } catch (e: any) { setError(e.message); }
  }, [nodeId, wsId]);

  const loadFiles = useCallback(async () => {
    if (!wsId || !nodeId) return;
    try {
      const stat = await api.workspaces.files.stat(nodeId, wsId, selectedBranch, currentPath);
      setEntries(stat.entries || []);
      setError('');
    } catch (e: any) { setError(e.message); }
  }, [nodeId, wsId, selectedBranch, currentPath]);

  const loadHistory = useCallback(async () => {
    if (!wsId || !nodeId) return;
    try {
      const logs = await api.workspaces.branches.log(nodeId, wsId, selectedBranch, 50);
      setCommits(logs || []);
    } catch (e: any) { setError(e.message); }
  }, [nodeId, wsId, selectedBranch]);

  useEffect(() => { loadBranches(); }, [loadBranches]);
  useEffect(() => { if (tab === 'files') loadFiles(); }, [loadFiles, tab]);
  useEffect(() => { if (tab === 'history') loadHistory(); }, [loadHistory, tab]);

  const navigateTo = (name: string, isDir: boolean) => {
    if (isDir) {
      setCurrentPath(currentPath === '.' ? name : `${currentPath}/${name}`);
      setPreviewPath('');
    } else {
      const full = currentPath === '.' ? name : `${currentPath}/${name}`;
      setPreviewPath(full);
    }
  };

  const goUp = () => {
    if (currentPath === '.') return;
    const parts = currentPath.split('/');
    parts.pop();
    setCurrentPath(parts.length === 0 ? '.' : parts.join('/'));
    setPreviewPath('');
  };

  const handleCreateBranch = async () => {
    if (!newBranchName || !wsId) return;
    try {
      await api.workspaces.branches.create(nodeId, wsId, newBranchName, selectedBranch);
      setShowCreateBranch(false);
      setNewBranchName('');
      loadBranches();
    } catch (e: any) { setError(e.message); }
  };

  const handleDeleteBranch = async (name: string) => {
    if (!confirm(`Delete branch "${name}"?`) || !wsId) return;
    try {
      await api.workspaces.branches.delete(nodeId, wsId, name);
      if (selectedBranch === name) setSelectedBranch('main');
      loadBranches();
    } catch (e: any) { setError(e.message); }
  };

  const handleCommit = async () => {
    if (!commitMsg || !wsId) return;
    try {
      await api.workspaces.branches.commit(nodeId, wsId, selectedBranch, commitMsg);
      setCommitMsg('');
      if (tab === 'history') loadHistory();
    } catch (e: any) { setError(e.message); }
  };

  const handleMerge = async () => {
    if (!mergeFrom || !wsId) return;
    try {
      await api.workspaces.merge(nodeId, wsId, mergeFrom, mergeInto);
      setShowMerge(false);
      loadBranches();
      if (tab === 'history') loadHistory();
    } catch (e: any) { setError(e.message); }
  };

  const handleMkdir = async () => {
    const name = prompt('Directory name:');
    if (!name || !wsId) return;
    const path = currentPath === '.' ? name : `${currentPath}/${name}`;
    try {
      await api.workspaces.files.mkdir(nodeId, wsId, selectedBranch, path);
      loadFiles();
    } catch (e: any) { setError(e.message); }
  };

  const handleDeleteFile = async (name: string, isDir: boolean) => {
    if (!confirm(`Delete "${name}"?`) || !wsId) return;
    const path = currentPath === '.' ? name : `${currentPath}/${name}`;
    try {
      await api.workspaces.files.delete(nodeId, wsId, selectedBranch, path, isDir);
      loadFiles();
    } catch (e: any) { setError(e.message); }
  };

  const handleRename = async (oldName: string) => {
    const newName = prompt('New name:', oldName);
    if (!newName || newName === oldName || !wsId) return;
    const oldPath = currentPath === '.' ? oldName : `${currentPath}/${oldName}`;
    const newPath = currentPath === '.' ? newName : `${currentPath}/${newName}`;
    try {
      await api.workspaces.files.rename(nodeId, wsId, selectedBranch, oldPath, newPath);
      loadFiles();
    } catch (e: any) { setError(e.message); }
  };

  const handleUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file || !wsId) return;
    const path = currentPath === '.' ? file.name : `${currentPath}/${file.name}`;
    setUploading(true);
    try {
      await api.workspaces.files.upload(nodeId, wsId, selectedBranch, path, file);
      loadFiles();
    } catch (err: any) { setError(err.message); }
    setUploading(false);
    e.target.value = '';
  };

  return (
    <div>
      <h1>Workspace: {wsId?.slice(0, 12)}...</h1>
      {error && <p style={{ color: 'red' }}>{error}</p>}

      {/* Branch selector */}
      <div style={{ marginBottom: 16, display: 'flex', alignItems: 'center', gap: 8 }}>
        <label>Branch:</label>
        <select value={selectedBranch} onChange={(e) => { setSelectedBranch(e.target.value); setCurrentPath('.'); setPreviewPath(''); }}
          style={{ padding: '4px 8px' }}>
          {branches.map((b) => <option key={b.name} value={b.name}>{b.name}</option>)}
        </select>
        <button onClick={() => setShowCreateBranch(true)} style={btnSmall}>+ Branch</button>
        {selectedBranch !== 'main' && (
          <button onClick={() => handleDeleteBranch(selectedBranch)} style={{ ...btnSmall, background: '#dc3545' }}>Delete</button>
        )}
        <button onClick={() => setShowMerge(true)} style={btnSmall}>Merge</button>
      </div>

      {showCreateBranch && (
        <div style={panelStyle}>
          <input value={newBranchName} onChange={(e) => setNewBranchName(e.target.value)}
            placeholder="branch-name" style={inputStyle} />
          <button onClick={handleCreateBranch} style={{ ...btnSmall, marginLeft: 8 }}>Create</button>
          <button onClick={() => setShowCreateBranch(false)} style={{ ...btnSmall, marginLeft: 4, background: '#6c757d' }}>Cancel</button>
        </div>
      )}

      {showMerge && (
        <div style={panelStyle}>
          <label>From: </label>
          <select value={mergeFrom} onChange={(e) => setMergeFrom(e.target.value)} style={{ padding: '4px 8px' }}>
            <option value="">Select...</option>
            {branches.filter((b) => b.name !== mergeInto).map((b) => <option key={b.name} value={b.name}>{b.name}</option>)}
          </select>
          <label style={{ marginLeft: 8 }}>Into: </label>
          <select value={mergeInto} onChange={(e) => setMergeInto(e.target.value)} style={{ padding: '4px 8px' }}>
            {branches.map((b) => <option key={b.name} value={b.name}>{b.name}</option>)}
          </select>
          <button onClick={handleMerge} style={{ ...btnSmall, marginLeft: 8 }}>Merge</button>
          <button onClick={() => setShowMerge(false)} style={{ ...btnSmall, marginLeft: 4, background: '#6c757d' }}>Cancel</button>
        </div>
      )}

      {/* Commit bar */}
      <div style={{ marginBottom: 16, display: 'flex', alignItems: 'center', gap: 8 }}>
        <input value={commitMsg} onChange={(e) => setCommitMsg(e.target.value)}
          placeholder="Commit message..." style={{ ...inputStyle, flex: 1 }}
          onKeyDown={(e) => e.key === 'Enter' && handleCommit()} />
        <button onClick={handleCommit} disabled={!commitMsg} style={btnSmall}>Commit</button>
      </div>

      {/* Tabs */}
      <div style={{ marginBottom: 12, borderBottom: '2px solid #ddd' }}>
        <button onClick={() => setTab('files')} style={tab === 'files' ? tabActive : tabInactive}>Files</button>
        <button onClick={() => setTab('history')} style={tab === 'history' ? tabActive : tabInactive}>History</button>
      </div>

      {tab === 'files' && (
        <div style={{ display: 'flex', gap: 16 }}>
          <div style={{ flex: 1 }}>
            {/* Breadcrumb */}
            <div style={{ marginBottom: 8, display: 'flex', alignItems: 'center', gap: 4 }}>
              <button onClick={() => { setCurrentPath('.'); setPreviewPath(''); }} style={linkBtn}>root</button>
              {currentPath !== '.' && currentPath.split('/').map((part, i, arr) => (
                <span key={i}>
                  <span style={{ color: '#999' }}>/</span>
                  <button onClick={() => setCurrentPath(arr.slice(0, i + 1).join('/'))} style={linkBtn}>{part}</button>
                </span>
              ))}
            </div>

            {/* Actions */}
            <div style={{ marginBottom: 8, display: 'flex', gap: 8 }}>
              {currentPath !== '.' && <button onClick={goUp} style={btnSmall}>⬆ Up</button>}
              <button onClick={handleMkdir} style={btnSmall}>📁 New Folder</button>
              <label style={{ ...btnSmall, display: 'inline-flex', alignItems: 'center', cursor: 'pointer' }}>
                📤 Upload
                <input type="file" onChange={handleUpload} style={{ display: 'none' }} />
              </label>
              {uploading && <span style={{ color: '#666' }}>Uploading...</span>}
            </div>

            {/* File table */}
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr style={{ borderBottom: '2px solid #ddd', textAlign: 'left' }}>
                  <th style={thStyle}>Name</th>
                  <th style={thStyle}>Size</th>
                  <th style={thStyle}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {entries.map((entry) => (
                  <tr key={entry.name} style={{ borderBottom: '1px solid #eee' }}>
                    <td style={tdStyle}>
                      <a href="#" onClick={(e) => { e.preventDefault(); navigateTo(entry.name, entry.isDir); }}
                        style={{ color: '#0066cc', textDecoration: 'none' }}>
                        {entry.isDir ? '📁 ' : '📄 '}{entry.name}
                      </a>
                    </td>
                    <td style={tdStyle}>{entry.isDir ? '—' : formatSize(entry.size || 0)}</td>
                    <td style={tdStyle}>
                      <button onClick={() => handleRename(entry.name)} style={actionBtn}>Rename</button>
                      <button onClick={() => handleDeleteFile(entry.name, entry.isDir)}
                        style={{ ...actionBtn, color: '#dc3545' }}>Delete</button>
                    </td>
                  </tr>
                ))}
                {entries.length === 0 && (
                  <tr><td colSpan={3} style={{ ...tdStyle, color: '#999', textAlign: 'center' }}>Empty directory</td></tr>
                )}
              </tbody>
            </table>
          </div>

          {/* Preview panel */}
          {previewPath && wsId && (
            <div style={{ width: 400, borderLeft: '1px solid #ddd', paddingLeft: 16 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
                <strong>{previewPath.split('/').pop()}</strong>
                <button onClick={() => setPreviewPath('')} style={actionBtn}>✕</button>
              </div>
              <FilePreview nodeId={nodeId} wsId={wsId} branch={selectedBranch} path={previewPath} />
            </div>
          )}
        </div>
      )}

      {tab === 'history' && (
        <table style={{ width: '100%', borderCollapse: 'collapse' }}>
          <thead>
            <tr style={{ borderBottom: '2px solid #ddd', textAlign: 'left' }}>
              <th style={thStyle}>ID</th>
              <th style={thStyle}>Branch</th>
              <th style={thStyle}>Message</th>
              <th style={thStyle}>Time</th>
            </tr>
          </thead>
          <tbody>
            {commits.map((c) => (
              <tr key={c.id} style={{ borderBottom: '1px solid #eee' }}>
                <td style={{ ...tdStyle, fontFamily: 'monospace', fontSize: 12 }}>{c.id?.slice(0, 8)}</td>
                <td style={tdStyle}>
                  <span style={{ background: '#e8f0fe', padding: '2px 6px', borderRadius: 4, fontSize: 12 }}>{c.branch}</span>
                </td>
                <td style={tdStyle}>{c.message}</td>
                <td style={{ ...tdStyle, color: '#666', fontSize: 12 }}>
                  {c.time ? new Date(c.time * 1000).toLocaleString() : '—'}
                </td>
              </tr>
            ))}
            {commits.length === 0 && (
              <tr><td colSpan={4} style={{ ...tdStyle, color: '#999', textAlign: 'center' }}>No commits yet</td></tr>
            )}
          </tbody>
        </table>
      )}
    </div>
  );
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

const btnSmall: React.CSSProperties = {
  padding: '4px 10px', background: '#0066cc', color: '#fff',
  border: 'none', borderRadius: 4, cursor: 'pointer', fontSize: 12,
};
const inputStyle: React.CSSProperties = { padding: '4px 8px', border: '1px solid #ccc', borderRadius: 4 };
const panelStyle: React.CSSProperties = { marginBottom: 12, padding: 8, border: '1px solid #ddd', borderRadius: 6, display: 'flex', alignItems: 'center', gap: 4 };
const thStyle: React.CSSProperties = { padding: '8px 12px' };
const tdStyle: React.CSSProperties = { padding: '8px 12px' };
const tabActive: React.CSSProperties = { padding: '8px 16px', border: 'none', borderBottom: '2px solid #0066cc', background: 'none', cursor: 'pointer', fontWeight: 600, color: '#0066cc' };
const tabInactive: React.CSSProperties = { padding: '8px 16px', border: 'none', borderBottom: '2px solid transparent', background: 'none', cursor: 'pointer', color: '#666' };
const actionBtn: React.CSSProperties = { background: 'none', border: 'none', cursor: 'pointer', color: '#0066cc', fontSize: 12, marginRight: 8 };
const linkBtn: React.CSSProperties = { background: 'none', border: 'none', cursor: 'pointer', color: '#0066cc', fontSize: 14, padding: 0 };
