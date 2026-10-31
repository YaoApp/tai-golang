import { useState, useEffect, useRef } from 'react';
import type { WSMessage } from '../../../hooks/useWebSocket';

interface Props {
  ctrlMessages: WSMessage[];
  dataMessages: WSMessage[];
  ctrlConnected: boolean;
  dataConnected: boolean;
  onSend: (msg: WSMessage) => void;
}

interface ChatEntry {
  role: 'user' | 'assistant' | 'system';
  content: string;
  kind?: string;
}

export default function ChatPanel({
  ctrlMessages,
  dataMessages,
  ctrlConnected,
  dataConnected,
  onSend,
}: Props) {
  const [input, setInput] = useState('');
  const [showRaw, setShowRaw] = useState(false);
  const chatEndRef = useRef<HTMLDivElement>(null);

  const handleSend = () => {
    if (!input.trim()) return;
    onSend({ kind: 'prompt', data: { message: input.trim() } });
    setInput('');
  };

  // Build chat entries from events.
  const entries: ChatEntry[] = [];
  let assistantBuf = '';
  let thinking = '';

  const flushAssistant = () => {
    if (thinking) {
      entries.push({ role: 'assistant', content: thinking, kind: 'thinking' });
    }
    if (assistantBuf) {
      entries.push({ role: 'assistant', content: assistantBuf });
    }
    assistantBuf = '';
    thinking = '';
  };

  for (const m of dataMessages) {
    const d = m.data as Record<string, unknown> | undefined;
    switch (m.kind) {
      case 'state_update': {
        const state = (d as any)?.state;
        if (state === 'running') {
          flushAssistant();
        } else if (state === 'idle') {
          flushAssistant();
        }
        entries.push({ role: 'system', content: `State → ${state}`, kind: m.kind });
        break;
      }
      case 'notice': {
        const sev = (d as any)?.severity ?? '';
        const title = (d as any)?.title ?? '';
        entries.push({ role: 'system', content: `[${sev}] ${title}`, kind: m.kind });
        break;
      }
      case 'agent_message_start':
        flushAssistant();
        break;
      case 'agent_message_chunk': {
        const content = (d as any)?.content;
        if (content?.type === 'text' && content?.text) {
          assistantBuf += content.text;
        }
        break;
      }
      case 'agent_thinking_chunk': {
        const content = (d as any)?.content;
        if (content?.type === 'text' && content?.text) {
          thinking += content.text;
        }
        break;
      }
      case 'agent_message_end':
        flushAssistant();
        break;
      case 'tool_call_start': {
        const name = (d as any)?.name ?? 'tool';
        entries.push({ role: 'system', content: `🔧 Tool: ${name}`, kind: m.kind });
        break;
      }
      case 'tool_call_update': {
        const status = (d as any)?.status ?? '';
        entries.push({ role: 'system', content: `Tool ${status}`, kind: m.kind });
        break;
      }
      case 'session_info_update':
        break; // skip
      default:
        if (m.kind !== '_closed') {
          entries.push({ role: 'system', content: `${m.kind}`, kind: m.kind });
        }
        break;
    }
  }
  // Flush any pending assistant text (streaming in progress).
  if (assistantBuf) {
    entries.push({ role: 'assistant', content: assistantBuf });
  }

  useEffect(() => {
    chatEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [entries.length, assistantBuf.length]);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      {/* Header */}
      <div style={{ display: 'flex', gap: 12, marginBottom: 8, fontSize: 12, alignItems: 'center' }}>
        <span>ctrl: <Dot ok={ctrlConnected} /></span>
        <span>data: <Dot ok={dataConnected} /></span>
        <span style={{ color: '#888' }}>Events: {dataMessages.length}</span>
        <label style={{ marginLeft: 'auto', cursor: 'pointer' }}>
          <input type="checkbox" checked={showRaw} onChange={() => setShowRaw(!showRaw)} />
          {' '}Raw
        </label>
      </div>

      {/* Chat / Raw view */}
      <div style={{ flex: 1, overflow: 'auto', marginBottom: 8, border: '1px solid #eee', borderRadius: 4, padding: 12, background: '#fafafa' }}>
        {showRaw ? (
          <>
            <h4 style={{ margin: '0 0 8px', fontSize: 13 }}>Data Stream ({dataMessages.length})</h4>
            {dataMessages.map((m, i) => (
              <div key={i} style={eventLine}>
                <span style={kindBadge}>{m.kind}</span>
                {m.data !== undefined && (
                  <pre style={jsonPre}>{JSON.stringify(m.data, null, 2)}</pre>
                )}
              </div>
            ))}
            <h4 style={{ margin: '12px 0 8px', fontSize: 13 }}>Ctrl Stream ({ctrlMessages.length})</h4>
            {ctrlMessages.map((m, i) => (
              <div key={i} style={eventLine}>
                <span style={{ ...kindBadge, background: '#fff3cd' }}>{m.kind}</span>
                {m.data !== undefined && (
                  <pre style={jsonPre}>{JSON.stringify(m.data, null, 2)}</pre>
                )}
              </div>
            ))}
          </>
        ) : (
          <>
            {entries.map((e, i) => (
              <ChatBubble key={i} entry={e} />
            ))}
            <div ref={chatEndRef} />
          </>
        )}
      </div>

      {/* Input */}
      <div style={{ display: 'flex', gap: 8 }}>
        <input
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && handleSend()}
          placeholder="Type a prompt..."
          style={{ flex: 1, padding: '8px 12px', border: '1px solid #ccc', borderRadius: 4 }}
          disabled={!ctrlConnected}
        />
        <button onClick={handleSend} disabled={!ctrlConnected} style={sendBtn(ctrlConnected)}>
          Send
        </button>
        <button onClick={() => onSend({ kind: 'cancel' })} disabled={!ctrlConnected} style={cancelBtn(ctrlConnected)}>
          Cancel
        </button>
      </div>
    </div>
  );
}

