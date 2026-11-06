import { useEffect, useState } from 'react';
import { api } from '../../../api';
import type { ContextScopeView, ScopeIds } from '../../../api';
import Button from '../../../components/ui/Button';

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
    <div className="stack" style={{ gap: 'var(--space-3)' }}>
      <div className="notice notice--warning">
        这些文件由 SDK 的 Context API 管理。当前 Tai runtime 尚未把它们注入
        Agent 的 AGENTS.md，编辑内容不会影响对话行为。
      </div>

      {error && <div className="notice notice--error">{error}</div>}

      <div className="row">
        <Button size="sm" onClick={() => void load()} disabled={busy}>Refresh</Button>
        <Button size="sm" onClick={() => void showMerged()}>Preview Merged</Button>
      </div>

      {merged && (
        <div>
          <div className="section-title" style={{ margin: 0 }}>Merged (bot → home → board → session)</div>
          <pre className="code-block" style={{ maxHeight: 260 }}>{merged}</pre>
          <Button size="sm" variant="ghost" onClick={() => setMerged('')}>Hide</Button>
        </div>
      )}

      {SCOPES.map((scope) => {
        const id = scopeIds[scope as keyof ScopeIds] ?? '';
        const view = views.find((v) => v.scope === scope);
        return (
          <details key={scope} open={!!view} className="scope">
            <summary className="scope__summary">
              <b className="mono">{scope}</b>
              {id ? <span className="badge mono">{id}</span> : <span className="badge badge--warning">未指定 ID</span>}
            </summary>
            {FILES.map((file) => {
              const key = `${scope}/${id}/${file}`;
              return (
                <div key={file} style={{ marginTop: 'var(--space-2)' }}>
                  <div className="scope__file mono">{file}</div>
                  <textarea
                    className="textarea textarea--mono"
                    value={drafts[key] ?? ''}
                    onChange={(e) => setDrafts((d) => ({ ...d, [key]: e.target.value }))}
                    placeholder={id ? `尚未创建 ${file}` : '先在上层指定该 scope 的 ID'}
                    disabled={!id}
                    style={{ marginBottom: 'var(--space-1)' }}
                  />
                  <Button
                    size="sm"
                    onClick={() => void save(scope, id, file)}
                    disabled={!id || busy}
                  >
                    Save {file}
                  </Button>
                </div>
              );
            })}
          </details>
        );
      })}
    </div>
  );
}
