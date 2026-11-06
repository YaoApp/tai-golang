import { useState, useEffect, useRef, useMemo, useCallback } from 'react';
import type { WSMessage } from '../../../hooks/useWebSocket';
import type {
  DisplayItem, MessageItem, ToolCallItem, PlanItem, TerminalItem,
  CompactionItem, SubagentItem, StateUpdatePayload, NoticePayload,
  UsagePayload, SessionInfoPayload, MessageChunkPayload, MessagePayload,
  ToolCallUpdatePayload, ToolCallContentChunkPayload, PlanUpdatePayload,
  PlanRemovedPayload, TerminalUpdatePayload, TerminalOutputChunkPayload,
  CompactionUpdatePayload, CompactionSummaryChunkPayload, SubagentUpdatePayload,
  SessionMessagePayload, ConfigOptionPayload,
  PermissionRequestPayload, ElicitationRequestPayload,
} from '../../../types';
import MessageBubble from './MessageBubble';
import ToolCallCard from './ToolCallCard';
import PlanPanel from './PlanPanel';
import TerminalCard from './TerminalCard';
import PermissionDialog from './PermissionDialog';
import ElicitationDialog from './ElicitationDialog';
import SubtitlePanel from './SubtitlePanel';

interface Props {
  sessionId: string;
  ctrlMessages: WSMessage[];
  dataMessages: WSMessage[];
  renderMessages: WSMessage[];
  ctrlConnected: boolean;
  dataConnected: boolean;
  renderConnected: boolean;
  realtimeActive: boolean;
  micAnalyser: AnalyserNode | null;
  playAnalyser: AnalyserNode | null;
  audioStats: { inFrames: number; outFrames: number; inBytes: number; outBytes: number };
  onSend: (msg: WSMessage) => void;
  workspaceId?: string;
  nodeId?: string;
}

type SendMode = 'prompt' | 'followup' | 'steer';

interface LocalUserMessage {
  id: string;
  text: string;
  mode: SendMode;
  timestamp: number;
  afterEventIndex: number; // dataMessages.length at send time
}

