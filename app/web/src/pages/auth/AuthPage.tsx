import { useEffect, useState } from 'react';
import { api } from '../../api';
import type { AuthInfo } from '../../api';
import TokenGenerator from './components/TokenGenerator';
import PageHeader from '../../components/ui/PageHeader';
import Panel from '../../components/ui/Panel';

export default function AuthPage() {
  const [info, setInfo] = useState<AuthInfo | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    api.auth.info().then(setInfo).catch((e) => setError(e.message));
  }, []);

  return (
    <div style={{ maxWidth: 900 }}>
      <PageHeader title="Auth" subtitle="Server identity, node token and user credential issuance" />

      {error && <div className="notice notice--error">{error}</div>}

      {info && (
        <div style={{ marginBottom: 'var(--space-4)' }}>
          <Panel title="Server info">
            <dl className="kv">
              <dt>Transport Port</dt>
              <dd className="mono">{info.transportPort}</dd>
              <dt>Protocols</dt>
              <dd className="mono">{info.protocols?.join(', ')}</dd>
              <dt>Protocol Version</dt>
              <dd className="mono">{info.version}</dd>
              <dt>Public Key ID</dt>
              <dd className="mono">{info.publicKeyId}</dd>
              <dt>Public Key</dt>
              <dd>
                <pre className="code-block" style={{ margin: 0 }}>
                  {info.publicKey}
                </pre>
              </dd>
            </dl>
          </Panel>
        </div>
      )}

      <TokenGenerator />
    </div>
  );
}
