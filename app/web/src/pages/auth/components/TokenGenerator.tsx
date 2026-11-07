import { useState } from 'react';
import { api } from '../../../api';
import type { UserIdentity } from '../../../api';
import Button from '../../../components/ui/Button';

/**
 * Scope actions a demo credential can be issued for.
 *
 * Mirrors the constants in tai-golang/types (identity.md §2.3). There is no
 * global wildcard: cross-resource authority must list each action explicitly.
 */
const SCOPE_OPTIONS: { value: string; hint: string }[] = [
  { value: 'session:access', hint: 'open / resume a session' },
  { value: 'workspace:read', hint: 'read files, list dirs, git history' },
  { value: 'workspace:write', hint: 'write files, mkdir, rename, commit' },
  { value: 'workspace:delete', hint: 'delete files/dirs, rollback' },
  { value: 'remote_tool:call', hint: 'call remote tools' },
];

/** Default: everything a demo session needs. */
const DEFAULT_SCOPES = SCOPE_OPTIONS.map((s) => s.value);

export default function TokenGenerator() {
  const [nodeId, setNodeId] = useState('demo-node');

  // --- Node token (unchanged v1 connection credential) ---
  const [token, setToken] = useState('');
  const [error, setError] = useState('');

  // --- User token (v2 identity credential) ---
  const [scopes, setScopes] = useState<string[]>(DEFAULT_SCOPES);
  const [userToken, setUserToken] = useState('');
  const [identity, setIdentity] = useState<UserIdentity | null>(null);
  const [userError, setUserError] = useState('');

  const generateNodeToken = async () => {
    try {
      setError('');
      const res = await api.auth.issueNodeToken(nodeId);
      setToken(res.token);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed');
    }
  };

  const toggleScope = (scope: string) => {
    setScopes((prev) =>
      prev.includes(scope) ? prev.filter((s) => s !== scope) : [...prev, scope],
    );
  };

  const generateUserToken = async () => {
    try {
      setUserError('');
      const res = await api.auth.issueUserToken(nodeId, scopes);
      setUserToken(res.token);
      setIdentity(res.identity);
    } catch (e: unknown) {
      setIdentity(null);
      setUserError(e instanceof Error ? e.message : 'Failed');
    }
  };

  const taiCommand = token
    ? `tai start --node-id ${nodeId} --server http://localhost:5099 --node-token ${token} --data ./tai-volumns`
    : '';

  return (
    <div className="stack" style={{ gap: 'var(--space-4)' }}>
      <div>
        <h3 className="section-title" style={{ fontSize: 'var(--text-xs)' }}>Generate Node Token</h3>
        <div className="panel">
          <div className="panel__body">
            <div className="row row--wrap">
              <input
                value={nodeId}
                onChange={(e) => setNodeId(e.target.value)}
                placeholder="Node ID"
                className="input mono"
                style={{ width: 220 }}
              />
              <Button variant="primary" onClick={generateNodeToken}>Generate</Button>
              {token && <span className="badge badge--success">issued</span>}
            </div>

            {error && <div className="notice notice--error" style={{ marginTop: 'var(--space-3)' }}>{error}</div>}

            {token && (
              <div className="stack" style={{ gap: 'var(--space-1)', marginTop: 'var(--space-4)' }}>
                <label className="field__label">Token:</label>
                <pre className="code-block" style={{ margin: 0 }}>{token}</pre>

                <label className="field__label" style={{ marginTop: 'var(--space-2)' }}>
                  tai start command:
                </label>
                <pre
                  className="code-block"
                  style={{ margin: 0, cursor: 'pointer' }}
                  onClick={() => void navigator.clipboard.writeText(taiCommand)}
                  title="Click to copy"
                >
                  {taiCommand}
                </pre>
                <span className="faint" style={{ fontSize: 'var(--text-xs)' }}>
                  点击命令块复制
                </span>
              </div>
            )}
          </div>
        </div>
      </div>

      <div>
        <div className="section-title" style={{ fontSize: 'var(--text-xs)' }}>User token</div>
        <div className="panel">
          <div className="panel__body">
            <div className="faint" style={{ fontSize: 'var(--text-xs)' }}>
              Issued for node <span className="mono">{nodeId || '—'}</span> — the server resolves
              the user from its business context and signs the requested scopes.
            </div>

            <div className="field__label" style={{ marginTop: 'var(--space-3)' }}>
              Scopes:
            </div>
            <div className="row row--wrap" style={{ gap: 'var(--space-3)' }}>
              {SCOPE_OPTIONS.map((s) => (
                <label key={s.value} className="row" style={{ gap: 'var(--space-1)' }} title={s.hint}>
                  <input
                    type="checkbox"
                    checked={scopes.includes(s.value)}
                    onChange={() => toggleScope(s.value)}
                  />
                  <span className="mono" style={{ fontSize: 'var(--text-sm)' }}>{s.value}</span>
                </label>
              ))}
            </div>

            <div className="row" style={{ marginTop: 'var(--space-3)' }}>
              <Button variant="primary" onClick={generateUserToken}>Issue user token</Button>
              {userToken && identity && <span className="badge badge--success">issued</span>}
            </div>

            {userError && <div className="notice notice--error" style={{ marginTop: 'var(--space-3)' }}>{userError}</div>}

            {userToken && identity && (
              <div className="stack" style={{ gap: 'var(--space-1)', marginTop: 'var(--space-4)' }}>
                <label className="field__label">Token:</label>
                <pre className="code-block" style={{ margin: 0 }}>{userToken}</pre>

                <label className="field__label" style={{ marginTop: 'var(--space-2)' }}>
                  Identity:
                </label>
                <dl className="kv">
                  <dt>user_id</dt>
                  <dd className="mono">{identity.user_id || '—'}</dd>
                  <dt>team_id</dt>
                  <dd className="mono">{identity.team_id || '—'}</dd>
                  <dt>scope</dt>
                  <dd className="mono">{identity.scope?.length ? identity.scope.join(', ') : '—'}</dd>
                </dl>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