export default function ChatPanel({
  sessionId: _sessionId,
  ctrlMessages,
  dataMessages,
  renderMessages,
  ctrlConnected,
  dataConnected,
  renderConnected: _renderConnected,
  realtimeActive,
  micAnalyser,
  playAnalyser,
  audioStats,
  onSend,
  workspaceId,
  nodeId,
}: Props) {
  const [input, setInput] = useState('');
  const [sendMode, setSendMode] = useState<SendMode>('prompt');
  const [attachments, setAttachments] = useState<{ name: string; path: string }[]>([]);
  const [uploading, setUploading] = useState(false);
  const chatEndRef = useRef<HTMLDivElement>(null);
  const ctrlEndRef = useRef<HTMLDivElement>(null);
  const dataEndRef = useRef<HTMLDivElement>(null);
  const renderEndRef = useRef<HTMLDivElement>(null);

  // Subtitle panel + render/audio panel visibility
  const [subtitleVisible, setSubtitleVisible] = useState(false);
  const [renderPanelVisible, setRenderPanelVisible] = useState(false);
  const [audioPanelVisible, setAudioPanelVisible] = useState(false);
  const prevRealtimeRef = useRef(false);

  // Auto-open subtitle/render/audio when Realtime activates
  useEffect(() => {
    if (realtimeActive && !prevRealtimeRef.current) {
      setSubtitleVisible(true);
      setRenderPanelVisible(true);
      setAudioPanelVisible(true);
    }
    prevRealtimeRef.current = realtimeActive;
  }, [realtimeActive]);

  // Local user messages (shown immediately on send, before server echo)
  const [localUserMessages, setLocalUserMessages] = useState<LocalUserMessage[]>([]);

  // Permission / Elicitation state
  const [permissionReq, setPermissionReq] = useState<PermissionRequestPayload | null>(null);
  const [elicitationReq, setElicitationReq] = useState<ElicitationRequestPayload | null>(null);

  // Track which ctrl events we've already processed for dialogs
  const processedCtrlRef = useRef(0);

  // Process ctrl messages for permission/elicitation requests
  useEffect(() => {
    for (let i = processedCtrlRef.current; i < ctrlMessages.length; i++) {
      const m = ctrlMessages[i];
      const d = m.data as Record<string, unknown> | undefined;
      if (m.kind === 'request_permission' && d) {
        setPermissionReq(d as unknown as PermissionRequestPayload);
      } else if (m.kind === 'elicitation' && d) {
        setElicitationReq(d as unknown as ElicitationRequestPayload);
      }
    }
    processedCtrlRef.current = ctrlMessages.length;
  }, [ctrlMessages]);

  // Build display items: local user messages + server data events
  const displayItems = useMemo(() => {
    // Collect messageIds already present in server events
    const serverUserMsgIds = new Set<string>();
    for (const m of dataMessages) {
      if (m.kind === 'user_message' || m.kind === 'user_message_chunk') {
        const d = m.data as Record<string, unknown> | undefined;
        if (d?.messageId && typeof d.messageId === 'string') serverUserMsgIds.add(d.messageId);
      }
    }

    // Build local user bubbles not yet echoed by server
    const pendingLocal = localUserMessages.filter((lm) => !serverUserMsgIds.has(lm.id));

    return buildDisplayItems(dataMessages, pendingLocal);
  }, [dataMessages, localUserMessages]);

  // Agent state
  const agentState = useMemo(() => {
    let state: StateUpdatePayload | null = null;
    for (const m of dataMessages) {
      if (m.kind === 'state_update') state = m.data as unknown as StateUpdatePayload;
    }
    return state;
  }, [dataMessages]);

  // Latest usage
  const latestUsage = useMemo(() => {
    let usage: UsagePayload | null = null;
    for (const m of dataMessages) {
      if (m.kind === 'usage_update') usage = m.data as unknown as UsagePayload;
    }
    return usage;
  }, [dataMessages]);

  // Auto-scroll all panels
  useEffect(() => {
    chatEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [displayItems.length]);
  useEffect(() => {
    ctrlEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [ctrlMessages.length]);
  useEffect(() => {
    dataEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [dataMessages.length]);
  useEffect(() => {
    renderEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [renderMessages.length]);

  // Send message
  const handleSend = useCallback(() => {
    if (!input.trim() && attachments.length === 0) return;
    const id = crypto.randomUUID();
    const text = input.trim();

    // Record position relative to current data events
    if (text) {
      setLocalUserMessages((prev) => [
        ...prev,
        { id, text, mode: sendMode, timestamp: Date.now(), afterEventIndex: dataMessages.length },
      ]);
    }

    if (attachments.length > 0) {
      onSend({
        kind: sendMode,
        data: {
          id,
          message: text || undefined,
          attachments: attachments.map((a) => a.path),
        },
        receivedAt: Date.now(),
      });
    } else {
      onSend({ kind: sendMode, data: { id, message: text }, receivedAt: Date.now() });
    }

    setInput('');
    setAttachments([]);
    setSendMode('prompt');
  }, [input, attachments, sendMode, onSend, dataMessages.length]);

  // File upload
  const handleFileSelect = useCallback(async () => {
    if (!workspaceId || !nodeId) return;
    const fileInput = document.createElement('input');
    fileInput.type = 'file';
    fileInput.onchange = async () => {
      const file = fileInput.files?.[0];
      if (!file) return;
      setUploading(true);
      try {
        const path = `/.attachments/${Date.now()}_${file.name}`;
        await fetch(
          `/api/workspaces/${workspaceId}/branches/main/files/content?nodeId=${nodeId}&path=${encodeURIComponent(path)}`,
          { method: 'PUT', body: file },
        );
        setAttachments((prev) => [...prev, { name: file.name, path }]);
      } catch {
        // upload failed
      } finally {
        setUploading(false);
      }
    };
    fileInput.click();
  }, [workspaceId, nodeId]);

  // Permission handlers
  const handlePermissionResolve = useCallback((optionId: string) => {
    if (!permissionReq) return;
    onSend({
      kind: 'resolve_permission',
      data: { requestId: permissionReq.requestId, response: { outcome: 'selected', optionId } },
      receivedAt: Date.now(),
    });
    setPermissionReq(null);
  }, [permissionReq, onSend]);

  const handlePermissionCancel = useCallback(() => {
    if (!permissionReq) return;
    onSend({
      kind: 'resolve_permission',
      data: { requestId: permissionReq.requestId, response: { outcome: 'cancelled' } },
      receivedAt: Date.now(),
    });
    setPermissionReq(null);
  }, [permissionReq, onSend]);

  // Elicitation handlers
  const handleElicitationAccept = useCallback((content: Record<string, unknown>) => {
    if (!elicitationReq) return;
    onSend({
      kind: 'resolve_elicitation',
      data: { id: elicitationReq.id, response: { action: 'accept', content } },
      receivedAt: Date.now(),
    });
    setElicitationReq(null);
  }, [elicitationReq, onSend]);

  const handleElicitationDecline = useCallback(() => {
    if (!elicitationReq) return;
    onSend({
      kind: 'resolve_elicitation',
      data: { id: elicitationReq.id, response: { action: 'decline' } },
      receivedAt: Date.now(),
    });
    setElicitationReq(null);
  }, [elicitationReq, onSend]);

  const isRunning = agentState?.state === 'running';

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      {/* Header */}
      <div style={headerBar}>
        <span>ctrl: <Dot ok={ctrlConnected} /></span>
        <span>data: <Dot ok={dataConnected} /></span>
        <span style={{ color: '#888' }}>Events: {dataMessages.length}</span>
        {agentState && (
          <span style={{ fontWeight: 600, color: stateColor(agentState.state) }}>
            {agentState.state === 'running' && '⟳ '}
            {agentState.state === 'requires_action' && '⚠ '}
            {agentState.state}
          </span>
        )}
        {latestUsage && (
          <span style={{ color: '#888', fontSize: 11 }}>
            Tokens: {latestUsage.used}/{latestUsage.size}
            {latestUsage.cost && ` ($${latestUsage.cost.amount.toFixed(4)})`}
          </span>
        )}
      </div>

      {/* Dual-column layout */}
      <div style={dualPane}>
        {/* Left: Text Chat (upper) + Subtitle (lower) */}
        <div style={leftColumn}>
          <div style={leftUpperPane}>
            {displayItems.map((item, i) => (
              <DisplayItemRenderer key={i} item={item} />
            ))}
            <div ref={chatEndRef} />
          </div>
          {subtitleVisible && (
            <div style={leftLowerPane}>
              <SubtitlePanel
                renderMessages={renderMessages}
                realtimeActive={realtimeActive}
                micAnalyser={micAnalyser}
                playAnalyser={playAnalyser}
                onClose={() => setSubtitleVisible(false)}
              />
            </div>
          )}
        </div>

        {/* Right: Raw event panels (ctrl/data + render/audio) */}
        <div style={rightPane}>
          <div style={ctrlSection}>
            <div style={sectionHeader}>
              Ctrl <span style={countBadge}>{ctrlMessages.length}</span>
            </div>
            <div style={sectionBody}>
              {ctrlMessages.map((m, i) => (
                <RawEventLine key={i} msg={m} channel="ctrl" />
              ))}
              <div ref={ctrlEndRef} />
            </div>
          </div>

          <div style={dataSection}>
            <div style={sectionHeader}>
              Data <span style={countBadge}>{dataMessages.length}</span>
            </div>
            <div style={sectionBody}>
              {dataMessages.map((m, i) => (
                <RawEventLine key={i} msg={m} channel="data" />
              ))}
              <div ref={dataEndRef} />
            </div>
          </div>

          {renderPanelVisible && (
            <div style={renderSection}>
              <div style={sectionHeader}>
                Render <span style={countBadge}>{renderMessages.length}</span>
                <button onClick={() => setRenderPanelVisible(false)} style={panelCloseBtn}>×</button>
              </div>
              <div style={sectionBody}>
                {renderMessages.map((m, i) => (
                  <RawEventLine key={i} msg={m} channel="render" />
                ))}
                <div ref={renderEndRef} />
              </div>
            </div>
          )}

          {audioPanelVisible && (
            <div style={audioSection}>
              <div style={sectionHeader}>
                Audio <span style={countBadge}>
                  {audioStats.inFrames + audioStats.outFrames}
                </span>
                <button onClick={() => setAudioPanelVisible(false)} style={panelCloseBtn}>×</button>
              </div>
              <div style={sectionBody}>
                <div style={{ fontSize: 11, color: '#555', lineHeight: 1.8 }}>
                  <div>⬇ Received: {audioStats.inFrames} frames ({formatBytes(audioStats.inBytes)})</div>
                  <div>⬆ Sent: {audioStats.outFrames} frames ({formatBytes(audioStats.outBytes)})</div>
                  <div>Sample Rate: 24000 Hz / PCM16 / Mono</div>
                  {realtimeActive && <div style={{ color: '#28a745' }}>● Streaming</div>}
                </div>
              </div>
            </div>
          )}
        </div>
      </div>

      {/* Attachment bar */}
      {attachments.length > 0 && (
        <div style={attachBar}>
          {attachments.map((a, i) => (
            <span key={i} style={attachChip}>
              📎 {a.name}
              <button onClick={() => setAttachments(attachments.filter((_, j) => j !== i))} style={chipRemove}>×</button>
            </span>
          ))}
        </div>
      )}

      {/* Input */}
      <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
        <button
          onClick={handleFileSelect}
          disabled={!ctrlConnected || !workspaceId || uploading}
          style={attachBtnStyle(ctrlConnected && !!workspaceId)}
          title={workspaceId ? 'Attach file' : 'No workspace (create session with workspace first)'}
        >
          {uploading ? '...' : '+'}
        </button>
        <select
          value={sendMode}
          onChange={(e) => setSendMode(e.target.value as SendMode)}
          style={modeSelect}
          title="Send mode"
        >
          <option value="prompt">Prompt</option>
          <option value="followup">Followup</option>
          <option value="steer">Steer</option>
        </select>
        <input
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); handleSend(); }
          }}
          placeholder={`Type a ${sendMode}...`}
          style={inputStyle}
          disabled={!ctrlConnected}
        />
        <button onClick={handleSend} disabled={!ctrlConnected} style={sendBtn(ctrlConnected)}>
          Send
        </button>
        {isRunning && (
          <button onClick={() => onSend({ kind: 'cancel', receivedAt: Date.now() })} style={cancelBtn}>
            Cancel
          </button>
        )}
      </div>

      {/* Permission / Elicitation dialogs */}
      {permissionReq && (
        <PermissionDialog
          request={permissionReq}
          onResolve={handlePermissionResolve}
          onCancel={handlePermissionCancel}
        />
      )}
      {elicitationReq && (
        <ElicitationDialog
          request={elicitationReq}
          onAccept={handleElicitationAccept}
          onDecline={handleElicitationDecline}
          onCancel={() => setElicitationReq(null)}
        />
      )}
    </div>
  );
}

