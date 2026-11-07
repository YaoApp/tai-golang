import { useCallback, useEffect, useState } from 'react';
import { BrowserRouter, Routes, Route } from 'react-router-dom';
import Layout from './components/Layout';
import LoginPage from './pages/auth/LoginPage';
import { api } from './api';
import type { DemoUser } from './api';
import AuthPage from './pages/auth/AuthPage';
import NodesPage from './pages/nodes/NodesPage';
import SessionsPage from './pages/sessions/SessionsPage';
import SessionDetailPage from './pages/sessions/SessionDetailPage';
import DocsPage from './pages/docs/DocsPage';
import WorkspacesPage from './pages/workspaces/WorkspacesPage';
import WorkspaceDetailPage from './pages/workspaces/WorkspaceDetailPage';
import { eventBus } from './lib/events';

export default function App() {
  const [user, setUser] = useState<DemoUser | null>(null);
  const [loading, setLoading] = useState(true);

  // Resolve the login state once on boot; protected APIs return 401 otherwise.
  useEffect(() => {
    api.auth
      .me()
      .then((res) => setUser(res.user))
      .catch(() => setUser(null))
      .finally(() => setLoading(false));
  }, []);

  // Keep one global event connection for the signed-in app's lifetime.
  useEffect(() => {
    if (!user) return;
    eventBus.connect();
    return () => eventBus.disconnect();
  }, [user]);

  const handleLogout = useCallback(() => {
    api.auth.logout().catch(() => undefined);
    setUser(null);
  }, []);

  if (loading) {
    return <div className="login"><div className="login__loading">Loading…</div></div>;
  }

  if (!user) {
    return <LoginPage onLogin={setUser} />;
  }

  return (
    <BrowserRouter>
      <Routes>
        <Route element={<Layout user={user} onLogout={handleLogout} />}>
          <Route path="/" element={<AuthPage />} />
          <Route path="/nodes" element={<NodesPage />} />
          <Route path="/sessions" element={<SessionsPage />} />
          <Route path="/sessions/:id" element={<SessionDetailPage />} />
          <Route path="/workspaces" element={<WorkspacesPage />} />
          <Route path="/workspaces/:id" element={<WorkspaceDetailPage />} />
          <Route path="/docs" element={<DocsPage />} />
          <Route path="/docs/:name" element={<DocsPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
