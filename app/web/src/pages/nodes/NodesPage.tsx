import { useEffect } from 'react';
import { useCurrentNode } from '../../hooks/useCurrentNode';
import NodeCard from './components/NodeCard';
import { eventBus } from '../../lib/events';
import Button from '../../components/ui/Button';
import EmptyState from '../../components/ui/EmptyState';
import PageHeader from '../../components/ui/PageHeader';

export default function NodesPage() {
  const [currentNode, setCurrentNode, nodes, refreshNodes] = useCurrentNode();

  // Registration changes arrive on the global event stream; any node.* event
  // triggers a list refresh.
  useEffect(() => eventBus.on('node.', () => void refreshNodes()), [refreshNodes]);

  // The empty-state copy is asserted by the browser tests; keep it literal.
  const isEmpty = nodes.length === 0;

  return (
    <div>
      <PageHeader
        title="Nodes"
        subtitle={
          <span>
            Registered nodes: {nodes.length}
            {currentNode && (
              <>
                {' · current '}
                <span className="mono" style={{ color: 'var(--accent)' }}>{currentNode}</span>
              </>
            )}
          </span>
        }
        actions={<Button onClick={() => void refreshNodes()}>Refresh</Button>}
      />

      {isEmpty ? (
        <EmptyState
          title="No nodes registered"
          hint={
            <>
              Use the Auth page to generate a token, then run <code>tai start</code>.
            </>
          }
        />
      ) : (
        <div className="node-list">
          {nodes.map((n) => (
            <div key={n.id} className="node-list__row">
              <NodeCard node={n} active={currentNode === n.id} />
              <Button
                variant={currentNode === n.id ? 'ghost' : 'default'}
                size="sm"
                disabled={currentNode === n.id}
                onClick={() => setCurrentNode(n.id)}
              >
                {currentNode === n.id ? 'Current' : 'Set as current'}
              </Button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
