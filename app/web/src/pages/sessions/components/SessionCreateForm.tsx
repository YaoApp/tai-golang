import { useState } from 'react';
import type { SessionRequest, McpServer, ProvidersMatrix } from '../../../types';
import type { WorkspaceEntry } from '../../../api';

interface Props {
  nodes: { id: string }[];
  currentNode: string;
  onNodeChange: (nodeId: string) => void;
  workspaces: WorkspaceEntry[];
  onSubmit: (nodeId: string, req: SessionRequest) => void;
  disabled?: boolean;
}

const RUNTIMES = ['tai', 'dsh', 'claude', 'codex'];
const ISOLATIONS = ['auto', 'host', 'container'];

const defaultLLM: ProvidersMatrix = {
  light: { provider_id: 'deepseek', model: 'deepseek-flash' },
  default: { provider_id: 'deepseek', model: 'deepseek-flash' },
  realtime: { provider_id: 'openai', model: 'gpt-realtime-mini' },
};

export default function SessionCreateForm({ nodes, currentNode, onNodeChange, workspaces, onSubmit, disabled }: Props) {
  const [runtime, setRuntime] = useState('tai');
  const [isolation, setIsolation] = useState('auto');
  const [showAdvanced, setShowAdvanced] = useState(false);

  // Advanced fields
  const [workspaceId, setWorkspaceId] = useState('');
  const [llm, setLlm] = useState<ProvidersMatrix>({ ...defaultLLM });
  const [assistantId, setAssistantId] = useState('');
  const [cwd, setCwd] = useState('');
  const [additionalDirs, setAdditionalDirs] = useState<string[]>([]);
  const [mcpServers, setMcpServers] = useState<McpServer[]>([]);
  const [envVars, setEnvVars] = useState<{ key: string; value: string }[]>([]);
  const [idleTimeout, setIdleTimeout] = useState('');
  const [maxDuration, setMaxDuration] = useState('');
  const [configJson, setConfigJson] = useState('');
  const [metadata, setMetadata] = useState<{ key: string; value: string }[]>([]);

  const handleSubmit = () => {
    const nodeId = currentNode || nodes[0]?.id;
    if (!nodeId) return;

    const req: SessionRequest = { runtime, isolation };
    if (workspaceId) req.workspaceId = workspaceId;

    // LLM
    const hasLlm = Object.values(llm).some((p) => p && (p.provider_id || p.model));
    if (hasLlm) req.llm = llm;

    // Agent config
    if (assistantId) req.assistantId = assistantId;
    if (cwd) req.cwd = cwd;
    if (additionalDirs.length > 0) req.additionalDirectories = additionalDirs.filter(Boolean);
    if (mcpServers.length > 0) req.mcpServers = mcpServers;

    // Env
    const envMap: Record<string, string> = {};
    envVars.forEach((e) => { if (e.key) envMap[e.key] = e.value; });
    if (Object.keys(envMap).length > 0) req.env = envMap;

    // Timeouts
    if (idleTimeout) req.idleTimeout = parseInt(idleTimeout, 10);
    if (maxDuration) req.maxDuration = parseInt(maxDuration, 10);

    // Config
    if (configJson.trim()) {
      try { req.config = JSON.parse(configJson); } catch { /* skip invalid */ }
    }
    const metaMap: Record<string, string> = {};
    metadata.forEach((m) => { if (m.key) metaMap[m.key] = m.value; });
    if (Object.keys(metaMap).length > 0) req.metadata = metaMap;

    onSubmit(nodeId, req);
  };

  return (
    <div style={formContainer}>
      {/* Required fields */}
      <div style={row}>
        <Field label="Node">
          <select value={currentNode} onChange={(e) => onNodeChange(e.target.value)} style={selectStyle}>
            <option value="">Select node...</option>
            {nodes.map((n) => <option key={n.id} value={n.id}>{n.id}</option>)}
          </select>
        </Field>
        <Field label="Runtime">
          <select value={runtime} onChange={(e) => setRuntime(e.target.value)} style={selectStyle}>
            {RUNTIMES.map((r) => <option key={r} value={r}>{r}</option>)}
          </select>
        </Field>
        <Field label="Isolation">
          <select value={isolation} onChange={(e) => setIsolation(e.target.value)} style={selectStyle}>
            {ISOLATIONS.map((i) => <option key={i} value={i}>{i}</option>)}
          </select>
        </Field>
        <button onClick={handleSubmit} disabled={disabled || !currentNode} style={createBtn(!!currentNode && !disabled)}>
          Create Session
        </button>
      </div>

      {/* Advanced toggle */}
      <button
        onClick={() => setShowAdvanced(!showAdvanced)}
        style={{ ...toggleBtn, marginTop: 8 }}
      >
        {showAdvanced ? '▼' : '▶'} Advanced Options
      </button>

      {showAdvanced && (
        <div style={advancedBox}>
          {/* LLM Provider Matrix */}
          <Section title="LLM Provider Matrix">
            <table style={tableStyle}>
              <thead>
                <tr>
                  <th style={thStyle}>Role</th>
                  <th style={thStyle}>Provider</th>
                  <th style={thStyle}>Model</th>
                </tr>
              </thead>
              <tbody>
                {(['light', 'default', 'realtime'] as const).map((role) => (
                  <tr key={role}>
                    <td style={tdStyle}><strong>{role}</strong></td>
                    <td style={tdStyle}>
                      <input
                        value={llm[role]?.provider_id ?? ''}
                        onChange={(e) => setLlm({ ...llm, [role]: { ...llm[role]!, provider_id: e.target.value } })}
                        placeholder={role === 'realtime' ? 'openai' : 'deepseek'}
                        style={inputSmall}
                      />
                    </td>
                    <td style={tdStyle}>
                      <input
                        value={llm[role]?.model ?? ''}
                        onChange={(e) => setLlm({ ...llm, [role]: { ...llm[role]!, model: e.target.value } })}
                        placeholder={role === 'realtime' ? 'gpt-realtime-mini' : 'deepseek-flash'}
                        style={inputSmall}
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            <p style={hint}>API Key 和 Base URL 通过启动环境变量注入，不在此输入。</p>
          </Section>

          {/* Workspace */}
          <Section title="Workspace">
            <select value={workspaceId} onChange={(e) => setWorkspaceId(e.target.value)} style={selectStyle}>
              <option value="">Auto-create workspace</option>
              {workspaces.map((ws) => (
                <option key={ws.id} value={ws.id}>{ws.name || ws.id.slice(0, 12)}</option>
              ))}
            </select>
          </Section>

          {/* Agent Configuration */}
          <Section title="Agent Configuration">
            <Field label="AssistantID">
              <input value={assistantId} onChange={(e) => setAssistantId(e.target.value)} placeholder="Optional" style={inputStyle} />
            </Field>
            <Field label="Cwd">
              <input value={cwd} onChange={(e) => setCwd(e.target.value)} placeholder="/path/to/workdir" style={inputStyle} />
            </Field>
            <Field label="Additional Directories">
              <DynamicStringList items={additionalDirs} onChange={setAdditionalDirs} placeholder="/path" />
            </Field>
          </Section>

          {/* MCP Servers */}
          <Section title="MCP Servers">
            {mcpServers.map((srv, i) => (
              <McpServerEntry
                key={i}
                server={srv}
                onChange={(s) => { const next = [...mcpServers]; next[i] = s; setMcpServers(next); }}
                onRemove={() => setMcpServers(mcpServers.filter((_, j) => j !== i))}
              />
            ))}
            <button onClick={() => setMcpServers([...mcpServers, { type: 'stdio', name: '' }])} style={addBtn}>
              + Add MCP Server
            </button>
          </Section>

          {/* Environment Variables */}
          <Section title="Environment Variables">
            <KeyValueEditor items={envVars} onChange={setEnvVars} />
          </Section>

          {/* Timeouts */}
          <Section title="Timeouts">
            <div style={row}>
              <Field label="IdleTimeout (sec)">
                <input type="number" value={idleTimeout} onChange={(e) => setIdleTimeout(e.target.value)} placeholder="(empty)" style={inputSmall} />
              </Field>
              <Field label="MaxDuration (sec)">
                <input type="number" value={maxDuration} onChange={(e) => setMaxDuration(e.target.value)} placeholder="(empty)" style={inputSmall} />
              </Field>
            </div>
          </Section>

          {/* Raw JSON / Metadata */}
          <Section title="Config / Metadata">
            <Field label="Config (JSON)">
              <textarea
                value={configJson}
                onChange={(e) => setConfigJson(e.target.value)}
                placeholder='{ "key": "value" }'
                style={{ ...inputStyle, minHeight: 60, fontFamily: 'monospace', fontSize: 12 }}
              />
            </Field>
            <Field label="Metadata">
              <KeyValueEditor items={metadata} onChange={setMetadata} />
            </Field>
          </Section>
        </div>
      )}
    </div>
  );
}

// --- Sub-components ---

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 2, fontSize: 12 }}>
      <span style={{ fontWeight: 600, color: '#555' }}>{label}</span>
      {children}
    </label>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div style={{ marginBottom: 12 }}>
      <h4 style={{ margin: '0 0 6px', fontSize: 13, color: '#333', borderBottom: '1px solid #eee', paddingBottom: 4 }}>{title}</h4>
      {children}
    </div>
  );
}

