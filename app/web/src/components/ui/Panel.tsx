import type { ReactNode } from 'react';

interface Props {
  title?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  /** Set for tables and event streams that manage their own padding. */
  flush?: boolean;
  className?: string;
  style?: React.CSSProperties;
}

/**
 * Bordered container with an optional uppercase title bar.
 *
 * @param flush drops the body padding so dense content (tables, raw event
 *   lines) can reach the panel edges
 * @returns a flex column that fills its parent's height
 */
export default function Panel({ title, actions, children, flush, className, style }: Props) {
  const classes = ['panel'];
  if (className) classes.push(className);

  return (
    <div className={classes.join(' ')} style={style}>
      {(title || actions) && (
        <div className="panel__header">
          {title}
          {actions && <div className="panel__actions">{actions}</div>}
        </div>
      )}
      <div className={flush ? 'panel__body panel__body--flush' : 'panel__body'}>
        {children}
      </div>
    </div>
  );
}