// --- Display Item Renderer ---

function DisplayItemRenderer({ item }: { item: DisplayItem }) {
  switch (item.type) {
    case 'message':
      return <MessageBubble item={item.data} />;
    case 'tool_call':
      return <ToolCallCard item={item.data} />;
    case 'plan':
      return <PlanPanel item={item.data} />;
    case 'terminal':
      return <TerminalCard item={item.data} />;
    case 'compaction':
      return <CompactionIndicator item={item.data} />;
    case 'subagent':
      return <SubagentCard item={item.data} />;
    case 'state':
      return <StateIndicator state={item.state} stopReason={item.stopReason} error={item.error} />;
    case 'notice':
      return <NoticeBar severity={item.severity} title={item.title} />;
    case 'usage':
      return null; // Shown in header
    case 'session_info':
      return null; // Shown in page header
    case 'config_option':
      return <ConfigOptionLine data={item.data} />;
    case 'session_message':
      return <SessionMessageLine data={item.data} />;
  }
}

// --- Raw event line with timestamp + collapsible JSON ---

function RawEventLine({ msg, channel }: { msg: WSMessage; channel: 'ctrl' | 'data' | 'render' }) {
  const [expanded, setExpanded] = useState(false);
  const ts = formatTime(msg.receivedAt);
  const isOut = msg.direction === 'out';
  const badgeBg = isOut ? '#d4edda' : channel === 'ctrl' ? '#fff3cd' : '#e8f0fe';

  return (
    <div style={rawLine}>
      <div style={rawLineHeader} onClick={() => setExpanded(!expanded)}>
        <span style={rawTs}>{ts}</span>
        {isOut && <span style={{ ...kindBadge, background: '#c3e6cb', color: '#155724', fontSize: 9 }}>→</span>}
        {!isOut && <span style={{ ...kindBadge, background: '#bee5eb', color: '#0c5460', fontSize: 9 }}>←</span>}
        <span style={{ ...kindBadge, background: badgeBg }}>{msg.kind}</span>
        <span style={rawToggle}>{expanded ? '▾' : '▸'}</span>
      </div>
      {expanded && msg.data !== undefined && (
        <pre style={rawJson}>{JSON.stringify(msg.data, null, 2)}</pre>
      )}
    </div>
  );
}

