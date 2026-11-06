import { NavLink, Outlet, useLocation } from 'react-router-dom';

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

export default function Layout() {
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

        <div className="sidebar__footer mono">transport v2</div>
      </nav>

      <main className="app__main">
        <Outlet />
      </main>
    </div>
  );
}
