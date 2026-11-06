import type { ReactNode } from 'react';

interface Props {
  title: ReactNode;
  /** One-line explanation of what this page is for. */
  subtitle?: ReactNode;
  actions?: ReactNode;
}

/**
 * Page title row with optional subtitle and right-aligned actions.
 *
 * @returns a flex header used at the top of every route
 */
export default function PageHeader({ title, subtitle, actions }: Props) {
  return (
    <div className="page-header">
      <div>
        <h1 className="page-header__title">{title}</h1>
        {subtitle && <div className="page-header__subtitle">{subtitle}</div>}
      </div>
      {actions && <div className="page-header__actions">{actions}</div>}
    </div>
  );
}
