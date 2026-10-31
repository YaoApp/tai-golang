const BASE = '';

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(body.error || res.statusText);
  }
  return res.json();
}

export interface AuthInfo {
  transportPort: number;
  protocols: string[];
  version: string;
  publicKeyId: string;
  publicKey: string;
}

export interface NodeInfo {
  id: string;
  taiId: string;
  registeredAt: string;
}

export interface SessionInfo {
  id: string;
  nodeId: string;
  createdAt: string;
  status: string;
}

export interface DocEntry {
  name: string;
  title: string;
}

export const api = {
  auth: {
    info: () => request<AuthInfo>('/api/auth/info'),
    issueNodeToken: (nodeId: string) =>
      request<{ token: string }>('/api/auth/node-token', {
        method: 'POST',
        body: JSON.stringify({ nodeId }),
      }),
    issueOperationToken: (nodeId: string, sessionId: string) =>
      request<{ token: string }>('/api/auth/operation-token', {
        method: 'POST',
        body: JSON.stringify({ nodeId, sessionId }),
      }),
  },
  nodes: {
    list: () => request<NodeInfo[]>('/api/nodes'),
  },
  sessions: {
    list: () => request<SessionInfo[]>('/api/sessions'),
    create: (nodeId: string, req?: Record<string, unknown>) =>
      request<SessionInfo>('/api/sessions', {
        method: 'POST',
        body: JSON.stringify({ nodeId, request: req }),
      }),
    delete: (id: string) =>
      request<{ status: string }>(`/api/sessions/${id}`, { method: 'DELETE' }),
  },
  docs: {
    list: () => request<DocEntry[]>('/api/docs'),
    get: (name: string) =>
      fetch(`/api/docs/${name}`).then((r) => r.text()),
  },
};
