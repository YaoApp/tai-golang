import { useEffect, useState } from 'react';
import { useParams, Link } from 'react-router-dom';
import { api } from '../../api';
import type { SessionInfo } from '../../api';
import { useWebSocket } from '../../hooks/useWebSocket';
import ChatPanel from './components/ChatPanel';

export default function SessionDetailPage() {
  const { id } = useParams<{ id: string }>();
  const [session, setSession] = useState<SessionInfo | null>(null);

  const ctrl = useWebSocket(id ? `/ws/sessions/${id}/ctrl` : null);
  const data = useWebSocket(id ? `/ws/sessions/${id}/data` : null);

  useEffect(() => {
    if (!id) return;
    api.sessions.list().then((list) => {
      const found = list.find((s) => s.id === id);
      if (found) setSession(found);
    });
  }, [id]);

  if (!id) return <p>No session selected</p>;

  const wsLabel = session?.workspaceName || session?.workspaceId?.slice(0, 12);

  return (
    <div style={{ height: 'calc(100vh - 100px)' }}>
      <h2 style={{ margin: '0 0 4px', fontSize: 16 }}>Session: {id}</h2>
      {wsLabel && (
        <p style={{ margin: '0 0 12px', fontSize: 13, color: '#666' }}>
          Workspace: <Link to={`/workspaces/${session?.workspaceId}?nodeId=${session?.nodeId}`} style={{ color: '#0066cc' }}>{wsLabel}</Link>
        </p>
      )}
      <ChatPanel
        ctrlMessages={ctrl.messages}
        dataMessages={data.messages}
        ctrlConnected={ctrl.connected}
        dataConnected={data.connected}
        onSend={ctrl.send}
      />
    </div>
  );
}
