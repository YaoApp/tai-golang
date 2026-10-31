import { useEffect, useState } from 'react';
import { api } from '../../api';
import type { NodeInfo } from '../../api';
import { useSSE } from '../../hooks/useSSE';
import NodeCard from './components/NodeCard';

export default function NodesPage() {
  const [nodes, setNodes] = useState<NodeInfo[]>([]);
  const events = useSSE<{ event: string; nodeId: string }>('/api/nodes/events');

  useEffect(() => {
    api.nodes.list().then(setNodes).catch(() => {});
  }, []);

  // Refresh node list when SSE events arrive.
  useEffect(() => {
    if (events.length > 0) {
      api.nodes.list().then(setNodes).catch(() => {});
    }
  }, [events.length]);

  return (
    <div>
      <h1>Nodes</h1>
      <p style={{ color: '#666', fontSize: 14 }}>
        Registered nodes: {nodes.length}
        {events.length > 0 && (
          <span style={{ color: '#28a745', marginLeft: 8 }}>
            (SSE: {events.length} events)
          </span>
        )}
      </p>
      {nodes.length === 0 && (
        <p style={{ color: '#999' }}>
          No nodes registered. Use the Auth page to generate a token, then run{' '}
          <code>tai start</code>.
        </p>
      )}
      {nodes.map((n) => (
        <NodeCard key={n.id} node={n} />
      ))}
    </div>
  );
}
