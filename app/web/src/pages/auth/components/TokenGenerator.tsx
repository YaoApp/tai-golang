import { useState } from 'react';
import { api } from '../../../api';
import Button from '../../../components/ui/Button';

export default function TokenGenerator() {
  const [nodeId, setNodeId] = useState('demo-node');
  const [token, setToken] = useState('');
  const [error, setError] = useState('');

  const generate = async () => {
    try {
      setError('');
      const res = await api.auth.issueNodeToken(nodeId);
      setToken(res.token);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed');
    }
  };

  const taiCommand = token
    ? `tai start --node-id ${nodeId} --server http://localhost:5099 --node-token ${token} --data ./tai-volumns`
    : '';

  return (
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
            <Button variant="primary" onClick={generate}>Generate</Button>
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
  );
}
