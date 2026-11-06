import { useEffect, useState, useCallback, useRef, useMemo } from 'react';
import { useParams, Link } from 'react-router-dom';
import { api } from '../../api';
import type { SessionInfo } from '../../api';
import { useWebSocket, type WSMessage } from '../../hooks/useWebSocket';
import ChatPanel from './components/ChatPanel';
import RealtimeOverlay from './components/RealtimeOverlay';
import MotionPanel, { type MotionFrame } from './components/MotionPanel';
import Caret from '../../components/ui/Caret';
import Button from '../../components/ui/Button';
import StatusDot from '../../components/ui/StatusDot';

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
  // Whether the running realtime session was started with motion enabled. The
  // panel and its stream only exist in that case.
  const [motionSupported, setMotionSupported] = useState(false);
  const motionFrameRef = useRef<MotionFrame | null>(null);
  const motionWsRef = useRef<WebSocket | null>(null);

  const ctrl = useWebSocket(id ? `/ws/sessions/${id}/ctrl` : null);
  const data = useWebSocket(id ? `/ws/sessions/${id}/data` : null);
  const render = useWebSocket(realtimeActive && id ? `/ws/sessions/${id}/render` : null);

  // Motion WS: raw WebSocket, only stores latest frame in ref (no setState per frame).
  // Gated on motionVisible so a session started without motion opens no motion
  // stream at all; the panel is not shown in that case either.
  useEffect(() => {
    if (!motionVisible || !id) {
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
  }, [motionVisible, id]);

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

  const handleRealtimeChange = useCallback((active: boolean, motionEnabled?: boolean) => {
    setRealtimeActive(active);
    if (active) {
      setOverlayOpen(false);
      // The motion panel only makes sense when the session was started with
      // motion enabled; otherwise it renders a static avatar at 0 fps.
      const withMotion = motionEnabled === true;
      setMotionSupported(withMotion);
      setMotionVisible(withMotion);
    } else {
      setMotionSupported(false);
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
    <div className="page--fixed" style={{ gap: 'var(--space-2)' }}>
      <div className="session-head">
        <div style={{ minWidth: 0 }}>
          <div className="session-head__title">
            <span className="truncate">{sessionTitle || `Session: ${id}`}</span>
            {session && <StatusDot status={session.status} />}
          </div>
          <div className="session-head__meta">
            {wsLabel && (
              <span>
                <Link to={`/workspaces/${session?.workspaceId}?nodeId=${session?.nodeId}`}>{wsLabel}</Link>
              </span>
            )}
            <span className="mono truncate">{id}</span>
          </div>
        </div>

        <div style={{ position: 'relative', flexShrink: 0 }} ref={overlayRef}>
          {realtimeActive ? (
            <div className="row">
              {tasks.length > 0 && (
                <div style={{ position: 'relative' }} ref={tasksRef}>
                  <Button
                    size="sm"
                    variant={runningCount > 0 ? 'default' : 'ghost'}
                    onClick={() => setTasksOpen(!tasksOpen)}
                  >
                    Tasks {runningCount > 0 ? `(${runningCount} running)` : `(${tasks.length})`}
                  </Button>
                  {tasksOpen && (
                    <div className="popover" style={{ top: '100%', right: 0, marginTop: 4, width: 400, maxHeight: 340, overflow: 'auto', padding: 'var(--space-2)' }}>
                      <div className="panel__header" style={{ background: 'none', border: 'none', padding: '0 0 var(--space-1)' }}>
                        Realtime tasks ({tasks.length})
                      </div>
                      {tasks.map((t) => (
                        <TaskRow key={t.taskId} task={t} />
                      ))}
                    </div>
                  )}
                </div>
              )}

              <div className="rt-bar">
                <span className="rt-bar__label">
                  <span className="chat__dot chat__dot--on" />
                  Realtime
                </span>
                <Button size="sm" variant="ghost" onClick={() => ctrl.send({ kind: 'audio_commit', receivedAt: Date.now() })}>Commit</Button>
                <Button size="sm" variant="ghost" onClick={() => ctrl.send({ kind: 'audio_interrupt', receivedAt: Date.now() })}>Interrupt</Button>
                {motionSupported && (
                  <Button size="sm" variant={motionVisible ? 'primary' : 'ghost'} onClick={() => setMotionVisible((v) => !v)}>Motion</Button>
                )}
                <Button size="sm" variant="danger" onClick={() => ctrl.send({ kind: 'stop_realtime', receivedAt: Date.now() })}>Stop</Button>
              </div>
            </div>
          ) : (
            <Button onClick={() => setOverlayOpen(!overlayOpen)}>
              Realtime <Caret direction={overlayOpen ? 'up' : 'down'} />
            </Button>
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

const overlayStyle: React.CSSProperties = {
  position: 'absolute', top: '100%', right: 0, marginTop: 'var(--space-1)',
  width: 420, zIndex: 100,
};
const hiddenStyle: React.CSSProperties = {
  position: 'absolute', width: 0, height: 0, overflow: 'hidden', opacity: 0, pointerEvents: 'none',
};

function TaskRow({ task }: { task: RealtimeTask }) {
  const [expanded, setExpanded] = useState(false);
  const statusIcon = task.status === 'running' ? '⟳' : task.status === 'complete' ? '✓' : '✗';
  const elapsed = task.endedAt ? `${((task.endedAt - task.startedAt) / 1000).toFixed(1)}s` : 'running…';
  return (
    <div className="task-row">
      <button type="button" className="task-row__head" onClick={() => setExpanded(!expanded)}>
        <span className={`task-row__icon task-row__icon--${task.status}`}>{statusIcon}</span>
        <span className="task-row__text truncate">{task.instruction || task.taskId}</span>
        <span className="task-row__elapsed mono">{elapsed}</span>
        <span className="task-row__caret">
          <Caret direction={expanded ? 'up' : 'down'} size={10} />
        </span>
      </button>
      {expanded && (
        <dl className="kv task-row__detail" style={{ gridTemplateColumns: '68px 1fr' }}>
          <dt>Task ID</dt>
          <dd className="mono">{task.taskId}</dd>
          <dt>Source</dt>
          <dd className="mono">{task.source}</dd>
          <dt>Status</dt>
          <dd>{task.status}</dd>
          <dt>Instruction</dt>
          <dd>{task.instruction || '—'}</dd>
          <dt>Started</dt>
          <dd className="mono">{new Date(task.startedAt).toLocaleTimeString()}</dd>
          {task.endedAt && (
            <>
              <dt>Ended</dt>
              <dd className="mono">{new Date(task.endedAt).toLocaleTimeString()}</dd>
            </>
          )}
        </dl>
      )}
    </div>
  );
}