function ChatBubble({ entry }: { entry: ChatEntry }) {
  if (entry.role === 'system') {
    return (
      <div style={{ textAlign: 'center', fontSize: 11, color: '#888', margin: '6px 0' }}>
        {entry.content}
      </div>
    );
  }
  const isUser = entry.role === 'user';
  const isThinking = entry.kind === 'thinking';
  return (
    <div style={{ display: 'flex', justifyContent: isUser ? 'flex-end' : 'flex-start', margin: '8px 0' }}>
      <div
        style={{
          maxWidth: '80%',
          padding: '8px 12px',
          borderRadius: 8,
          background: isUser ? '#0066cc' : isThinking ? '#f0f0f0' : '#fff',
          color: isUser ? '#fff' : isThinking ? '#666' : '#333',
          border: isUser ? 'none' : '1px solid #e0e0e0',
          fontSize: 13,
          lineHeight: 1.5,
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
          fontStyle: isThinking ? 'italic' : 'normal',
        }}
      >
        {isThinking && <div style={{ fontSize: 10, color: '#999', marginBottom: 4 }}>💭 Thinking</div>}
        {entry.content}
      </div>
    </div>
  );
}

function Dot({ ok }: { ok: boolean }) {
  return (
    <span
      style={{
        display: 'inline-block', width: 8, height: 8, borderRadius: '50%',
        background: ok ? '#28a745' : '#dc3545', marginRight: 4,
      }}
    />
  );
}

const sendBtn = (ok: boolean): React.CSSProperties => ({
  padding: '8px 16px', border: 'none', borderRadius: 4,
  background: ok ? '#0066cc' : '#ccc', color: '#fff',
  cursor: ok ? 'pointer' : 'default',
});

const cancelBtn = (ok: boolean): React.CSSProperties => ({
  padding: '8px 12px', border: '1px solid #dc3545', borderRadius: 4,
  background: 'transparent', color: '#dc3545',
  cursor: ok ? 'pointer' : 'default',
});

const eventLine: React.CSSProperties = {
  marginBottom: 6, paddingBottom: 6, borderBottom: '1px solid #f0f0f0',
};

const kindBadge: React.CSSProperties = {
  display: 'inline-block', padding: '2px 6px', borderRadius: 3,
  background: '#e8f0fe', fontSize: 11, fontWeight: 600, marginRight: 8,
};

const jsonPre: React.CSSProperties = {
  margin: '4px 0 0', fontSize: 11, whiteSpace: 'pre-wrap', wordBreak: 'break-all', color: '#555',
};
