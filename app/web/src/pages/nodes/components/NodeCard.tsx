import type { NodeInfo } from '../../../api';

interface Props {
  node: NodeInfo;
  active?: boolean;
}

/**
 * Registered node summary. Kept as a plain surface card so it can sit inside a
 * list row without competing with the row's own controls.
 *
 * @param active marks the node currently targeted by the console
 */
export default function NodeCard({ node, active }: Props) {
  const platform = [node.os, node.arch].filter(Boolean).join('/');

  return (
    <div className={`node-card${active ? ' node-card--active' : ''}`}>
      <div className="node-card__head">
        <span className="node-card__name mono">{node.id}</span>
        {node.version && <span className="badge mono">tai {node.version}</span>}
        {active && <span className="badge badge--accent">current</span>}
      </div>
      <dl className="node-card__meta">
        <dt>tai_id</dt>
        <dd className="mono">{node.taiId || '—'}</dd>
        {platform && (
          <>
            <dt>platform</dt>
            <dd className="mono">{platform}</dd>
          </>
        )}
        <dt>registered</dt>
        <dd className="mono">{new Date(node.registeredAt).toLocaleString()}</dd>
      </dl>
    </div>
  );
}
