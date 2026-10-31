import { useEffect, useState } from 'react';
import { api } from '../../api';
import type { AuthInfo } from '../../api';
import TokenGenerator from './components/TokenGenerator';

export default function AuthPage() {
  const [info, setInfo] = useState<AuthInfo | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    api.auth.info().then(setInfo).catch((e) => setError(e.message));
  }, []);

  return (
    <div>
      <h1>Auth</h1>
      {error && <p style={{ color: 'red' }}>{error}</p>}
      {info && (
        <table style={tableStyle}>
          <tbody>
            <tr><td style={tdLabel}>Transport Port</td><td>{info.transportPort}</td></tr>
            <tr><td style={tdLabel}>Protocols</td><td>{info.protocols?.join(', ')}</td></tr>
            <tr><td style={tdLabel}>Version</td><td>{info.version}</td></tr>
            <tr><td style={tdLabel}>Public Key ID</td><td style={{ fontSize: 12 }}>{info.publicKeyId}</td></tr>
            <tr>
              <td style={tdLabel}>Public Key</td>
              <td>
                <pre style={{ fontSize: 11, margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
                  {info.publicKey}
                </pre>
              </td>
            </tr>
          </tbody>
        </table>
      )}
      <TokenGenerator />
    </div>
  );
}

const tableStyle: React.CSSProperties = {
  borderCollapse: 'collapse',
  width: '100%',
  maxWidth: 700,
};

const tdLabel: React.CSSProperties = {
  fontWeight: 600,
  padding: '6px 12px 6px 0',
  verticalAlign: 'top',
  whiteSpace: 'nowrap',
  width: 140,
};
