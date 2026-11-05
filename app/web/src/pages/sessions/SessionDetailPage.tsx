import { useEffect, useState, useCallback } from 'react';
import { useParams, Link } from 'react-router-dom';
import { api } from '../../api';
import type { SessionInfo } from '../../api';
import { useWebSocket } from '../../hooks/useWebSocket';
import ChatPanel from './components/ChatPanel';
import RealtimePanel from './components/RealtimePanel';

export default function SessionDetailPage() {
  const { id } = useParams<{ id: string }>();
  const [session, setSession] = useState<SessionInfo | null>(null);
  const [realtimeActive, setRealtimeActive] = useState(false);

  const ctrl = useWebSocket(id ? `/ws/sessions/${id}/ctrl` : null);
  const data = useWebSocket(id ? `/ws/sessions/${id}/data` : null);
  // Render WS managed by RealtimePanel independently; keep hook for future reconnect detection.
  useWebSocket(realtimeActive && id ? `/ws/sessions/${id}/render` : null);

  useEffect(() => {
    if (!id) return;
    api.sessions.list().then((list) => {
      const found = list.find((s) => s.id === id);
      if (found) setSession(found);
    });
  }, [id]);

  // Update session title from data stream session_info_update
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
  }, []);

  if (!id) return <p>No session selected</p>;

  const wsLabel = session?.workspaceName || session?.workspaceId?.slice(0, 12);
  const sessionTitle = (session as SessionInfo & { title?: string })?.title;

  return (
    <div style={{ height: 'calc(100vh - 100px)', display: 'flex', flexDirection: 'column' }}>
      {/* Header */}
      <div style={{ marginBottom: 8 }}>
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

      {/* Realtime Panel (independent, above chat) */}
      <RealtimePanel
        sessionId={id}
        ctrlConnected={ctrl.connected}
        ctrlMessages={ctrl.messages}
        onSendCtrl={ctrl.send}
        onRealtimeChange={handleRealtimeChange}
      />

      {/* Text Chat Panel */}
      <div style={{ flex: 1, minHeight: 0 }}>
        <ChatPanel
          sessionId={id}
          ctrlMessages={ctrl.messages}
          dataMessages={data.messages}
          ctrlConnected={ctrl.connected}
          dataConnected={data.connected}
          onSend={ctrl.send}
          workspaceId={session?.workspaceId}
          nodeId={session?.nodeId}
        />
      </div>
    </div>
  );
}
