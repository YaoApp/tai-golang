import { useEffect, useState, useCallback } from 'react';
import { useParams, useSearchParams } from 'react-router-dom';
import { api } from '../../api';
import type { BranchInfo, FileStatEntry, CommitEntry } from '../../api';
import FilePreview from './components/FilePreview';
import ContextPanel from './components/ContextPanel';
import SkillsPanel from './components/SkillsPanel';
import Button from '../../components/ui/Button';

type Tab = 'files' | 'history' | 'context' | 'skills';

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
  const [tab, setTab] = useState<Tab>('files');

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
      <div className="page-header">
        <div className="page-header__title">
          <h1 className="mono" style={{ fontSize: 'var(--text-xl)' }}>
            {wsId?.slice(0, 12)}…
          </h1>
          <span className="page-header__subtitle mono">workspace</span>
        </div>
      </div>

      {error && <div className="notice notice--error" style={{ marginBottom: 'var(--space-3)' }}>{error}</div>}

      {/* Branch + commit toolbar */}
      <div className="toolbar">
        <div className="field" style={{ flexDirection: 'row', alignItems: 'center', gap: 'var(--space-2)' }}>
          <span className="field__label">Branch</span>
          <select
            className="select mono"
            value={selectedBranch}
            onChange={(e) => { setSelectedBranch(e.target.value); setCurrentPath('.'); setPreviewPath(''); }}
          >
            {branches.map((b) => <option key={b.name} value={b.name}>{b.name}</option>)}
          </select>
        </div>
        <Button size="sm" onClick={() => setShowCreateBranch(true)}>+ Branch</Button>
        {selectedBranch !== 'main' && (
          <Button size="sm" variant="danger" onClick={() => handleDeleteBranch(selectedBranch)}>Delete</Button>
        )}
        <Button size="sm" onClick={() => setShowMerge(true)}>Merge</Button>

        <span className="spacer" />

        <input
          className="input"
          value={commitMsg}
          onChange={(e) => setCommitMsg(e.target.value)}
          placeholder="Commit message…"
          style={{ flex: 1, maxWidth: 360 }}
          onKeyDown={(e) => e.key === 'Enter' && handleCommit()}
        />
        <Button size="sm" variant="primary" disabled={!commitMsg} onClick={handleCommit}>Commit</Button>
      </div>

      {showCreateBranch && (
        <div className="inline-form">
          <input
            value={newBranchName}
            onChange={(e) => setNewBranchName(e.target.value)}
            placeholder="branch-name"
            className="input mono"
            style={{ width: 220 }}
          />
          <Button size="sm" variant="primary" onClick={handleCreateBranch}>Create</Button>
          <Button size="sm" variant="ghost" onClick={() => setShowCreateBranch(false)}>Cancel</Button>
        </div>
      )}

      {showMerge && (
        <div className="inline-form">
          <span className="field__label">From</span>
          <select className="select mono" value={mergeFrom} onChange={(e) => setMergeFrom(e.target.value)}>
            <option value="">Select…</option>
            {branches.filter((b) => b.name !== mergeInto).map((b) => <option key={b.name} value={b.name}>{b.name}</option>)}
          </select>
          <span className="field__label" style={{ marginLeft: 'var(--space-2)' }}>Into</span>
          <select className="select mono" value={mergeInto} onChange={(e) => setMergeInto(e.target.value)}>
            {branches.map((b) => <option key={b.name} value={b.name}>{b.name}</option>)}
          </select>
          <Button size="sm" variant="primary" onClick={handleMerge}>Merge</Button>
          <Button size="sm" variant="ghost" onClick={() => setShowMerge(false)}>Cancel</Button>
        </div>
      )}

      {/* Tabs */}
      <div className="tabs">
        {(['files', 'history', 'context', 'skills'] as Tab[]).map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => setTab(t)}
            className={`tabs__item${tab === t ? ' tabs__item--active' : ''}`}
          >
            {t[0].toUpperCase() + t.slice(1)}
          </button>
        ))}
      </div>

      {tab === 'context' && (
        <ContextPanel
          nodeId={nodeId}
          wsId={wsId ?? ''}
          // The demo has no bot/home/board entities, so fixed placeholders and
          // the workspace ID are used to give every scope a readable directory.
          // Scope name is part of the path, so board and session do not collide
          // even though they share the ID.
          scopeIds={{
            bot: 'demo',
            home: 'demo',
            board: wsId ?? '',
            session: wsId ?? '',
          }}
        />
      )}

      {tab === 'skills' && <SkillsPanel nodeId={nodeId} wsId={wsId ?? ''} />}

      {tab === 'files' && (
        <div className="files-layout">
          <div className="files-layout__main">
            {/* Breadcrumb */}
            <div className="breadcrumb">
              <button
                type="button"
                className="breadcrumb__link"
                onClick={() => { setCurrentPath('.'); setPreviewPath(''); }}
              >
                root
              </button>
              {currentPath !== '.' && currentPath.split('/').map((part, i, arr) => (
                <span key={i} className="breadcrumb__seg">
                  <span className="faint">/</span>
                  <button
                    type="button"
                    className="breadcrumb__link"
                    onClick={() => setCurrentPath(arr.slice(0, i + 1).join('/'))}
                  >
                    {part}
                  </button>
                </span>
              ))}
            </div>

            {/* Actions */}
            <div className="row row--wrap" style={{ marginBottom: 'var(--space-2)' }}>
              {currentPath !== '.' && <Button size="sm" onClick={goUp}>Up</Button>}
              <Button size="sm" onClick={handleMkdir}>New folder</Button>
              <label className="btn btn--sm" style={{ cursor: 'pointer' }}>
                Upload
                <input type="file" onChange={handleUpload} style={{ display: 'none' }} />
              </label>
              {uploading && <span className="faint">Uploading…</span>}
            </div>

            {/* File table */}
            <div className="table-wrap">
              <table className="table">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th style={{ width: 100 }}>Size</th>
                    <th style={{ width: 160 }} />
                  </tr>
                </thead>
                <tbody>
                  {entries.map((entry) => (
                    <tr key={entry.name}>
                      <td>
                        <button
                          type="button"
                          className="file-link"
                          onClick={() => navigateTo(entry.name, entry.isDir)}
                        >
                          <span className="file-link__icon">{entry.isDir ? 'DIR' : 'FILE'}</span>
                          <span className="mono">{entry.name}</span>
                        </button>
                      </td>
                      <td className="table__cell--mono">{entry.isDir ? '—' : formatSize(entry.size || 0)}</td>
                      <td>
                        <div className="row" style={{ gap: 'var(--space-1)' }}>
                          <Button size="sm" variant="ghost" onClick={() => handleRename(entry.name)}>Rename</Button>
                          <Button size="sm" variant="danger" onClick={() => handleDeleteFile(entry.name, entry.isDir)}>Delete</Button>
                        </div>
                      </td>
                    </tr>
                  ))}
                  {entries.length === 0 && (
                    <tr>
                      <td colSpan={3} className="faint" style={{ textAlign: 'center' }}>Empty directory</td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </div>

          {/* Preview panel */}
          {previewPath && wsId && (
            <div className="files-layout__preview">
              <div className="panel">
                <div className="panel__header">
                  <span className="truncate mono" style={{ textTransform: 'none' }}>
                    {previewPath.split('/').pop()}
                  </span>
                  <span className="spacer" />
                  <Button size="sm" variant="ghost" onClick={() => setPreviewPath('')}>Close</Button>
                </div>
                <div className="panel__body">
                  <FilePreview nodeId={nodeId} wsId={wsId} branch={selectedBranch} path={previewPath} />
                </div>
              </div>
            </div>
          )}
        </div>
      )}

      {tab === 'history' && (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th style={{ width: 90 }}>ID</th>
                <th style={{ width: 120 }}>Branch</th>
                <th>Message</th>
                <th style={{ width: 180 }}>Time</th>
              </tr>
            </thead>
            <tbody>
              {commits.map((c) => (
                <tr key={c.id}>
                  <td className="table__cell--mono">{c.id?.slice(0, 8)}</td>
                  <td><span className="badge badge--accent">{c.branch}</span></td>
                  <td className="table__cell--primary">{c.message}</td>
                  <td className="table__cell--mono">
                    {c.time ? new Date(c.time * 1000).toLocaleString() : '—'}
                  </td>
                </tr>
              ))}
              {commits.length === 0 && (
                <tr>
                  <td colSpan={4} className="faint" style={{ textAlign: 'center' }}>No commits yet</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
