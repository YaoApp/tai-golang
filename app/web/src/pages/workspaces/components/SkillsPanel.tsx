import { useEffect, useRef, useState } from 'react';
import { api } from '../../../api';
import type { SkillEntry } from '../../../api';

interface Props {
  nodeId: string;
  wsId: string;
}

/**
 * Skills panel: inspect and upload workspace skill packages.
 *
 * The backend syncs idempotently by hash, so unchanged packages report
 * pushed=0.
 */
export default function SkillsPanel({ nodeId, wsId }: Props) {
  const [skills, setSkills] = useState<SkillEntry[]>([]);
  const [key, setKey] = useState('');
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');
  const [busy, setBusy] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  const load = async () => {
    try {
      setError('');
      const r = await api.workspaces.skills.list(nodeId, wsId);
      setSkills(r.skills ?? []);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'load failed');
    }
  };

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nodeId, wsId]);

  const upload = async () => {
    const file = fileRef.current?.files?.[0];
    if (!key.trim()) {
      setError('请填写 skill key（如 bots/postman）');
      return;
    }
    if (!file) {
      setError('请选择 tar.gz 或 zip 归档');
      return;
    }
    setBusy(true);
    try {
      setError('');
      setMessage('');
      const r = await api.workspaces.skills.upload(nodeId, wsId, key.trim(), file);
      setMessage(r.pushed > 0 ? `已同步 ${r.key}` : `${r.key} 内容未变，跳过`);
      setKey('');
      if (fileRef.current) fileRef.current.value = '';
      await load();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'upload failed');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <div style={notice}>
        skill 包同步到工作区 <code>.skills/</code> 目录，由 SDK 的 SyncSkills 按
        hash 幂等处理。
      </div>

      {error && <div style={errorBox}>{error}</div>}
      {message && <div style={okBox}>{message}</div>}

      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
        <input
          value={key}
          onChange={(e) => setKey(e.target.value)}
          placeholder="skill key（如 bots/postman）"
          style={{ ...input, width: 220 }}
        />
        <input ref={fileRef} type="file" accept=".tar.gz,.tgz,.zip" style={{ fontSize: 12 }} />
        <button onClick={() => void upload()} disabled={busy} style={btn}>
          Upload
        </button>
        <button onClick={() => void load()} disabled={busy} style={btn}>
          Refresh
        </button>
      </div>

      <div>
        <div style={sectionTitle}>Synced skills ({skills.length})</div>
        {skills.length === 0 ? (
          <p style={{ color: '#999', fontSize: 12 }}>No skills synced yet.</p>
        ) : (
          <table style={{ width: '100%', fontSize: 12, borderCollapse: 'collapse' }}>
            <thead>
              <tr>
                <th style={th}>Key</th>
                <th style={th}>Hash</th>
              </tr>
            </thead>
            <tbody>
              {skills.map((s) => (
                <tr key={s.key}>
                  <td style={td}>{s.key}</td>
                  <td style={{ ...td, fontFamily: 'monospace', color: '#666' }}>
                    {s.hash.slice(0, 22)}…
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

// ---------- styles ----------

const notice: React.CSSProperties = {
  padding: '8px 12px',
  background: '#eef4ff',
  border: '1px solid #cfe0ff',
  borderRadius: 6,
  fontSize: 12,
  color: '#1c4b9c',
};

const errorBox: React.CSSProperties = {
  padding: '8px 12px',
  background: '#fdecea',
  border: '1px solid #f5c6cb',
  borderRadius: 6,
  fontSize: 12,
  color: '#a71d2a',
};

const okBox: React.CSSProperties = {
  padding: '8px 12px',
  background: '#e8f5e9',
  border: '1px solid #c8e6c9',
  borderRadius: 6,
  fontSize: 12,
  color: '#1b5e20',
};

const sectionTitle: React.CSSProperties = {
  fontSize: 12,
  fontWeight: 600,
  color: '#444',
  marginBottom: 6,
};

const input: React.CSSProperties = {
  padding: '4px 8px',
  fontSize: 12,
  border: '1px solid #ddd',
  borderRadius: 4,
  boxSizing: 'border-box',
};

const th: React.CSSProperties = {
  textAlign: 'left',
  padding: '4px 8px',
  borderBottom: '1px solid #e0e0e0',
  color: '#666',
  fontWeight: 600,
};

const td: React.CSSProperties = {
  padding: '4px 8px',
  borderBottom: '1px solid #f0f0f0',
};

const btn: React.CSSProperties = {
  padding: '4px 10px',
  fontSize: 12,
  border: '1px solid #ccc',
  borderRadius: 4,
  background: '#fff',
  cursor: 'pointer',
};
