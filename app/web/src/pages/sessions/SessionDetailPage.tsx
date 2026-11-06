import { useEffect, useState, useCallback, useRef, useMemo } from 'react';
import { useParams, Link } from 'react-router-dom';
import { api } from '../../api';
import type { SessionInfo } from '../../api';
import { useWebSocket, type WSMessage } from '../../hooks/useWebSocket';
import ChatPanel from './components/ChatPanel';
import RealtimeOverlay from './components/RealtimeOverlay';
import MotionPanel, { type MotionFrame } from './components/MotionPanel';

interface RealtimeTask {
  taskId: string;
  instruction: string;
  source: string;
  status: 'running' | 'complete' | 'cancelled';
  startedAt: number;
  endedAt?: number;
}

function extractTasks(ctrlMessages: WSMessage[]): RealtimeTask[] {
  const map = new Map<string, RealtimeTask>();
  for (const m of ctrlMessages) {
    const d = m.data as Record<string, unknown> | undefined;
    if (!d) continue;
    const taskId = (d.task_id as string) || '';
    if (!taskId) continue;
    if (m.kind === 'realtime.task_start') {
      map.set(taskId, {
        taskId,
        instruction: (d.instruction as string) || '',
        source: (d.source as string) || 'realtime',
        status: 'running',
        startedAt: m.receivedAt,
      });
    } else if (m.kind === 'realtime.task_end') {
      const t = map.get(taskId);
      if (t) { t.status = (d.status as string) === 'complete' ? 'complete' : 'cancelled'; t.endedAt = m.receivedAt; }
    } else if (m.kind === 'realtime.task_cancel') {
      const t = map.get(taskId);
      if (t) { t.status = 'cancelled'; t.endedAt = m.receivedAt; }
    }
  }
  return Array.from(map.values());
}

