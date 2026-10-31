import { useState } from 'react';
import { api } from '../../../api';

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
    ? `tai start --node-id ${nodeId} --server http://localhost:5099 --node-token ${token}`
    : '';

  return (
    <div style={{ marginTop: 20 }}>
      <h3>Generate Node Token</h3>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
        <input
          value={nodeId}
          onChange={(e) => setNodeId(e.target.value)}
          placeholder="Node ID"
          style={{ padding: '6px 10px', border: '1px solid #ccc', borderRadius: 4, width: 200 }}
        />
        <button onClick={generate} style={btnStyle}>
          Generate
        </button>
      </div>
      {error && <p style={{ color: 'red' }}>{error}</p>}
      {token && (
        <div style={{ marginTop: 12 }}>
          <label style={{ fontWeight: 600 }}>Token:</label>
          <pre style={preStyle}>{token}</pre>
          <label style={{ fontWeight: 600, marginTop: 8, display: 'block' }}>
            tai start command:
          </label>
          <pre
            style={{ ...preStyle, cursor: 'pointer' }}
            onClick={() => navigator.clipboard.writeText(taiCommand)}
            title="Click to copy"
          >
            {taiCommand}
          </pre>
        </div>
      )}
    </div>
  );
}

const btnStyle: React.CSSProperties = {
  padding: '6px 16px',
  border: 'none',
  borderRadius: 4,
  background: '#0066cc',
  color: '#fff',
  cursor: 'pointer',
};

const preStyle: React.CSSProperties = {
  background: '#f1f3f5',
  padding: '8px 12px',
  borderRadius: 4,
  fontSize: 12,
  overflowX: 'auto',
  wordBreak: 'break-all',
  whiteSpace: 'pre-wrap',
};
