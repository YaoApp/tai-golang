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
  const render = useWebSocket(realtimeActive && id ? `/ws/sessions/${id}/render` : null);

  useEffect(() => {
    if (!id) return;
    api.sessions.list().then((list) => {
      const found = list.find((s) => s.id === id);
      if (found) setSession(found);
    });
  }, [id]);

  const handleRealtimeChange = useCallback((active: boolean) => {
    setRealtimeActive(active);
  }, []);

  if (!id) return <p>No session selected</p>;

  const wsLabel = session?.workspaceName || session?.workspaceId?.slice(0, 12);

  return (
    <div style={{ height: 'calc(100vh - 100px)' }}>
      <h2 style={{ margin: '0 0 4px', fontSize: 16 }}>
        Session: {id}
        {session && (
          <span style={{ marginLeft: 12, fontSize: 13, fontWeight: 400, color: session.status === 'running' ? '#28a745' : '#fd7e14' }}>
            [{session.status}]
          </span>
        )}
      </h2>
      {wsLabel && (
        <p style={{ margin: '0 0 12px', fontSize: 13, color: '#666' }}>
          Workspace: <Link to={`/workspaces/${session?.workspaceId}?nodeId=${session?.nodeId}`} style={{ color: '#0066cc' }}>{wsLabel}</Link>
        </p>
      )}
      <RealtimePanel
        sessionId={id}
        ctrlConnected={ctrl.connected}
        ctrlMessages={ctrl.messages}
        onSendCtrl={ctrl.send}
        onRealtimeChange={handleRealtimeChange}
      />
      <ChatPanel
        ctrlMessages={ctrl.messages}
        dataMessages={data.messages}
        renderMessages={render.messages}
        ctrlConnected={ctrl.connected}
        dataConnected={data.connected}
        renderConnected={render.connected}
        onSend={ctrl.send}
      />
    </div>
  );
}