export default function SessionDetailPage() {
  const { id } = useParams<{ id: string }>();
  const [session, setSession] = useState<SessionInfo | null>(null);
  const [realtimeActive, setRealtimeActive] = useState(false);
  const [overlayOpen, setOverlayOpen] = useState(false);
  const [tasksOpen, setTasksOpen] = useState(false);
  const overlayRef = useRef<HTMLDivElement>(null);
  const tasksRef = useRef<HTMLDivElement>(null);

  const [micAnalyser, setMicAnalyser] = useState<AnalyserNode | null>(null);
  const [playAnalyser, setPlayAnalyser] = useState<AnalyserNode | null>(null);
  const [audioStats, setAudioStats] = useState({ inFrames: 0, outFrames: 0, inBytes: 0, outBytes: 0 });
  const [motionVisible, setMotionVisible] = useState(false);
  const motionFrameRef = useRef<MotionFrame | null>(null);
  const motionWsRef = useRef<WebSocket | null>(null);

  const ctrl = useWebSocket(id ? `/ws/sessions/${id}/ctrl` : null);
  const data = useWebSocket(id ? `/ws/sessions/${id}/data` : null);
  const render = useWebSocket(realtimeActive && id ? `/ws/sessions/${id}/render` : null);

  // Motion WS: raw WebSocket, only stores latest frame in ref (no setState per frame).
  // Motion is optional — if unavailable, panel stays still (no simulation).
  useEffect(() => {
    if (!realtimeActive || !id) {
      if (motionWsRef.current) { motionWsRef.current.close(); motionWsRef.current = null; }
      return;
    }
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/ws/sessions/${id}/motion`;
    const ws = new WebSocket(wsUrl);
    motionWsRef.current = ws;
    ws.onmessage = (e) => {
      try {
        const parsed = JSON.parse(e.data);
        if (parsed.type === '_closed') return;
        if (parsed.face) { motionFrameRef.current = parsed as MotionFrame; }
      } catch { /* ignore */ }
    };
    ws.onclose = () => { motionWsRef.current = null; };

    return () => {
      ws.close(); motionWsRef.current = null;
    };
  }, [realtimeActive, id]);

  useEffect(() => {
    if (!id) return;
    api.sessions.list().then((list) => {
      const found = list.find((s) => s.id === id);
      if (found) setSession(found);
    });
  }, [id]);

  useEffect(() => {
    for (const m of data.messages) {
      if (m.kind === 'session_info_update') {
        const d = m.data as Record<string, unknown> | undefined;
        if (d?.title && typeof d.title === 'string') {
          setSession((prev) => prev ? { ...prev, title: d.title as string } : prev);
        }
      }
    }
  }, [data.messages]);

  const handleRealtimeChange = useCallback((active: boolean) => {
    setRealtimeActive(active);
    if (active) {
      setOverlayOpen(false);
      setMotionVisible(true);
    } else {
      setMotionVisible(false);
      motionFrameRef.current = null;
    }
  }, []);

  const handleAnalysers = useCallback((mic: AnalyserNode | null, play: AnalyserNode | null) => {
    setMicAnalyser(mic);
    setPlayAnalyser(play);
  }, []);

  useEffect(() => {
    if (!overlayOpen) return;
    const handler = (e: MouseEvent) => {
      if (overlayRef.current && !overlayRef.current.contains(e.target as Node)) {
        setOverlayOpen(false);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [overlayOpen]);

  // Close tasks dropdown on outside click
  useEffect(() => {
    if (!tasksOpen) return;
    const handler = (e: MouseEvent) => {
      if (tasksRef.current && !tasksRef.current.contains(e.target as Node)) {
        setTasksOpen(false);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [tasksOpen]);

  const tasks = useMemo(() => extractTasks(ctrl.messages), [ctrl.messages]);
  const runningCount = tasks.filter((t) => t.status === 'running').length;

  if (!id) return <p>No session selected</p>;

  const wsLabel = session?.workspaceName || session?.workspaceId?.slice(0, 12);
  const sessionTitle = (session as SessionInfo & { title?: string })?.title;

  // Show overlay dropdown only when not running and user toggled it open.
  const showOverlay = overlayOpen && !realtimeActive;

  return (
    <div style={{ height: 'calc(100vh - 100px)', display: 'flex', flexDirection: 'column' }}>
      {/* Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 8 }}>
        <div>
          <h2 style={{ margin: '0 0 2px', fontSize: 16 }}>
            {sessionTitle || `Session: ${id}`}
            {session && (
              <span style={{ marginLeft: 12, fontSize: 13, fontWeight: 400, color: session.status === 'running' ? '#28a745' : '#fd7e14' }}>
                [{session.status}]
              </span>
            )}
          </h2>
          {wsLabel && (
            <p style={{ margin: 0, fontSize: 12, color: '#666' }}>
              Workspace: <Link to={`/workspaces/${session?.workspaceId}?nodeId=${session?.nodeId}`} style={{ color: '#0066cc' }}>{wsLabel}</Link>
              <span style={{ marginLeft: 12, color: '#999' }}>ID: {id}</span>
            </p>
          )}
        </div>

        <div style={{ position: 'relative' }} ref={overlayRef}>
          {realtimeActive ? (
            /* Running: inline action bar with tasks badge */
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              {/* Tasks badge */}
              {tasks.length > 0 && (
                <div style={{ position: 'relative' }} ref={tasksRef}>
                  <button onClick={() => setTasksOpen(!tasksOpen)} style={tasksBadgeBtn(runningCount > 0)}>
                    Tasks {runningCount > 0 ? `⟳${runningCount}` : ''} ({tasks.length})
                  </button>
                  {tasksOpen && (
                    <div style={tasksDropdown}>
                      <div style={{ fontWeight: 600, fontSize: 12, marginBottom: 6, borderBottom: '1px solid #eee', paddingBottom: 4 }}>
                        Realtime Tasks ({tasks.length})
                      </div>
                      {tasks.map((t) => (
                        <TaskRow key={t.taskId} task={t} />
                      ))}
                    </div>
                  )}
                </div>
              )}
              <div style={inlineBar}>
                <span style={{ color: '#28a745', fontWeight: 600, fontSize: 12 }}>● Realtime</span>
                <button onClick={() => ctrl.send({ kind: 'audio_commit', receivedAt: Date.now() })} style={inlineBtn}>Commit</button>
                <button onClick={() => ctrl.send({ kind: 'audio_interrupt', receivedAt: Date.now() })} style={inlineBtn}>Interrupt</button>
                <button onClick={() => setMotionVisible((v) => !v)} style={{ ...inlineBtn, color: motionVisible ? '#2196f3' : '#333', borderColor: motionVisible ? '#2196f3' : '#666' }}>Motion</button>
                <button onClick={() => ctrl.send({ kind: 'stop_realtime', receivedAt: Date.now() })} style={inlineStopBtn}>Stop</button>
              </div>
            </div>
          ) : (
            /* Stopped: toggle dropdown */
            <button onClick={() => setOverlayOpen(!overlayOpen)} style={realtimeToggleBtn}>
              Realtime {overlayOpen ? '▴' : '▾'}
            </button>
          )}

          {/*
           * Single RealtimeOverlay instance — always mounted so its ctrl
           * listener and audio cleanup lifecycle are never interrupted.
           * Rendered inside the dropdown when stopped, hidden when running.
           */}
          <div style={showOverlay ? overlayStyle : hiddenStyle}>
            <RealtimeOverlay
              sessionId={id}
              ctrlConnected={ctrl.connected}
              ctrlMessages={ctrl.messages}
              onSendCtrl={ctrl.send}
              onRealtimeChange={handleRealtimeChange}
              onAnalysers={handleAnalysers}
              onAudioStats={setAudioStats}
            />
          </div>
        </div>
      </div>

      <div style={{ flex: 1, minHeight: 0 }}>
        <ChatPanel
          sessionId={id}
          ctrlMessages={ctrl.messages}
          dataMessages={data.messages}
          renderMessages={render.messages}
          ctrlConnected={ctrl.connected}
          dataConnected={data.connected}
          renderConnected={render.connected}
          realtimeActive={realtimeActive}
          micAnalyser={micAnalyser}
          playAnalyser={playAnalyser}
          audioStats={audioStats}
          onSend={ctrl.send}
          workspaceId={session?.workspaceId}
          nodeId={session?.nodeId}
        />
      </div>

      {/* Floating Motion Panel — portal-style, outside normal layout */}
      <MotionPanel
        frameRef={motionFrameRef}
        visible={motionVisible}
        onClose={() => setMotionVisible(false)}
      />
    </div>
  );
}

const inlineBar: React.CSSProperties = {
  display: 'flex', alignItems: 'center', gap: 8,
  padding: '4px 12px', borderRadius: 6,
  border: '2px solid #28a745', background: '#d4edda',
};
const inlineBtn: React.CSSProperties = {
  padding: '4px 10px', border: '1px solid #666', borderRadius: 4,
  background: 'transparent', color: '#333', cursor: 'pointer', fontSize: 11,
};
const inlineStopBtn: React.CSSProperties = {
  padding: '4px 10px', border: '1px solid #dc3545', borderRadius: 4,
  background: 'transparent', color: '#dc3545', cursor: 'pointer', fontSize: 11,
};
const realtimeToggleBtn: React.CSSProperties = {
  padding: '6px 14px', borderRadius: 6, fontSize: 12, fontWeight: 600,
  cursor: 'pointer', border: '1px solid #ccc', background: '#fff', color: '#333',
};
const overlayStyle: React.CSSProperties = {
  position: 'absolute', top: '100%', right: 0, marginTop: 4,
  width: 420, background: '#fff', border: '1px solid #ddd',
  borderRadius: 8, boxShadow: '0 4px 16px rgba(0,0,0,0.12)',
  padding: 16, zIndex: 100,
};
const hiddenStyle: React.CSSProperties = {
  position: 'absolute', width: 0, height: 0, overflow: 'hidden', opacity: 0, pointerEvents: 'none',
};
const tasksBadgeBtn = (hasRunning: boolean): React.CSSProperties => ({
  padding: '4px 10px', borderRadius: 6, fontSize: 11, fontWeight: 600, cursor: 'pointer',
  border: hasRunning ? '1px solid #fd7e14' : '1px solid #999',
  background: hasRunning ? '#fff3e0' : '#f5f5f5',
  color: hasRunning ? '#e65100' : '#555',
});
const tasksDropdown: React.CSSProperties = {
  position: 'absolute', top: '100%', left: 0, marginTop: 4,
  width: 400, maxHeight: 320, overflow: 'auto',
  background: '#fff', border: '1px solid #ddd',
  borderRadius: 8, boxShadow: '0 4px 16px rgba(0,0,0,0.12)',
  padding: 10, zIndex: 100,
};

function TaskRow({ task }: { task: RealtimeTask }) {
  const [expanded, setExpanded] = useState(false);
  const statusIcon = task.status === 'running' ? '⟳' : task.status === 'complete' ? '✓' : '✗';
  const statusColor = task.status === 'running' ? '#fd7e14' : task.status === 'complete' ? '#28a745' : '#dc3545';
  const elapsed = task.endedAt ? `${((task.endedAt - task.startedAt) / 1000).toFixed(1)}s` : 'running...';
  return (
    <div style={{ marginBottom: 4, borderBottom: '1px solid #f0f0f0', paddingBottom: 4 }}>
      <div
        onClick={() => setExpanded(!expanded)}
        style={{ display: 'flex', alignItems: 'center', gap: 6, cursor: 'pointer', fontSize: 11 }}
      >
        <span style={{ color: statusColor, fontWeight: 700, width: 14 }}>{statusIcon}</span>
        <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: '#333' }}>
          {task.instruction || task.taskId}
        </span>
        <span style={{ color: '#999', fontSize: 10, flexShrink: 0 }}>{elapsed}</span>
        <span style={{ color: '#aaa', fontSize: 10 }}>{expanded ? '▴' : '▾'}</span>
      </div>
      {expanded && (
        <div style={{ marginTop: 4, padding: '4px 8px', background: '#f8f8f8', borderRadius: 4, fontSize: 10, color: '#555' }}>
          <div><b>Task ID:</b> {task.taskId}</div>
          <div><b>Source:</b> {task.source}</div>
          <div><b>Status:</b> <span style={{ color: statusColor }}>{task.status}</span></div>
          <div><b>Instruction:</b> {task.instruction}</div>
          <div><b>Started:</b> {new Date(task.startedAt).toLocaleTimeString()}</div>
          {task.endedAt && <div><b>Ended:</b> {new Date(task.endedAt).toLocaleTimeString()}</div>}
        </div>
      )}
    </div>
  );
}