// --- Inline components ---

function StateIndicator({ state, stopReason, error }: { state: string; stopReason?: string; error?: { code: number; message: string } }) {
  return (
    <div style={{ textAlign: 'center', fontSize: 11, margin: '4px 0', color: stateColor(state) }}>
      {state === 'running' && '⟳ '}
      {state === 'requires_action' && '⚠ '}
      State: {state}
      {stopReason && ` (${stopReason})`}
      {error && ` — ${error.message}`}
    </div>
  );
}

function NoticeBar({ severity, title }: { severity: string; title: string }) {
  const colors: Record<string, { bg: string; fg: string; border: string }> = {
    info: { bg: '#e8f4f8', fg: '#0c5460', border: '#bee5eb' },
    warning: { bg: '#fff3cd', fg: '#856404', border: '#ffeeba' },
    error: { bg: '#f8d7da', fg: '#721c24', border: '#f5c6cb' },
  };
  const c = colors[severity] || colors.info;
  return (
    <div style={{ margin: '6px 0', padding: '6px 12px', borderRadius: 4, background: c.bg, color: c.fg, border: `1px solid ${c.border}`, fontSize: 12 }}>
      [{severity}] {title}
    </div>
  );
}

function CompactionIndicator({ item }: { item: CompactionItem }) {
  const summary = item.summary.join('');
  return (
    <div style={{ margin: '6px 0', padding: '6px 12px', borderRadius: 4, background: '#f0f0f0', fontSize: 11, color: '#666' }}>
      🗜️ Compaction: <strong>{item.status}</strong>
      {summary && <div style={{ marginTop: 4, whiteSpace: 'pre-wrap' }}>{summary}</div>}
    </div>
  );
}

function SubagentCard({ item }: { item: SubagentItem }) {
  return (
    <div style={{ margin: '6px 0', padding: '6px 12px', borderRadius: 4, background: '#eef', border: '1px solid #ddf', fontSize: 12 }}>
      🤖 Subagent: <strong>{item.title || item.sessionId}</strong>
      {item.status && <span style={{ marginLeft: 8, color: '#666' }}>[{item.status}]</span>}
    </div>
  );
}

function ConfigOptionLine({ data }: { data: ConfigOptionPayload }) {
  return (
    <div style={{ textAlign: 'center', fontSize: 11, margin: '4px 0', color: '#888' }}>
      ⚙ Config: {data.name || data.configId} = {String(data.currentValue)}
    </div>
  );
}