function DynamicStringList({ items, onChange, placeholder }: { items: string[]; onChange: (v: string[]) => void; placeholder: string }) {
  return (
    <div>
      {items.map((item, i) => (
        <div key={i} style={{ display: 'flex', gap: 4, marginBottom: 4 }}>
          <input
            value={item}
            onChange={(e) => { const next = [...items]; next[i] = e.target.value; onChange(next); }}
            placeholder={placeholder}
            style={{ ...inputSmall, flex: 1 }}
          />
          <button onClick={() => onChange(items.filter((_, j) => j !== i))} style={removeBtn}>×</button>
        </div>
      ))}
      <button onClick={() => onChange([...items, ''])} style={addBtn}>+ Add</button>
    </div>
  );
}

function KeyValueEditor({ items, onChange }: { items: { key: string; value: string }[]; onChange: (v: { key: string; value: string }[]) => void }) {
  return (
    <div>
      {items.map((item, i) => (
        <div key={i} style={{ display: 'flex', gap: 4, marginBottom: 4 }}>
          <input
            value={item.key}
            onChange={(e) => { const next = [...items]; next[i] = { ...next[i], key: e.target.value }; onChange(next); }}
            placeholder="Key"
            style={{ ...inputSmall, flex: 1 }}
          />
          <input
            value={item.value}
            onChange={(e) => { const next = [...items]; next[i] = { ...next[i], value: e.target.value }; onChange(next); }}
            placeholder="Value"
            style={{ ...inputSmall, flex: 2 }}
          />
          <button onClick={() => onChange(items.filter((_, j) => j !== i))} style={removeBtn}>×</button>
        </div>
      ))}
      <button onClick={() => onChange([...items, { key: '', value: '' }])} style={addBtn}>+ Add</button>
    </div>
  );
}

