# Auth

The SDK authenticates two different actors:

| Credential | Actor | Lifetime | Purpose |
| --- | --- | --- | --- |
| `node_token` | Tai node | 7 days with `DefaultAuth` | Identity of the node's Control/Light/Heavy connections |
| `user_token` | End user / service | 30 minutes with `DefaultAuth` | Identity and scopes for one business operation |

`node_token` is long-lived. `user_token` is short-lived, renewable, and carried
only in the first frame of a business stream. The Tai node treats `user_token`
as opaque: it stores and forwards it but never parses it.

## AuthProvider

```go
type AuthProvider interface {
    ValidateNodeToken(token string) (*types.NodeInfo, error)
    IssueNodeToken(nodeID string) (string, error)
    RefreshNodeToken(oldToken string) (string, error)

    IssueUserToken(nodeID string, scopes types.Scope) (string, *types.UserIdentity, error)
    RefreshUserToken(oldToken string) (string, error)
    ValidateUserToken(token string) (*types.UserIdentity, error)

    WellKnownInfo() *types.WellKnownResponse
}
```

`IssueUserToken` is called by the transport when a session is opened or
resumed. The provider resolves the current user from its own business context
and returns both the signed token and the identity that was signed.

Optional interfaces:

```go
type UserTokenContextProvider interface {
    IssueUserTokenContext(ctx context.Context, nodeID string, scopes types.Scope) (string, *types.UserIdentity, error)
}

type UserTokenExpiry interface {
    UserTokenExpiresAt(token string) (time.Time, error)
}
```

- `IssueUserTokenContext` lets the provider read the login state from the
  request context instead of global state.
- `UserTokenExpiresAt` is only used as a local GC hint; Tai never trusts it for
  authorization.

## DefaultAuth

> **Demo only.** `auth.DefaultAuth` is a complete, self-contained auth
> implementation intended to demonstrate the end-to-end flow (Demo App, local
> nodes, examples). It is **not** a production user system: it uses a single
> signing key, an in-memory resolver, and a local fallback identity. Production
> deployments should implement `AuthProvider` and integrate their own user
> database, login state, token issuance, and key management.

`auth.DefaultAuth` returns the SDK's Ed25519 JWT implementation:

```go
import (
    "crypto/ed25519"
    "crypto/rand"

    "github.com/yaoapp/tai-golang/auth"
    "github.com/yaoapp/tai-golang/types"
)

_, privateKey, err := ed25519.GenerateKey(rand.Reader)
if err != nil {
    log.Fatal(err)
}

provider := auth.DefaultAuth(privateKey)
```

Token properties:

- Algorithm: Ed25519 (`EdDSA`)
- `node_token`: 7-day expiry, claims `node_id` / optional `user_id`
- `user_token`: 30-minute expiry, claims `user_id` / `team_id` / `scope` / `metadata`
- `RefreshUserToken` re-signs the old identity and scopes with a new expiry;
  it does not elevate scopes

Use the private key consistently: tokens issued by one key are rejected by a
provider using another key.

### Configure discovery and TLS

```go
provider := auth.DefaultAuth(privateKey)

if p, ok := provider.(interface {
    SetTransportPort(int)
    SetTLS(caCertPEM, fingerprint string)
}); ok {
    p.SetTransportPort(6099)
    p.SetTLS(caPEM, fingerprint)
}
```

`WellKnownInfo` is served at `GET /.well-known/yao-transport` and advertises:

```json
{
  "transport_port": 6099,
  "protocols": ["HTTP/3", "HTTP/2"],
  "tls": {"ca_certificate": "...", "fingerprint": "..."},
  "tool_endpoint": "...",
  "version": "2.0.0"
}
```

The `tool_endpoint` field is filled by the server when remote tools are
configured; it is normally the same `transport_port`.

### Resolve the user

The default resolver returns `local-user` / `local-team` so local development
works without a user database. Production integrations install a resolver:

```go
if p, ok := provider.(interface {
    SetUserResolver(func(nodeID string, scopes types.Scope) (*types.UserIdentity, error))
}); ok {
    p.SetUserResolver(func(nodeID string, scopes types.Scope) (*types.UserIdentity, error) {
        return &types.UserIdentity{
            UserID: "user-1",
            TeamID: "team-1",
            Scope:  scopes,
        }, nil
    })
}
```

