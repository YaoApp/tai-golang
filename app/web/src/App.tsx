import { BrowserRouter, Routes, Route } from 'react-router-dom';
import Layout from './components/Layout';
import AuthPage from './pages/auth/AuthPage';
import NodesPage from './pages/nodes/NodesPage';
import SessionsPage from './pages/sessions/SessionsPage';
import SessionDetailPage from './pages/sessions/SessionDetailPage';
import DocsPage from './pages/docs/DocsPage';

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route path="/" element={<AuthPage />} />
          <Route path="/nodes" element={<NodesPage />} />
          <Route path="/sessions" element={<SessionsPage />} />
          <Route path="/sessions/:id" element={<SessionDetailPage />} />
          <Route path="/docs" element={<DocsPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
