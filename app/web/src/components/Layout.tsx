import { NavLink, Outlet, useLocation } from 'react-router-dom';
import type { DemoUser } from '../api';
import Button from './ui/Button';

const navGroups = [
  {
    label: 'Setup',
    items: [{ to: '/', label: 'Auth' }],
  },
  {
    label: 'Features',
    items: [
      { to: '/nodes', label: 'Nodes' },
      { to: '/sessions', label: 'Sessions' },
      { to: '/workspaces', label: 'Workspaces' },
    ],
  },
  {
    label: 'Reference',
    items: [{ to: '/docs', label: 'Docs' }],
  },
];

interface Props {
  user: DemoUser;
  onLogout: () => void;
}

export default function Layout({ user, onLogout }: Props) {
  const { pathname } = useLocation();

  return (
    <div className="app">
      <nav className="sidebar">
        <div className="sidebar__brand">
          <span className="sidebar__mark" />
          <span>Tai Console</span>
        </div>

        {navGroups.map((group) => (
          <div key={group.label} className="sidebar__group">
            <div className="sidebar__group-label">{group.label}</div>
            {group.items.map((item) => {
              // `end` matches only "/" exactly; the other routes must stay
              // highlighted on their detail pages (e.g. /sessions/:id).
              const active =
                item.to === '/'
                  ? pathname === '/'
                  : pathname === item.to || pathname.startsWith(item.to + '/');

              return (
                <NavLink
                  key={item.to}
                  to={item.to}
                  className={`sidebar__link${active ? ' sidebar__link--active' : ''}`}
                >
                  {item.label}
                </NavLink>
              );
            })}
          </div>
        ))}

        <div className="sidebar__account">
          <div className="sidebar__account-email mono" title={user.email}>
            {user.email}
          </div>
          <div className="sidebar__account-meta">
            <span>transport v2</span>
            <Button size="sm" variant="ghost" onClick={onLogout}>
              Sign out
            </Button>
          </div>
        </div>
      </nav>

      <main className="app__main">
        <Outlet />
      </main>
    </div>
  );
}