Use `SetUserResolverContext` when the caller comes from the request context:

```go
if p, ok := provider.(interface {
    SetUserResolverContext(func(context.Context, string, types.Scope) (*types.UserIdentity, error))
}); ok {
    p.SetUserResolverContext(func(ctx context.Context, nodeID string, scopes types.Scope) (*types.UserIdentity, error) {
        user, ok := userFromContext(ctx)
        if !ok {
            return nil, context.Canceled
        }
        return &types.UserIdentity{
            UserID: user.ID,
            TeamID: user.TeamID,
            Scope:  scopes,
            Metadata: map[string]any{"name": user.Name},
        }, nil
    })
}
```

The context resolver wins over the legacy resolver. A resolver error aborts the
session open/resume.

## Scopes

Scopes are `<resource>:<operation>` strings. A resource wildcard (`workspace:*`)
covers every action of that resource; there is no global wildcard.

```go
const (
    types.SessionAccess   = "session:access"
    types.WorkspaceRead   = "workspace:read"
    types.WorkspaceWrite  = "workspace:write"
    types.WorkspaceDelete = "workspace:delete"
    types.WorkspaceAll    = "workspace:*"
    types.RemoteToolCall  = "remote_tool:call"
)
```

```go
scopes := types.Scope{
    types.SessionAccess,
    types.WorkspaceRead,
    types.WorkspaceWrite,
    types.RemoteToolCall,
}

if scopes.Has(types.WorkspaceRead) { /* allowed */ }
```

`SessionOpenRequest.Scopes` and `ResumeSessionRequest.Scopes` declare what the
caller needs. When empty, the transport defaults to `{session:access}`.

## Token flow

### Node registration

```text
node                         SDK
  │  IssueNodeToken(nodeID)   │  (provisioning, out of band)
  │  register (node_token)    │
  ├──────────────────────────►│  ValidateNodeToken
  │  register_ack             │
  │◄──────────────────────────┤  TaiID + pool config
```

### Session open

```text
caller                         SDK                     node
  │ OpenSession(ctx, req)        │                       │
  ├─────────────────────────────►│                       │
  │                              │ IssueUserTokenContext │
  │                              │   → user_token        │
  │                              │ first frame identity  │
  │                              ├──────────────────────►│
  │                              │                       │ ValidateUserToken
  │                              │                       │ (forwarded to SDK
  │                              │                       │  only for Remote Tool)
```

The node's daemon stores the user credential with the sandbox/session and
forwards it to the Tool H2 endpoint when the Remote Tool layer validates
scopes. Agent processes and sandbox files never receive `user_token`.

### Refresh

`user_token` is refreshed proactively for long-running sessions:

1. Try `RefreshUserToken(old)` to inherit identity/scopes.
2. If the old token can no longer be refreshed, issue a fresh token from the
   business context.
3. The node keeps using the credential string; it does not interpret claims.

## Custom AuthProvider

Implement the full interface and pass it to `ServerConfig`:

```go
server, err := tai.NewServer(&tai.ServerConfig{
    Auth:          myAuthProvider,
    TransportPort: 6099,
    HTTPPort:      5099,
})
```

Requirements:

- `ValidateNodeToken` must reject tampered, expired, or wrong-key tokens.
- `IssueNodeToken` should return a credential valid long enough for node
  reconnects; the default is 7 days.
- `IssueUserToken` must return a non-empty `UserID` and `TeamID`; otherwise the
  transport rejects the credential.
- `ValidateUserToken` must verify signature, expiry, and required identity
  fields.
- `WellKnownInfo` should advertise the actual transport port and TLS settings.

A custom provider may use its own JWT library, opaque tokens, or database
lookups, as long as the semantics above hold.

## Security rules

- Never log `node_token` or `user_token`.
- Do not put `user_token` in Agent environment, sandbox files, or tool output.
- Do not accept a `user_token` as a `node_token` or vice versa; token purpose
  must be explicit.
- Enforce scopes at the business handler; a valid token is not automatically
  authorized for every operation.
