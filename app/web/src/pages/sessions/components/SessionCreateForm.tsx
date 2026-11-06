import { useState } from 'react';
import type { SessionRequest, McpServer, ProvidersMatrix } from '../../../types';
import type { WorkspaceEntry } from '../../../api';
import Button from '../../../components/ui/Button';
import Caret from '../../../components/ui/Caret';

interface Props {
  nodes: { id: string }[];
  currentNode: string;
  onNodeChange: (nodeId: string) => void;
  workspaces: WorkspaceEntry[];
  onSubmit: (nodeId: string, req: SessionRequest) => void;
  /** Reports validation failures to the page so one error area covers both. */
  onError: (message: string) => void;
  disabled?: boolean;
}

const RUNTIMES = ['tai', 'dsh', 'claude', 'codex'];
const ISOLATIONS = ['auto', 'host', 'container'];

const defaultLLM: ProvidersMatrix = {
  light: { provider_id: 'deepseek', model: 'deepseek-flash' },
  default: { provider_id: 'deepseek', model: 'deepseek-flash' },
  realtime: { provider_id: 'openai', model: 'gpt-realtime-mini' },
};

export default function SessionCreateForm({ nodes, currentNode, onNodeChange, workspaces, onSubmit, onError, disabled }: Props) {
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
    if (!nodeId) {
      // Surfacing this instead of returning silently: a dead button with no
      // explanation is the most common confusion when the stack is down.
      onError('No nodes available. Start a Tai node and wait for it to register.');
      return;
    }
    onError('');

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
    <div className="form-panel">
      {/* Required fields */}
      <div className="form-grid">
        <Field label="Node">
          <select className="select" value={currentNode} onChange={(e) => onNodeChange(e.target.value)}>
            <option value="">Select node…</option>
            {nodes.map((n) => <option key={n.id} value={n.id}>{n.id}</option>)}
          </select>
        </Field>
        <Field label="Runtime">
          <select className="select" value={runtime} onChange={(e) => setRuntime(e.target.value)}>
            {RUNTIMES.map((r) => <option key={r} value={r}>{r}</option>)}
          </select>
        </Field>
        <Field label="Isolation">
          <select className="select" value={isolation} onChange={(e) => setIsolation(e.target.value)}>
            {ISOLATIONS.map((i) => <option key={i} value={i}>{i}</option>)}
          </select>
        </Field>
        <Button
          variant="primary"
          onClick={handleSubmit}
          disabled={disabled}
          style={{ height: 32 }}
        >
          Create Session
        </Button>
      </div>

      {/* Advanced toggle */}
      <button
        type="button"
        onClick={() => setShowAdvanced(!showAdvanced)}
        className="advanced-toggle"
      >
        <Caret direction={showAdvanced ? 'down' : 'right'} size={10} />
        Advanced options
      </button>

      {showAdvanced && (
        <div className="advanced-box">
          {/* LLM Provider Matrix */}
          <Section title="LLM Provider Matrix">
            <div className="table-wrap">
              <table className="table">
                <thead>
                  <tr>
                    <th style={{ width: 110 }}>Role</th>
                    <th>Provider</th>
                    <th>Model</th>
                  </tr>
                </thead>
                <tbody>
                  {(['light', 'default', 'realtime'] as const).map((role) => (
                    <tr key={role}>
                      <td className="table__cell--primary mono">{role}</td>
                      <td>
                        <input
                          className="input mono"
                          value={llm[role]?.provider_id ?? ''}
                          onChange={(e) => setLlm({ ...llm, [role]: { ...llm[role]!, provider_id: e.target.value } })}
                          placeholder={role === 'realtime' ? 'openai' : 'deepseek'}
                        />
                      </td>
                      <td>
                        <input
                          className="input mono"
                          value={llm[role]?.model ?? ''}
                          onChange={(e) => setLlm({ ...llm, [role]: { ...llm[role]!, model: e.target.value } })}
                          placeholder={role === 'realtime' ? 'gpt-realtime-mini' : 'deepseek-flash'}
                        />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <p className="field__label" style={{ marginTop: 'var(--space-2)', textTransform: 'none', letterSpacing: 0 }}>
              API Key 和 Base URL 通过启动环境变量注入，不在此输入。
            </p>
          </Section>

          {/* Workspace */}
          <Section title="Workspace">
            <select className="select" value={workspaceId} onChange={(e) => setWorkspaceId(e.target.value)} style={{ width: '100%' }}>
              <option value="">Auto-create workspace</option>
              {workspaces.map((ws) => (
                <option key={ws.id} value={ws.id}>{ws.name || ws.id.slice(0, 12)}</option>
              ))}
            </select>
          </Section>

          {/* Agent Configuration */}
          <Section title="Agent Configuration">
            <Field label="AssistantID">
              <input className="input" value={assistantId} onChange={(e) => setAssistantId(e.target.value)} placeholder="Optional" />
            </Field>
            <Field label="Cwd">
              <input className="input mono" value={cwd} onChange={(e) => setCwd(e.target.value)} placeholder="/path/to/workdir" />
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
            <Button size="sm" variant="ghost" onClick={() => setMcpServers([...mcpServers, { type: 'stdio', name: '' }])}>
              + Add MCP Server
            </Button>
          </Section>

          {/* Environment Variables */}
          <Section title="Environment Variables">
            <KeyValueEditor items={envVars} onChange={setEnvVars} />
          </Section>

          {/* Timeouts */}
          <Section title="Timeouts">
            <div className="form-grid">
              <Field label="IdleTimeout (sec)">
                <input className="input mono" type="number" value={idleTimeout} onChange={(e) => setIdleTimeout(e.target.value)} placeholder="(empty)" />
              </Field>
              <Field label="MaxDuration (sec)">
                <input className="input mono" type="number" value={maxDuration} onChange={(e) => setMaxDuration(e.target.value)} placeholder="(empty)" />
              </Field>
            </div>
          </Section>

          {/* Raw JSON / Metadata */}
          <Section title="Config / Metadata">
            <Field label="Config (JSON)">
              <textarea
                className="textarea textarea--mono"
                value={configJson}
                onChange={(e) => setConfigJson(e.target.value)}
                placeholder='{ "key": "value" }'
                style={{ minHeight: 60 }}
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
    <label className="field">
      <span className="field__label">{label}</span>
      {children}
    </label>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="adv-section">
      <h4 className="adv-section__title">{title}</h4>
      {children}
    </div>
  );
}

function DynamicStringList({ items, onChange, placeholder }: { items: string[]; onChange: (v: string[]) => void; placeholder: string }) {
  return (
    <div className="stack" style={{ gap: 'var(--space-1)' }}>
      {items.map((item, i) => (
        <div key={i} className="row">
          <input
            className="input mono"
            value={item}
            onChange={(e) => { const next = [...items]; next[i] = e.target.value; onChange(next); }}
            placeholder={placeholder}
            style={{ flex: 1 }}
          />
          <Button size="sm" variant="ghost" onClick={() => onChange(items.filter((_, j) => j !== i))}>×</Button>
        </div>
      ))}
      <Button size="sm" variant="ghost" onClick={() => onChange([...items, ''])} style={{ alignSelf: 'flex-start' }}>
        + Add
      </Button>
    </div>
  );
}

function KeyValueEditor({ items, onChange }: { items: { key: string; value: string }[]; onChange: (v: { key: string; value: string }[]) => void }) {
  return (
    <div className="stack" style={{ gap: 'var(--space-1)' }}>
      {items.map((item, i) => (
        <div key={i} className="row">
          <input
            className="input mono"
            value={item.key}
            onChange={(e) => { const next = [...items]; next[i] = { ...next[i], key: e.target.value }; onChange(next); }}
            placeholder="Key"
            style={{ flex: 1 }}
          />
          <input
            className="input mono"
            value={item.value}
            onChange={(e) => { const next = [...items]; next[i] = { ...next[i], value: e.target.value }; onChange(next); }}
            placeholder="Value"
            style={{ flex: 2 }}
          />
          <Button size="sm" variant="ghost" onClick={() => onChange(items.filter((_, j) => j !== i))}>×</Button>
        </div>
      ))}
      <Button size="sm" variant="ghost" onClick={() => onChange([...items, { key: '', value: '' }])} style={{ alignSelf: 'flex-start' }}>
        + Add
      </Button>
    </div>
  );
}

function McpServerEntry({ server, onChange, onRemove }: { server: McpServer; onChange: (s: McpServer) => void; onRemove: () => void }) {
  return (
    <div className="mcp-entry">
      <div className="row" style={{ marginBottom: 'var(--space-2)' }}>
        <select
          className="select mono"
          value={server.type}
          onChange={(e) => onChange({ ...server, type: e.target.value as 'stdio' | 'http' })}
          style={{ width: 84 }}
        >
          <option value="stdio">stdio</option>
          <option value="http">http</option>
        </select>
        <input
          className="input mono"
          value={server.name}
          onChange={(e) => onChange({ ...server, name: e.target.value })}
          placeholder="Server name"
          style={{ flex: 1 }}
        />
        <Button size="sm" variant="ghost" onClick={onRemove}>×</Button>
      </div>
      {server.type === 'stdio' ? (
        <>
          <Field label="Command">
            <input className="input mono" value={server.command ?? ''} onChange={(e) => onChange({ ...server, command: e.target.value })} />
          </Field>
          <Field label="Args (comma-separated)">
            <input
              className="input mono"
              value={(server.args ?? []).join(', ')}
              onChange={(e) => onChange({ ...server, args: e.target.value.split(',').map((s) => s.trim()).filter(Boolean) })}
            />
          </Field>
        </>
      ) : (
        <Field label="URL">
          <input className="input mono" value={server.url ?? ''} onChange={(e) => onChange({ ...server, url: e.target.value })} placeholder="http://…" />
        </Field>
      )}
    </div>
  );
}