function McpServerEntry({ server, onChange, onRemove }: { server: McpServer; onChange: (s: McpServer) => void; onRemove: () => void }) {
  return (
    <div style={{ border: '1px solid #ddd', borderRadius: 4, padding: 8, marginBottom: 8 }}>
      <div style={{ display: 'flex', gap: 8, marginBottom: 6, alignItems: 'center' }}>
        <select
          value={server.type}
          onChange={(e) => onChange({ ...server, type: e.target.value as 'stdio' | 'http' })}
          style={selectSmall}
        >
          <option value="stdio">stdio</option>
          <option value="http">http</option>
        </select>
        <input
          value={server.name}
          onChange={(e) => onChange({ ...server, name: e.target.value })}
          placeholder="Server name"
          style={{ ...inputSmall, flex: 1 }}
        />
        <button onClick={onRemove} style={removeBtn}>×</button>
      </div>
      {server.type === 'stdio' ? (
        <>
          <Field label="Command">
            <input value={server.command ?? ''} onChange={(e) => onChange({ ...server, command: e.target.value })} style={inputSmall} />
          </Field>
          <Field label="Args (comma-separated)">
            <input
              value={(server.args ?? []).join(', ')}
              onChange={(e) => onChange({ ...server, args: e.target.value.split(',').map((s) => s.trim()).filter(Boolean) })}
              style={inputSmall}
            />
          </Field>
        </>
      ) : (
        <Field label="URL">
          <input value={server.url ?? ''} onChange={(e) => onChange({ ...server, url: e.target.value })} placeholder="http://..." style={inputSmall} />
        </Field>
      )}
    </div>
  );
}

// --- Styles ---

const formContainer: React.CSSProperties = { marginBottom: 16 };

const row: React.CSSProperties = { display: 'flex', gap: 8, alignItems: 'flex-end', flexWrap: 'wrap' };

const selectStyle: React.CSSProperties = { padding: '6px 10px', borderRadius: 4, border: '1px solid #ccc', fontSize: 13 };

const selectSmall: React.CSSProperties = { ...selectStyle, padding: '4px 8px', fontSize: 12 };

const inputStyle: React.CSSProperties = { padding: '6px 10px', borderRadius: 4, border: '1px solid #ccc', fontSize: 13, width: '100%' };

const inputSmall: React.CSSProperties = { padding: '4px 8px', borderRadius: 4, border: '1px solid #ccc', fontSize: 12 };

const createBtn = (ok: boolean): React.CSSProperties => ({
  padding: '6px 16px', border: 'none', borderRadius: 4,
  background: ok ? '#0066cc' : '#ccc', color: '#fff',
  cursor: ok ? 'pointer' : 'default', fontWeight: 600, fontSize: 13, whiteSpace: 'nowrap',
});

const toggleBtn: React.CSSProperties = {
  background: 'none', border: 'none', cursor: 'pointer',
  fontSize: 12, color: '#0066cc', padding: 0,
};

const advancedBox: React.CSSProperties = {
  marginTop: 8, padding: 12, border: '1px solid #e0e0e0',
  borderRadius: 6, background: '#f9f9f9',
};

const addBtn: React.CSSProperties = {
  background: 'none', border: '1px dashed #aaa', borderRadius: 4,
  padding: '4px 12px', cursor: 'pointer', fontSize: 11, color: '#666',
};

const removeBtn: React.CSSProperties = {
  background: 'none', border: '1px solid #ddd', borderRadius: 3,
  padding: '2px 6px', cursor: 'pointer', fontSize: 12, color: '#999',
};

const tableStyle: React.CSSProperties = { borderCollapse: 'collapse', width: '100%', fontSize: 12 };

const thStyle: React.CSSProperties = { textAlign: 'left', padding: '4px 8px', borderBottom: '1px solid #ddd', fontWeight: 600, color: '#555' };

const tdStyle: React.CSSProperties = { padding: '4px 8px', borderBottom: '1px solid #eee' };

const hint: React.CSSProperties = { margin: '4px 0 0', fontSize: 11, color: '#888', fontStyle: 'italic' };