function SessionMessageLine({ data }: { data: SessionMessagePayload }) {
  const text = data.content?.map((c) => c.text).filter(Boolean).join('') || '';
  return (
    <div style={{ margin: '6px 0', padding: '6px 12px', borderRadius: 4, background: '#eef', border: '1px solid #ddf', fontSize: 12 }}>
      💬 Session Message from <strong>{data.senderSessionId.slice(0, 8)}</strong>: {text}
    </div>
  );
}

// --- Event processing ---

function buildDisplayItems(dataMessages: WSMessage[], pendingLocalMessages: LocalUserMessage[] = []): DisplayItem[] {
  const messages = new Map<string, MessageItem>();
  const thoughts = new Map<string, MessageItem>();
  const toolCalls = new Map<string, ToolCallItem>();
  const plans = new Map<string, PlanItem>();
  const terminals = new Map<string, TerminalItem>();
  const compactions = new Map<string, CompactionItem>();
  const subagents = new Map<string, SubagentItem>();
  const inlineItems = new Map<string, DisplayItem>();

  const insertOrder: string[] = [];
  const insertedIds = new Set<string>();
  let inlineSeq = 0;

  // Turn counter: Process mode spawns a new agent process per Prompt,
  // so messageIds can collide across turns. We prefix with turn number
  // to disambiguate. Incremented on each state_update(idle).
  let turnCounter = 0;

  function trackInsert(id: string) {
    if (!insertedIds.has(id)) {
      insertedIds.add(id);
      insertOrder.push(id);
    }
  }

  // Scope messageId to the current turn to prevent cross-turn collisions.
  function scopedId(rawId: string): string {
    return `t${turnCounter}:${rawId}`;
  }

  // Sort pending local messages by afterEventIndex for correct insertion.
  const sortedLocal = [...pendingLocalMessages].sort((a, b) => a.afterEventIndex - b.afterEventIndex);
  let localIdx = 0;

  // Insert local user messages whose afterEventIndex <= current dataMessage index.
  function flushLocalMessages(beforeDataIndex: number) {
    while (localIdx < sortedLocal.length && sortedLocal[localIdx].afterEventIndex <= beforeDataIndex) {
      const lm = sortedLocal[localIdx];
      const key = `local:${lm.id}`;
      inlineItems.set(key, {
        type: 'message',
        data: { messageId: lm.id, role: 'user', chunks: [lm.text], complete: true, sendMode: lm.mode },
      });
      trackInsert(key);
      localIdx++;
    }
  }

  for (let dataIdx = 0; dataIdx < dataMessages.length; dataIdx++) {
    // Insert any local user messages that were sent before this data event.
    flushLocalMessages(dataIdx);

    const m = dataMessages[dataIdx];
    const d = m.data as Record<string, unknown> | undefined;

    // Detect turn boundaries: state_update(idle) ends a turn.
    // All subsequent content events belong to the next turn.
    if (m.kind === 'state_update') {
      const p = d as unknown as StateUpdatePayload;
      if (p?.state === 'idle') {
        turnCounter++;
      }
    }

    switch (m.kind) {
      // --- Messages ---
      case 'agent_message_chunk': {
        const p = d as unknown as MessageChunkPayload;
        const id = scopedId(p?.messageId || `msg_anon_${inlineSeq++}`);
        let msg = messages.get(id);
        if (!msg) {
          msg = { messageId: id, role: 'assistant', chunks: [], complete: false };
          messages.set(id, msg);
          trackInsert(`msg:${id}`);
        }
        if (p?.content?.text) msg.chunks.push(p.content.text);
        break;
      }
      case 'agent_message': {
        const p = d as unknown as MessagePayload;
        const id = scopedId(p?.messageId || `msg_anon_${inlineSeq++}`);
        let msg = messages.get(id);
        if (!msg) {
          msg = { messageId: id, role: 'assistant', chunks: [], complete: true };
          messages.set(id, msg);
          trackInsert(`msg:${id}`);
        }
        msg.complete = true;
        if (p?.content) {
          const text = p.content.map((c) => c.text || '').join('');
          if (text && msg.chunks.length === 0) msg.chunks.push(text);
        }
        break;
      }
      case 'agent_thought_chunk': {
        const p = d as unknown as MessageChunkPayload;
        const id = scopedId(`thought_${p?.messageId || inlineSeq++}`);
        let msg = thoughts.get(id);
        if (!msg) {
          msg = { messageId: id, role: 'assistant', chunks: [], complete: false, isThought: true };
          thoughts.set(id, msg);
          trackInsert(`thought:${id}`);
        }
        if (p?.content?.text) msg.chunks.push(p.content.text);
        break;
      }
      case 'agent_thought': {
        const p = d as unknown as MessagePayload;
        const id = scopedId(`thought_${p?.messageId || inlineSeq++}`);
        let msg = thoughts.get(id);
        if (!msg) {
          msg = { messageId: id, role: 'assistant', chunks: [], complete: true, isThought: true };
          thoughts.set(id, msg);
          trackInsert(`thought:${id}`);
        }
        msg.complete = true;
        break;
      }
      case 'user_message_chunk': {
        const p = d as unknown as MessageChunkPayload;
        const id = scopedId(p?.messageId || `umsg_anon_${inlineSeq++}`);
        let msg = messages.get(id);
        if (!msg) {
          msg = { messageId: id, role: 'user', chunks: [], complete: false };
          messages.set(id, msg);
          trackInsert(`msg:${id}`);
        }
        if (p?.content?.text) msg.chunks.push(p.content.text);
        break;
      }
      case 'user_message': {
        const p = d as unknown as MessagePayload;
        const id = scopedId(p?.messageId || `umsg_anon_${inlineSeq++}`);
        let msg = messages.get(id);
        if (!msg) {
          msg = { messageId: id, role: 'user', chunks: [], complete: true };
          messages.set(id, msg);
          trackInsert(`msg:${id}`);
        }
        msg.complete = true;
        if (p?.content) {
          const text = p.content.map((c) => c.text || '').join('');
          if (text && msg.chunks.length === 0) msg.chunks.push(text);
        }
        break;
      }

      // --- Tool calls ---
      case 'tool_call_update': {
        const p = d as unknown as ToolCallUpdatePayload;
        const id = p?.toolCallId || `tc_anon_${inlineSeq++}`;
        let tc = toolCalls.get(id);
        if (!tc) {
          tc = { toolCallId: id, name: '', kind: '', status: 'pending', output: [] };
          toolCalls.set(id, tc);
          trackInsert(`tc:${id}`);
        }
        if (p?.name) tc.name = p.name;
        if (p?.kind) tc.kind = p.kind;
        if (p?.status) tc.status = p.status;
        if (p?.locations) tc.locations = p.locations;
        break;
      }
      case 'tool_call_content_chunk': {
        const p = d as unknown as ToolCallContentChunkPayload;
        const id = p?.toolCallId || '';
        let tc = toolCalls.get(id);
        if (!tc) {
          tc = { toolCallId: id, name: '', kind: '', status: 'in_progress', output: [] };
          toolCalls.set(id, tc);
          trackInsert(`tc:${id}`);
        }
        if (p?.content?.text) tc.output.push(p.content.text);
        break;
      }

      // --- Plan ---
      case 'plan_update': {
        const p = d as unknown as PlanUpdatePayload;
        const id = p?.plan?.planId || `plan_anon_${inlineSeq++}`;
        const plan: PlanItem = { planId: id, entries: p?.plan?.entries || [] };
        plans.set(id, plan);
        trackInsert(`plan:${id}`);
        break;
      }
      case 'plan_removed': {
        const p = d as unknown as PlanRemovedPayload;
        const id = p?.planId || '';
        const plan = plans.get(id);
        if (plan) plan.removed = true;
        break;
      }

      // --- Terminal ---
      case 'terminal_update': {
        const p = d as unknown as TerminalUpdatePayload;
        const id = p?.terminalId || `term_anon_${inlineSeq++}`;
        let term = terminals.get(id);
        if (!term) {
          term = { terminalId: id, output: [] };
          terminals.set(id, term);
          trackInsert(`term:${id}`);
        }
        if (p?.command !== undefined) term.command = p.command;
        if (p?.cwd !== undefined) term.cwd = p.cwd;
        if (p?.title !== undefined) term.title = p.title;
        if (p?.exitStatus) term.exitCode = p.exitStatus.exitCode;
        break;
      }
      case 'terminal_output_chunk': {
        const p = d as unknown as TerminalOutputChunkPayload;
        const id = p?.terminalId || '';
        let term = terminals.get(id);
        if (!term) {
          term = { terminalId: id, output: [] };
          terminals.set(id, term);
          trackInsert(`term:${id}`);
        }
        if (p?.data) term.output.push(p.data);
        break;
      }

      // --- Compaction ---
      case 'compaction_update': {
        const p = d as unknown as CompactionUpdatePayload;
        const id = p?.compactionId || `comp_anon_${inlineSeq++}`;
        let comp = compactions.get(id);
        if (!comp) {
          comp = { compactionId: id, status: p?.status || '', summary: [] };
          compactions.set(id, comp);
          trackInsert(`comp:${id}`);
        }
        if (p?.status) comp.status = p.status;
        break;
      }
      case 'compaction_summary_chunk': {
        const p = d as unknown as CompactionSummaryChunkPayload;
        const id = p?.compactionId || '';
        let comp = compactions.get(id);
        if (!comp) {
          comp = { compactionId: id, status: '', summary: [] };
          compactions.set(id, comp);
          trackInsert(`comp:${id}`);
        }
        if (p?.content?.text) comp.summary.push(p.content.text);
        break;
      }

      // --- Subagent ---
      case 'subagent_update': {
        const p = d as unknown as SubagentUpdatePayload;
        const id = p?.sessionId || `sub_anon_${inlineSeq++}`;
        subagents.set(id, { sessionId: id, title: p?.title, status: p?.status });
        trackInsert(`sub:${id}`);
        break;
      }

      // --- Session message ---
      case 'session_message':
      case 'session_message_chunk': {
        const p = d as unknown as SessionMessagePayload;
        if (p) {
          const key = `il:${inlineSeq++}`;
          inlineItems.set(key, { type: 'session_message', data: p });
          trackInsert(key);
        }
        break;
      }

      // --- State/notice/usage/info/config → inline items ---
      case 'state_update': {
        const p = d as unknown as StateUpdatePayload;
        const key = `il:${inlineSeq++}`;
        inlineItems.set(key, { type: 'state', state: p?.state || 'idle', stopReason: p?.stopReason, error: p?.error });
        trackInsert(key);
        break;
      }
      case 'notice': {
        const p = d as unknown as NoticePayload;
        const key = `il:${inlineSeq++}`;
        inlineItems.set(key, { type: 'notice', severity: p?.severity || 'info', title: p?.title || '' });
        trackInsert(key);
        break;
      }
      case 'usage_update': {
        const p = d as unknown as UsagePayload;
        const key = `il:${inlineSeq++}`;
        inlineItems.set(key, { type: 'usage', used: p?.used || 0, size: p?.size || 0, cost: p?.cost });
        trackInsert(key);
        break;
      }
      case 'session_info_update': {
        const p = d as unknown as SessionInfoPayload;
        if (p?.title) {
          const key = `il:${inlineSeq++}`;
          inlineItems.set(key, { type: 'session_info', title: p.title });
          trackInsert(key);
        }
        break;
      }
      case 'config_option_update': {
        const p = d as unknown as ConfigOptionPayload;
        if (p) {
          const key = `il:${inlineSeq++}`;
          inlineItems.set(key, { type: 'config_option', data: p });
          trackInsert(key);
        }
        break;
      }
      case 'available_commands_update':
      case '_closed':
      default:
        break;
    }
  }

  // Flush any remaining local messages sent after all data events.
  flushLocalMessages(dataMessages.length + 1);

  // All items go through insertOrder — proper chronological interleaving
  const result: DisplayItem[] = [];
  for (const key of insertOrder) {
    if (key.startsWith('local:')) {
      const item = inlineItems.get(key);
      if (item) result.push(item);
    } else if (key.startsWith('msg:')) {
      const msg = messages.get(key.slice(4));
      if (msg) result.push({ type: 'message', data: msg });
    } else if (key.startsWith('thought:')) {
      const msg = thoughts.get(key.slice(8));
      if (msg) result.push({ type: 'message', data: msg });
    } else if (key.startsWith('tc:')) {
      const tc = toolCalls.get(key.slice(3));
      if (tc) result.push({ type: 'tool_call', data: tc });
    } else if (key.startsWith('plan:')) {
      const plan = plans.get(key.slice(5));
      if (plan) result.push({ type: 'plan', data: plan });
    } else if (key.startsWith('term:')) {
      const term = terminals.get(key.slice(5));
      if (term) result.push({ type: 'terminal', data: term });
    } else if (key.startsWith('comp:')) {
      const comp = compactions.get(key.slice(5));
      if (comp) result.push({ type: 'compaction', data: comp });
    } else if (key.startsWith('sub:')) {
      const sub = subagents.get(key.slice(4));
      if (sub) result.push({ type: 'subagent', data: sub });
    } else if (key.startsWith('il:')) {
      const item = inlineItems.get(key);
      if (item) result.push(item);
    }
  }

  return result;
}

