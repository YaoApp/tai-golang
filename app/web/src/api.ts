const BASE = '';

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    ...init,
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(body.error || res.statusText);
  }
  return res.json();
}

/** Demo account returned by the login/registration endpoints. */
export interface DemoUser {
  email: string;
  name?: string;
  createdAt: string;
}

export interface AuthInfo {
  transportPort: number;
  protocols: string[];
  version: string;
}

export interface NodeInfo {
  id: string;
  taiId: string;
  /** Tai binary version reported in the node's Register message. */
  version?: string;
  os?: string;
  arch?: string;
  capabilities?: string[];
  registeredAt: string;
}

/**
 * Verified user identity behind a user credential.
 *
 * Mirrors tai-golang/types.UserIdentity. `scope` lists the actions the
 * credential may start; it is not an object-level authorization grant.
 */
export interface UserIdentity {
  user_id: string;
  team_id: string;
  scope: string[];
}

/** Result of POST /api/auth/user-token. */
export interface UserTokenResult {
  token: string;
  identity: UserIdentity;
}

export interface SessionInfo {
  id: string;
  nodeId: string;
  createdAt: string;
  status: string;
  workspaceId?: string;
  workspaceName?: string;
  /** User the session belongs to (empty when the session has no user). */
  userId?: string;
  /** Team the session belongs to. */
  teamId?: string;
}

export interface DocEntry {
  name: string;
  title: string;
}

export interface WorkspaceMeta {
  name: string;
  owner: string;
}

export interface WorkspaceEntry {
  id: string;
  name: string;
  owner: string;
  created_at?: string;
  updated_at?: string;
}

export interface BranchInfo {
  name: string;
  head: string;
  meta?: Record<string, unknown>;
}

export interface CommitInfo {
  id: string;
  parent?: string;
  message: string;
  time: number;
}

export interface CommitEntry extends CommitInfo {
  branch: string;
}

export interface MergeResult {
  commit_id: string;
  conflicts?: string[];
}

export interface FileStatEntry {
  name: string;
  isDir: boolean;
  size?: number;
  modTime?: number;
}

export interface FileStat {
  isDir: boolean;
  path: string;
  name?: string;
  size?: number;
  modTime?: number;
  entries?: FileStatEntry[];
}

