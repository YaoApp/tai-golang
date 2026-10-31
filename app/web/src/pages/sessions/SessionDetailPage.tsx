import { useParams } from 'react-router-dom';
import { useWebSocket } from '../../hooks/useWebSocket';
import ChatPanel from './components/ChatPanel';

export default function SessionDetailPage() {
  const { id } = useParams<{ id: string }>();

  const ctrl = useWebSocket(id ? `/ws/sessions/${id}/ctrl` : null);
  const data = useWebSocket(id ? `/ws/sessions/${id}/data` : null);

  if (!id) return <p>No session selected</p>;

  return (
    <div style={{ height: 'calc(100vh - 100px)' }}>
      <h2 style={{ margin: '0 0 12px', fontSize: 16 }}>Session: {id}</h2>
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