// --- Helpers ---

function Dot({ ok }: { ok: boolean }) {
  return (
    <span style={{
      display: 'inline-block', width: 8, height: 8, borderRadius: '50%',
      background: ok ? '#28a745' : '#dc3545', marginRight: 4,
    }} />
  );
}

function stateColor(state: string): string {
  switch (state) {
    case 'running': return '#0066cc';
    case 'idle': return '#28a745';
    case 'requires_action': return '#fd7e14';
    default: return '#888';
  }
}

function formatTime(ts: number): string {
  if (!ts) return '--:--:--';
  const d = new Date(ts);
  const h = String(d.getHours()).padStart(2, '0');
  const m = String(d.getMinutes()).padStart(2, '0');
  const s = String(d.getSeconds()).padStart(2, '0');
  const ms = String(d.getMilliseconds()).padStart(3, '0');
  return `${h}:${m}:${s}.${ms}`;
}

// --- Styles ---

const headerBar: React.CSSProperties = {
  display: 'flex', gap: 12, marginBottom: 8, fontSize: 12, alignItems: 'center', flexWrap: 'wrap',
};

const dualPane: React.CSSProperties = {
  display: 'flex', flex: 1, gap: 8, minHeight: 0,
};

const leftColumn: React.CSSProperties = {
  flex: 3, display: 'flex', flexDirection: 'column', gap: 4, minHeight: 0,
};

