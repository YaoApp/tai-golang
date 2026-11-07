import { useState } from 'react';
import { api } from '../../api';
import type { DemoUser } from '../../api';
import Button from '../../components/ui/Button';
import Field from '../../components/ui/Field';
import Panel from '../../components/ui/Panel';

type Mode = 'login' | 'register';

/** Built-in account seeded by the backend; prefilled so login is one click. */
const DEFAULT_EMAIL = 'demo@example.com';
const DEFAULT_PASSWORD = 'demo1234';

interface Props {
  onLogin: (user: DemoUser) => void;
}

/**
 * Demo login/registration gate.
 *
 * The account email becomes the session's user identity: the SDK resolves it
 * from the request context and puts it into the user credential claims.
 */
export default function LoginPage({ onLogin }: Props) {
  const [mode, setMode] = useState<Mode>('login');
  const [email, setEmail] = useState(DEFAULT_EMAIL);
  const [password, setPassword] = useState(DEFAULT_PASSWORD);
  const [name, setName] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      const res =
        mode === 'login'
          ? await api.auth.login(email, password)
          : await api.auth.register(email, password, name);
      onLogin(res.user);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="login">
      <div className="login__card">
        <div className="login__brand">
          <span className="sidebar__mark" />
          <div>
            <div className="login__title">Tai Console</div>
            <div className="login__subtitle">
              Sign in to attribute sessions to a real user identity
            </div>
          </div>
        </div>

        <Panel
          title={mode === 'login' ? 'Sign in' : 'Create account'}
          actions={
            <div className="login__tabs">
              <Button
                size="sm"
                variant={mode === 'login' ? 'primary' : 'ghost'}
                type="button"
                onClick={() => {
                  setMode('login');
                  setError('');
                }}
              >
                Login
              </Button>
              <Button
                size="sm"
                variant={mode === 'register' ? 'primary' : 'ghost'}
                type="button"
                onClick={() => {
                  setMode('register');
                  setError('');
                  // Don't try to register the seeded default account again.
                  if (email === DEFAULT_EMAIL) {
                    setEmail('');
                    setPassword('');
                  }
                }}
              >
                Register
              </Button>
            </div>
          }
        >
          <form onSubmit={submit} className="login__form">
            {mode === 'register' && (
              <Field label="Name">
                <input
                  className="input"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="Alice"
                  autoComplete="name"
                />
              </Field>
            )}
            <Field label="Email">
              <input
                className="input"
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="alice@example.com"
                autoComplete="email"
                required
              />
            </Field>
            <Field label="Password">
              <input
                className="input"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={mode === 'register' ? 'At least 8 characters' : '••••••••'}
                autoComplete={mode === 'login' ? 'current-password' : 'new-password'}
                required
              />
            </Field>
            {error && <div className="notice notice--error">{error}</div>}
            <Button variant="primary" type="submit" disabled={busy}>
              {busy ? 'Working…' : mode === 'login' ? 'Sign in' : 'Create account'}
            </Button>
          </form>
        </Panel>

        <div className="login__hint">
          <div>
            Default account: <code>{DEFAULT_EMAIL}</code> / <code>{DEFAULT_PASSWORD}</code>
            {' '}(prefilled — just press Sign in)
          </div>
          <div>
            Email + password only — accounts live in demo memory. The email becomes
            the <code>user_id</code> in the session's user credential.
          </div>
        </div>
      </div>
    </div>
  );
}
