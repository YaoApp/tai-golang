import { useEffect } from 'react';
import { api } from '../../api';
import { useCurrentNode } from '../../hooks/useCurrentNode';
import NodeCard from './components/NodeCard';
import { useSSE } from '../../hooks/useSSE';

export default function NodesPage() {
  const [currentNode, setCurrentNode, nodes] = useCurrentNode();
  const events = useSSE<{ event: string; nodeId: string }>('/api/nodes/events');

  // Refresh node list when SSE events arrive (hook already fetches on mount).
  useEffect(() => {
    if (events.length > 0) {
      api.nodes.list().catch(() => {});
    }
  }, [events.length]);

  return (
    <div>
      <h1>Nodes</h1>
      <p style={{ color: '#666', fontSize: 14 }}>
        Registered nodes: {nodes.length}
        {currentNode && (
          <span style={{ marginLeft: 12, color: '#0066cc', fontWeight: 600 }}>
            Current: {currentNode}
          </span>
        )}
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
        <div key={n.id} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
          <div style={{ flex: 1 }}>
            <NodeCard node={n} />
          </div>
          <button
            onClick={() => setCurrentNode(n.id)}
            disabled={currentNode === n.id}
            style={{
              padding: '6px 14px',
              border: currentNode === n.id ? '2px solid #0066cc' : '1px solid #ccc',
              borderRadius: 6,
              background: currentNode === n.id ? '#e8f0fe' : '#fff',
              color: currentNode === n.id ? '#0066cc' : '#333',
              cursor: currentNode === n.id ? 'default' : 'pointer',
              fontWeight: currentNode === n.id ? 600 : 400,
              fontSize: 13,
              whiteSpace: 'nowrap',
            }}
          >
            {currentNode === n.id ? '✓ Current' : 'Set as Current'}
          </button>
        </div>
      ))}
    </div>
  );
}