const leftUpperPane: React.CSSProperties = {
  flex: 3, overflow: 'auto', padding: 12,
  border: '1px solid #eee', borderRadius: 4, background: '#fafafa',
};

const leftLowerPane: React.CSSProperties = {
  flex: 1, display: 'flex', flexDirection: 'column', minHeight: 80,
};

const rightPane: React.CSSProperties = {
  flex: 2, display: 'flex', flexDirection: 'column', gap: 4, minHeight: 0,
};

const ctrlSection: React.CSSProperties = {
  flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0,
  border: '1px solid #eee', borderRadius: 4, background: '#fffcf0',
};

const dataSection: React.CSSProperties = {
  flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0,
  border: '1px solid #eee', borderRadius: 4, background: '#f8faff',
};

const renderSection: React.CSSProperties = {
  flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0,
  border: '1px solid #eee', borderRadius: 4, background: '#f0fff4',
};

const audioSection: React.CSSProperties = {
  flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0,
  border: '1px solid #eee', borderRadius: 4, background: '#fff5f5',
};

const sectionHeader: React.CSSProperties = {
  padding: '4px 8px', fontSize: 11, fontWeight: 700, borderBottom: '1px solid #eee',
  flexShrink: 0, display: 'flex', alignItems: 'center', gap: 4,
};

