import { useEffect, useState } from 'react';
import { api } from '../../../api';
import type { ContextScopeView, ScopeIds } from '../../../api';

interface Props {
  nodeId: string;
  wsId: string;
  /** Per-scope IDs, derived by the parent from session/workspace info. */
  scopeIds: ScopeIds;
}

/** Fixed scope order, matching the backend. */
const SCOPES = ['bot', 'home', 'board', 'session'] as const;

/** Standard files inside each scope. */
const FILES = ['INSTRUCTION.md', 'MEMORY.md', 'PREFERENCE.md'] as const;

/**
 * Context panel: view and edit the four scopes' Context files.
 *
 * The files are managed by the SDK's Context API (INSTRUCTION / MEMORY /
 * PREFERENCE), but the Tai runtime does not inject them into the agent's
 * AGENTS.md today.
 */
export default function ContextPanel({ nodeId, wsId, scopeIds }: Props) {
  const [views, setViews] = useState<ContextScopeView[]>([]);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [merged, setMerged] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const load = async () => {
    try {
      setError('');
      const r = await api.workspaces.context.read(nodeId, wsId, scopeIds);
      setViews(r.scopes);
      // Seed drafts from the server content.
      const d: Record<string, string> = {};
      for (const v of r.scopes) {
        for (const f of FILES) {
          d[`${v.scope}/${v.id}/${f}`] = v.files[f] ?? '';
        }
      }
      setDrafts(d);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'load failed');
    }
  };

  useEffect(() => {
    void load();
    // scopeIds is an object literal; depend on its JSON form so the fetch
    // does not re-run on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nodeId, wsId, JSON.stringify(scopeIds)]);

  const save = async (scope: string, scopeID: string, file: string) => {
    if (!scopeID) {
      setError(`scope ${scope} 未指定 ID，无法保存`);
      return;
    }
    setBusy(true);
    try {
      setError('');
      await api.workspaces.context.writeFile(
        nodeId, wsId, scope, scopeID, file, drafts[`${scope}/${scopeID}/${file}`] ?? '',
      );
      await load();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'save failed');
    } finally {
      setBusy(false);
    }
  };

  const showMerged = async () => {
    try {
      setError('');
      const r = await api.workspaces.context.merged(nodeId, wsId, scopeIds);
      setMerged(r.merged);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'merge failed');
    }
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <div style={notice}>
        这些文件由 SDK 的 Context API 管理。当前 Tai runtime 尚未把它们注入
        Agent 的 AGENTS.md，编辑内容不会影响对话行为。
      </div>

      {error && <div style={errorBox}>{error}</div>}

      <div style={{ display: 'flex', gap: 8 }}>
        <button onClick={() => void load()} style={btn} disabled={busy}>Refresh</button>
        <button onClick={() => void showMerged()} style={btn}>Preview Merged</button>
      </div>

      {merged && (
        <div>
          <div style={sectionTitle}>Merged (bot → home → board → session)</div>
          <pre style={pre}>{merged}</pre>
          <button onClick={() => setMerged('')} style={btn}>Hide</button>
        </div>
      )}

      {SCOPES.map((scope) => {
        const id = scopeIds[scope as keyof ScopeIds] ?? '';
        const view = views.find((v) => v.scope === scope);
        return (
          <details key={scope} open={!!view} style={scopeBox}>
            <summary style={summary}>
              <b>{scope}</b>
              {id ? <span style={idLabel}>{id}</span> : <span style={missingLabel}>未指定 ID</span>}
            </summary>
            {FILES.map((file) => {
              const key = `${scope}/${id}/${file}`;
              return (
                <div key={file} style={{ marginTop: 8 }}>
                  <div style={fileLabel}>{file}</div>
                  <textarea
                    value={drafts[key] ?? ''}
                    onChange={(e) => setDrafts((d) => ({ ...d, [key]: e.target.value }))}
                    placeholder={id ? `尚未创建 ${file}` : '先在上层指定该 scope 的 ID'}
                    disabled={!id}
                    style={textarea}
                  />
                  <button
                    onClick={() => void save(scope, id, file)}
                    disabled={!id || busy}
                    style={btn}
                  >
                    Save {file}
                  </button>
                </div>
              );
            })}
          </details>
        );
      })}
    </div>
  );
}

// ---------- styles ----------

const notice: React.CSSProperties = {
  padding: '8px 12px',
  background: '#fff8e1',
  border: '1px solid #ffe0a3',
  borderRadius: 6,
  fontSize: 12,
  color: '#7a5c00',
};

const errorBox: React.CSSProperties = {
  padding: '8px 12px',
  background: '#fdecea',
  border: '1px solid #f5c6cb',
  borderRadius: 6,
  fontSize: 12,
  color: '#a71d2a',
};

const scopeBox: React.CSSProperties = {
  border: '1px solid #e0e0e0',
  borderRadius: 6,
  padding: '8px 12px',
};

const summary: React.CSSProperties = {
  cursor: 'pointer',
  fontSize: 13,
  display: 'flex',
  alignItems: 'center',
  gap: 8,
};

const idLabel: React.CSSProperties = {
  fontSize: 11,
  color: '#666',
  fontFamily: 'monospace',
};

const missingLabel: React.CSSProperties = {
  fontSize: 11,
  color: '#999',
};

const sectionTitle: React.CSSProperties = {
  fontSize: 12,
  fontWeight: 600,
  color: '#444',
  marginBottom: 4,
};

const fileLabel: React.CSSProperties = {
  fontSize: 11,
  color: '#666',
  marginBottom: 2,
  fontFamily: 'monospace',
};

const textarea: React.CSSProperties = {
  width: '100%',
  minHeight: 72,
  fontFamily: 'monospace',
  fontSize: 12,
  padding: 6,
  border: '1px solid #ddd',
  borderRadius: 4,
  marginBottom: 4,
  boxSizing: 'border-box',
};

const pre: React.CSSProperties = {
  background: '#f7f7f7',
  border: '1px solid #e0e0e0',
  borderRadius: 6,
  padding: 10,
  fontSize: 11,
  maxHeight: 260,
  overflow: 'auto',
  whiteSpace: 'pre-wrap',
  marginBottom: 4,
};

const btn: React.CSSProperties = {
  padding: '4px 10px',
  fontSize: 12,
  border: '1px solid #ccc',
  borderRadius: 4,
  background: '#fff',
  cursor: 'pointer',
};
