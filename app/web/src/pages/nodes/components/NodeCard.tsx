import type { NodeInfo } from '../../../api';

export default function NodeCard({ node }: { node: NodeInfo }) {
  return (
    <div style={{
      border: '1px solid #ddd',
      borderRadius: 8,
      padding: '12px 16px',
      marginBottom: 8,
      background: '#fff',
    }}>
      <div style={{ fontWeight: 600, fontSize: 15 }}>{node.id}</div>
      <div style={{ fontSize: 13, color: '#666', marginTop: 4 }}>
        tai_id: {node.taiId || '—'}
      </div>
      <div style={{ fontSize: 12, color: '#999', marginTop: 4 }}>
        Registered: {new Date(node.registeredAt).toLocaleString()}
      </div>
    </div>
  );
}