const sectionBody: React.CSSProperties = {
  flex: 1, overflow: 'auto', padding: '4px 8px',
};

const countBadge: React.CSSProperties = {
  display: 'inline-block', padding: '1px 5px', borderRadius: 8,
  background: '#ddd', fontSize: 10, fontWeight: 400, marginLeft: 4,
};

const panelCloseBtn: React.CSSProperties = {
  marginLeft: 'auto', background: 'none', border: 'none', cursor: 'pointer',
  color: '#999', fontSize: 13, padding: '0 2px', lineHeight: 1,
};

function formatBytes(b: number): string {
  if (b < 1024) return `${b} B`;
  if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`;
  return `${(b / (1024 * 1024)).toFixed(1)} MB`;
}

const rawLine: React.CSSProperties = {
  marginBottom: 2, paddingBottom: 2, borderBottom: '1px solid #f0f0f0',
};

const rawLineHeader: React.CSSProperties = {
  display: 'flex', alignItems: 'center', gap: 6, cursor: 'pointer',
  padding: '2px 0', fontSize: 11,
};

const rawTs: React.CSSProperties = {
  fontFamily: 'monospace', fontSize: 10, color: '#999', flexShrink: 0,
};

const rawToggle: React.CSSProperties = {
  marginLeft: 'auto', color: '#bbb', fontSize: 10,
};

const rawJson: React.CSSProperties = {
  margin: '2px 0 4px 16px', fontSize: 10, whiteSpace: 'pre-wrap',
  wordBreak: 'break-all', color: '#555', background: '#f5f5f5',
  padding: '4px 6px', borderRadius: 3, maxHeight: 200, overflow: 'auto',
};

const kindBadge: React.CSSProperties = {
  display: 'inline-block', padding: '1px 5px', borderRadius: 3,
  background: '#e8f0fe', fontSize: 10, fontWeight: 600,
};

const attachBar: React.CSSProperties = {
  display: 'flex', gap: 6, marginBottom: 6, flexWrap: 'wrap',
};

const attachChip: React.CSSProperties = {
  display: 'inline-flex', alignItems: 'center', gap: 4,
  padding: '2px 8px', background: '#e8f0fe', borderRadius: 12, fontSize: 11,
};

const chipRemove: React.CSSProperties = {
  background: 'none', border: 'none', cursor: 'pointer', color: '#999', fontSize: 12, padding: 0,
};

const inputStyle: React.CSSProperties = {
  flex: 1, padding: '8px 12px', border: '1px solid #ccc', borderRadius: 4, fontSize: 13,
};

const modeSelect: React.CSSProperties = {
  padding: '6px 4px', borderRadius: 4, border: '1px solid #ccc', fontSize: 11, width: 80,
};

const attachBtnStyle = (ok: boolean): React.CSSProperties => ({
  padding: '8px', border: '1px solid #ccc', borderRadius: 4,
  background: '#fff', cursor: ok ? 'pointer' : 'default', fontSize: 16,
  opacity: ok ? 1 : 0.5,
});

const sendBtn = (ok: boolean): React.CSSProperties => ({
  padding: '8px 16px', border: 'none', borderRadius: 4,
  background: ok ? '#0066cc' : '#ccc', color: '#fff',
  cursor: ok ? 'pointer' : 'default', fontWeight: 600, fontSize: 13,
});

const cancelBtn: React.CSSProperties = {
  padding: '8px 12px', border: '1px solid #dc3545', borderRadius: 4,
  background: 'transparent', color: '#dc3545', cursor: 'pointer', fontSize: 13,
};
