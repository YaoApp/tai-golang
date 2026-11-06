import type { ReactNode } from 'react';

interface Props {
  title?: ReactNode;
  /** Longer explanation below the title. */
  hint?: ReactNode;
  children?: ReactNode;
  actions?: ReactNode;
}

/**
 * Centered placeholder for a list or panel with no content.
 *
 * @returns an empty-state block; `hint` explains how to produce content
 */
export default function EmptyState({ title, hint, children, actions }: Props) {
  return (
    <div className="empty-state">
      {title && <div className="empty-state__title">{title}</div>}
      {hint && <div>{hint}</div>}
      {children}
      {actions}
    </div>
  );
}
