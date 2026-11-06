import { useEffect } from 'react';
import { BrowserRouter, Routes, Route } from 'react-router-dom';
import Layout from './components/Layout';
import AuthPage from './pages/auth/AuthPage';
import NodesPage from './pages/nodes/NodesPage';
import SessionsPage from './pages/sessions/SessionsPage';
import SessionDetailPage from './pages/sessions/SessionDetailPage';
import DocsPage from './pages/docs/DocsPage';
import WorkspacesPage from './pages/workspaces/WorkspacesPage';
import WorkspaceDetailPage from './pages/workspaces/WorkspaceDetailPage';
import { eventBus } from './lib/events';

export default function App() {
  // Keep one global event connection for the app's lifetime; pages subscribe
  // by prefix.
  useEffect(() => {
    eventBus.connect();
    return () => eventBus.disconnect();
  }, []);

  return (
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route path="/" element={<AuthPage />} />
          <Route path="/nodes" element={<NodesPage />} />
          <Route path="/sessions" element={<SessionsPage />} />
          <Route path="/sessions/:id" element={<SessionDetailPage />} />
          <Route path="/workspaces" element={<WorkspacesPage />} />
          <Route path="/workspaces/:id" element={<WorkspaceDetailPage />} />
          <Route path="/docs" element={<DocsPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
