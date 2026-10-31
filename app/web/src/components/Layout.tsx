import { NavLink, Outlet } from 'react-router-dom';

const navItems = [
  { to: '/', label: 'Auth' },
  { to: '/nodes', label: 'Nodes' },
  { to: '/sessions', label: 'Sessions' },
  { to: '/docs', label: 'Docs' },
];

export default function Layout() {
  return (
    <div style={{ display: 'flex', minHeight: '100vh' }}>
      <nav style={{
        width: 180,
        padding: '20px 12px',
        borderRight: '1px solid #ddd',
        background: '#f8f9fa',
      }}>
        <h2 style={{ margin: '0 0 20px', fontSize: 18 }}>Tai Demo</h2>
        {navItems.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            style={({ isActive }) => ({
              display: 'block',
              padding: '8px 12px',
              margin: '4px 0',
              borderRadius: 6,
              textDecoration: 'none',
              color: isActive ? '#fff' : '#333',
              background: isActive ? '#0066cc' : 'transparent',
              fontWeight: isActive ? 600 : 400,
            })}
          >
            {item.label}
          </NavLink>
        ))}
      </nav>
      <main style={{ flex: 1, padding: 24 }}>
        <Outlet />
      </main>
    </div>
  );
}
