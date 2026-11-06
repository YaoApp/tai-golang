import { useEffect, useRef, useState } from 'react';
import { api } from '../../../api';
import type { SkillEntry } from '../../../api';
import Button from '../../../components/ui/Button';

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
    <div className="stack" style={{ gap: 'var(--space-3)' }}>
      <div className="notice notice--info">
        skill 包同步到工作区 <code>.skills/</code> 目录，由 SDK 的 SyncSkills 按
        hash 幂等处理。
      </div>

      {error && <div className="notice notice--error">{error}</div>}
      {message && <div className="notice notice--info">{message}</div>}

      <div className="row row--wrap">
        <input
          className="input mono"
          value={key}
          onChange={(e) => setKey(e.target.value)}
          placeholder="skill key（如 bots/postman）"
          style={{ width: 220 }}
        />
        <input ref={fileRef} type="file" accept=".tar.gz,.tgz,.zip" style={{ fontSize: 'var(--text-sm)' }} />
        <Button size="sm" variant="primary" onClick={() => void upload()} disabled={busy}>Upload</Button>
        <Button size="sm" onClick={() => void load()} disabled={busy}>Refresh</Button>
      </div>

      <div>
        <div className="section-title" style={{ margin: '0 0 var(--space-2)' }}>
          Synced skills ({skills.length})
        </div>
        {skills.length === 0 ? (
          <p className="faint" style={{ fontSize: 'var(--text-sm)' }}>No skills synced yet.</p>
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>Key</th>
                  <th>Hash</th>
                </tr>
              </thead>
              <tbody>
                {skills.map((s) => (
                  <tr key={s.key}>
                    <td className="table__cell--primary mono">{s.key}</td>
                    <td className="table__cell--mono">{s.hash.slice(0, 22)}…</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