export const api = {
  auth: {
    info: () => request<AuthInfo>('/api/auth/info'),
    register: (email: string, password: string, name?: string) =>
      request<{ user: DemoUser }>('/api/auth/register', {
        method: 'POST',
        body: JSON.stringify({ email, password, name }),
      }),
    login: (email: string, password: string) =>
      request<{ user: DemoUser }>('/api/auth/login', {
        method: 'POST',
        body: JSON.stringify({ email, password }),
      }),
    logout: () =>
      request<{ ok: boolean }>('/api/auth/logout', { method: 'POST' }),
    me: () => request<{ user: DemoUser }>('/api/auth/me'),
    issueNodeToken: (nodeId: string) =>
      request<{ token: string }>('/api/auth/node-token', {
        method: 'POST',
        body: JSON.stringify({ nodeId }),
      }),
    /**
     * Issue a v2 user credential for one business operation.
     *
     * The caller declares the target node and the action scopes it needs; the
     * server resolves the user from its business context and returns both the
     * token and the verified identity. The old operation-token endpoint is gone.
     */
    issueUserToken: (nodeId: string, scopes: string[]) =>
      request<UserTokenResult>('/api/auth/user-token', {
        method: 'POST',
        body: JSON.stringify({ nodeId, scopes }),
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
    resume: (id: string) =>
      request<{ id: string; status: string; userId?: string; teamId?: string }>(
        `/api/sessions/${id}/resume`,
        { method: 'POST' },
      ),
  },
  docs: {
    list: () => request<DocEntry[]>('/api/docs'),
    get: async (name: string) => {
      const res = await fetch(`${BASE}/api/docs/${name}`);
      // Markdown is returned as text, so the JSON error path in request() does
      // not apply; a missing document must still reject rather than render the
      // error body as if it were content.
      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: res.statusText }));
        throw new Error(body.error || `HTTP ${res.status}`);
      }
      return res.text();
    },
  },
  workspaces: {
    list: (nodeId: string) =>
      request<WorkspaceEntry[]>(`/api/workspaces?nodeId=${nodeId}`),
    create: (nodeId: string, name: string, owner?: string) =>
      request<{ id: string; meta: WorkspaceMeta }>('/api/workspaces', {
        method: 'POST',
        body: JSON.stringify({ nodeId, name, owner }),
      }),
    delete: (nodeId: string, id: string) =>
      request<{ status: string }>(`/api/workspaces/${id}?nodeId=${nodeId}`, { method: 'DELETE' }),
    updateMeta: (nodeId: string, id: string, meta: WorkspaceMeta) =>
      request<{ status: string }>(`/api/workspaces/${id}/meta?nodeId=${nodeId}`, {
        method: 'PUT',
        body: JSON.stringify(meta),
      }),
    branches: {
      list: (nodeId: string, wsId: string) =>
        request<BranchInfo[]>(`/api/workspaces/${wsId}/branches?nodeId=${nodeId}`),
      create: (nodeId: string, wsId: string, name: string, from?: string) =>
        request<{ name: string }>(`/api/workspaces/${wsId}/branches?nodeId=${nodeId}`, {
          method: 'POST',
          body: JSON.stringify({ name, from: from || 'main' }),
        }),
      delete: (nodeId: string, wsId: string, name: string) =>
        request<{ status: string }>(`/api/workspaces/${wsId}/branches/${name}?nodeId=${nodeId}`, { method: 'DELETE' }),
      commit: (nodeId: string, wsId: string, branch: string, message: string) =>
        request<CommitInfo>(`/api/workspaces/${wsId}/branches/${branch}/commit?nodeId=${nodeId}`, {
          method: 'POST',
          body: JSON.stringify({ message }),
        }),
      rollback: (nodeId: string, wsId: string, branch: string, commitId: string) =>
        request<{ status: string }>(`/api/workspaces/${wsId}/branches/${branch}/rollback?nodeId=${nodeId}`, {
          method: 'POST',
          body: JSON.stringify({ commitId }),
        }),
      log: (nodeId: string, wsId: string, branch: string, limit?: number) =>
        request<CommitEntry[]>(`/api/workspaces/${wsId}/branches/${branch}/log?nodeId=${nodeId}&limit=${limit || 50}`),
    },
    merge: (nodeId: string, wsId: string, from: string, into: string, opts?: { deleteSource?: boolean; message?: string }) =>
      request<MergeResult>(`/api/workspaces/${wsId}/merge?nodeId=${nodeId}`, {
        method: 'POST',
        body: JSON.stringify({ from, into, ...opts }),
      }),
    history: (nodeId: string, wsId: string, limit?: number) =>
      request<CommitEntry[]>(`/api/workspaces/${wsId}/history?nodeId=${nodeId}&limit=${limit || 50}`),
    files: {
      stat: (nodeId: string, wsId: string, branch: string, path: string) =>
        request<FileStat>(`/api/workspaces/${wsId}/branches/${branch}/files?nodeId=${nodeId}&path=${encodeURIComponent(path)}`),
      content: (nodeId: string, wsId: string, branch: string, path: string) =>
        fetch(`/api/workspaces/${wsId}/branches/${branch}/files/content?nodeId=${nodeId}&path=${encodeURIComponent(path)}`),
      upload: (nodeId: string, wsId: string, branch: string, path: string, body: BodyInit) =>
        fetch(`/api/workspaces/${wsId}/branches/${branch}/files/content?nodeId=${nodeId}&path=${encodeURIComponent(path)}`, {
          method: 'PUT',
          body,
        }).then((r) => r.json()),
      delete: (nodeId: string, wsId: string, branch: string, path: string, recursive?: boolean) =>
        request<{ status: string }>(`/api/workspaces/${wsId}/branches/${branch}/files?nodeId=${nodeId}&path=${encodeURIComponent(path)}${recursive ? '&recursive=true' : ''}`, { method: 'DELETE' }),
      mkdir: (nodeId: string, wsId: string, branch: string, path: string) =>
        request<{ status: string }>(`/api/workspaces/${wsId}/branches/${branch}/files/mkdir?nodeId=${nodeId}&path=${encodeURIComponent(path)}`, { method: 'POST' }),
      rename: (nodeId: string, wsId: string, branch: string, oldPath: string, newPath: string) =>
        request<{ status: string }>(`/api/workspaces/${wsId}/branches/${branch}/files/rename?nodeId=${nodeId}`, {
          method: 'POST',
          body: JSON.stringify({ oldPath, newPath }),
        }),
      previewUrl: (nodeId: string, wsId: string, branch: string, path: string) =>
        `/api/workspaces/${wsId}/branches/${branch}/files/preview?nodeId=${nodeId}&path=${encodeURIComponent(path)}`,
    },
    /**
     * Context API: the INSTRUCTION/MEMORY/PREFERENCE files of each scope.
     *
     * Note: the Tai runtime does not read .context/, so this content never
     * reaches the agent prompt.
     */
    context: {
      read: (nodeId: string, wsId: string, ids: ScopeIds) =>
        request<{ scopes: ContextScopeView[] }>(
          `/api/workspaces/${wsId}/context?nodeId=${nodeId}&ids=${encodeScopeIds(ids)}`,
        ),
      writeFile: (nodeId: string, wsId: string, scope: string, scopeID: string, file: string, content: string) =>
        fetch(
          `/api/workspaces/${wsId}/context/${scope}/${encodeURIComponent(scopeID)}/${file}?nodeId=${nodeId}`,
          { method: 'PUT', body: content, headers: { 'Content-Type': 'text/plain' } },
        ).then((r) => r.json()),
      seed: (nodeId: string, wsId: string, scope: string, scopeID: string, files: Record<string, string>) =>
        request<{ status: string }>(
          `/api/workspaces/${wsId}/context/${scope}/${encodeURIComponent(scopeID)}/seed?nodeId=${nodeId}`,
          { method: 'POST', body: JSON.stringify({ files }) },
        ),
      merged: (nodeId: string, wsId: string, ids: ScopeIds) =>
        request<{ merged: string }>(
          `/api/workspaces/${wsId}/context/merged?nodeId=${nodeId}&ids=${encodeScopeIds(ids)}`,
        ),
    },
    /** Skills API: skill packages synced under .skills/. */
    skills: {
      list: (nodeId: string, wsId: string) =>
        request<{ skills: SkillEntry[] }>(`/api/workspaces/${wsId}/skills?nodeId=${nodeId}`),
      upload: (nodeId: string, wsId: string, key: string, file: File) => {
        const form = new FormData();
        form.append('key', key);
        form.append('file', file);
        return fetch(`/api/workspaces/${wsId}/skills?nodeId=${nodeId}`, {
          method: 'POST',
          body: form,
        }).then((r) => r.json() as Promise<{ status: string; key: string; hash: string; pushed: number }>);
      },
    },
  },
};

/** Scope to ID mapping for Context requests. */
export interface ScopeIds {
  bot?: string;
  home?: string;
  board?: string;
  session?: string;
}

/** Context file content for one scope. */
export interface ContextScopeView {
  scope: string;
  id: string;
  files: Record<string, string>;
}

/** A synced skill. */
export interface SkillEntry {
  key: string;
  hash: string;
}

/** Serialize ScopeIds into the backend's "bot:id1,home:id2" form. */
function encodeScopeIds(ids: ScopeIds): string {
  return Object.entries(ids)
    .filter(([, v]) => !!v)
    .map(([k, v]) => `${k}:${v}`)
    .join(',');
}
