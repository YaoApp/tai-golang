package node

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/session"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"github.com/yaoapp/tai-golang/workspace"
	"google.golang.org/protobuf/proto"
)

// remoteNode implements types.RemoteNode by combining registry lookup
// with transport and workspace operations.
type remoteNode struct {
	nodeID   string
	registry *Registry
	policy   *workspace.ChannelPolicy
}

// authProvider returns the registry auth provider or an error when the
// embedding server has not installed one.
func (n *remoteNode) authProvider() (types.AuthProvider, error) {
	auth := n.registry.Auth()
	if auth == nil {
		return nil, fmt.Errorf("node: auth provider not configured")
	}
	return auth, nil
}

// normalizeScopes applies the default session scope set when the caller did
// not declare any: opening/driving a session needs session:access.
func normalizeScopes(scopes types.Scope) types.Scope {
	if len(scopes) == 0 {
		return types.DefaultSessionScopes()
	}
	return scopes
}

// issueIdentity issues a user credential for the given scopes via the
// registry auth provider and converts it into the wire identity carried by the
// first frame of a business stream. When the provider reports credential
// expiry, it is attached as the local GC hint (Identity.expires_at).
//
// Providers implementing UserTokenContextProvider receive ctx, so they can
// resolve the caller from the request's login state (identity.md §2.4.4).
func (n *remoteNode) issueIdentity(ctx context.Context, scopes types.Scope) (*pb.Identity, error) {
	auth, err := n.authProvider()
	if err != nil {
		return nil, err
	}
	scopes = normalizeScopes(scopes)

	var token string
	var identity *types.UserIdentity
	if ctp, ok := auth.(types.UserTokenContextProvider); ok {
		token, identity, err = ctp.IssueUserTokenContext(ctx, n.nodeID, scopes)
	} else {
		token, identity, err = auth.IssueUserToken(n.nodeID, scopes)
	}
	if err != nil {
		return nil, err
	}
	return types.IdentityToPB(identity, token, tokenExpiresAt(auth, token)), nil
}

// tokenExpiresAt returns the credential expiry as a Unix GC hint, or 0 when
// the provider does not expose it. It is informational only: Tai never
// validates it.
func tokenExpiresAt(auth types.AuthProvider, token string) int64 {
	reporter, ok := auth.(types.UserTokenExpiry)
	if !ok {
		return 0
	}
	expiry, err := reporter.UserTokenExpiresAt(token)
	if err != nil || expiry.IsZero() {
		return 0
	}
	return expiry.Unix()
}

// userTokenRefresher returns a callback that keeps the user credential fresh
// for a long-running session. It follows the credential contract: renew the
// existing credential first (RefreshUserToken inherits identity and scopes and
// only extends the validity window); when the old credential can no longer be
// renewed, fall back to issuing a fresh one from the business context.
func (n *remoteNode) userTokenRefresher(identity *pb.Identity, scopes types.Scope) func() (string, time.Time, error) {
	scopes = normalizeScopes(scopes)
	current := identity.GetUserToken()

	var mu sync.Mutex
	return func() (string, time.Time, error) {
		mu.Lock()
		defer mu.Unlock()

		auth, err := n.authProvider()
		if err != nil {
			return "", time.Time{}, err
		}
		if current != "" {
			if renewed, err := auth.RefreshUserToken(current); err == nil {
				current = renewed
				return renewed, expiryTime(auth, renewed), nil
			}
		}
		token, _, err := auth.IssueUserToken(n.nodeID, scopes)
		if err != nil {
			return "", time.Time{}, err
		}
		current = token
		return token, expiryTime(auth, token), nil
	}
}

// expiryTime returns the credential expiry (zero when unknown).
func expiryTime(auth types.AuthProvider, token string) time.Time {
	reporter, ok := auth.(types.UserTokenExpiry)
	if !ok {
		return time.Time{}
	}
	expiry, err := reporter.UserTokenExpiresAt(token)
	if err != nil {
		return time.Time{}
	}
	return expiry
}

// ---------- types.RemoteNode implementation ----------

func (n *remoteNode) Info() *types.Node {
	return n.registry.NodeByID(n.nodeID)
}

func (n *remoteNode) Online() bool {
	info := n.registry.NodeByID(n.nodeID)
	return info != nil && info.Online
}

func (n *remoteNode) OpenWorkspace(ctx context.Context, wsID string, branch ...string) (types.Workspace, error) {
	br := "main"
	if len(branch) > 0 && branch[0] != "" {
		br = branch[0]
	}

	req := workspace.WSRequest{WorkspaceID: wsID}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("workspace: marshal open: %w", err)
	}

	// Workspace first frames keep the identity slot for when the business
	// layer declares workspace scopes; the demo binding does not attach one
	// yet (identity is optional per namespace).
	firstFrame := transport.FirstFrame(workspace.NamespaceWorkspace, workspace.KindWSOpen, data, nil)
	stream, err := transport.PkgOpenLightStream(ctx, n.nodeID, firstFrame)
	if err != nil {
		return nil, fmt.Errorf("workspace: open %s: %w", wsID, err)
	}
	defer stream.Close()

	resp, err := workspace.ReadResponse(stream, workspace.KindWSOpen)
	if err != nil {
		return nil, fmt.Errorf("workspace: open %s: %w", wsID, err)
	}

	var meta types.Meta
	if err := json.Unmarshal(resp, &meta); err != nil {
		return nil, fmt.Errorf("workspace: unmarshal meta: %w", err)
	}

	return workspace.NewRemoteWorkspace(n.nodeID, wsID, br, meta, n.policy), nil
}

func (n *remoteNode) CreateWorkspace(ctx context.Context, meta types.Meta) (types.Workspace, error) {
	req := workspace.WSCreateRequest{Meta: meta}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("workspace: marshal create: %w", err)
	}

	firstFrame := transport.FirstFrame(workspace.NamespaceWorkspace, workspace.KindWSCreate, data, nil)
	stream, err := transport.PkgOpenLightStream(ctx, n.nodeID, firstFrame)
	if err != nil {
		return nil, fmt.Errorf("workspace: create: %w", err)
	}
	defer stream.Close()

	resp, err := workspace.ReadResponse(stream, workspace.KindWSCreate)
	if err != nil {
		return nil, fmt.Errorf("workspace: create: %w", err)
	}

	var result struct {
		ID   string     `json:"id"`
		Meta types.Meta `json:"meta"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("workspace: unmarshal create result: %w", err)
	}

	return workspace.NewRemoteWorkspace(n.nodeID, result.ID, "main", result.Meta, n.policy), nil
}

func (n *remoteNode) ListWorkspaces(ctx context.Context, opts types.ListOptions) ([]types.RegistryEntry, error) {
	req := workspace.WSListRequest{Opts: opts}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("workspace: marshal list: %w", err)
	}

	firstFrame := transport.FirstFrame(workspace.NamespaceWorkspace, workspace.KindWSList, data, nil)
	stream, err := transport.PkgOpenLightStream(ctx, n.nodeID, firstFrame)
	if err != nil {
		return nil, fmt.Errorf("workspace: list: %w", err)
	}
	defer stream.Close()

	resp, err := workspace.ReadResponse(stream, workspace.KindWSList)
	if err != nil {
		return nil, fmt.Errorf("workspace: list: %w", err)
	}

	var entries []types.RegistryEntry
	if err := json.Unmarshal(resp, &entries); err != nil {
		return nil, fmt.Errorf("workspace: unmarshal list: %w", err)
	}
	return entries, nil
}

func (n *remoteNode) DeleteWorkspace(ctx context.Context, wsID string) error {
	req := workspace.WSRequest{WorkspaceID: wsID}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("workspace: marshal delete: %w", err)
	}

	firstFrame := transport.FirstFrame(workspace.NamespaceWorkspace, workspace.KindWSDelete, data, nil)
	stream, err := transport.PkgOpenLightStream(ctx, n.nodeID, firstFrame)
	if err != nil {
		return fmt.Errorf("workspace: delete %s: %w", wsID, err)
	}
	defer stream.Close()

	_, err = workspace.ReadResponse(stream, workspace.KindWSDelete)
	if err != nil {
		return fmt.Errorf("workspace: delete %s: %w", wsID, err)
	}
	return nil
}

// ---------- Session lifecycle ----------

func (n *remoteNode) OpenSession(ctx context.Context, req *types.SessionOpenRequest) (types.Session, error) {
	var identity *pb.Identity
	if !req.Anonymous {
		var err error
		identity, err = n.issueIdentity(ctx, req.Scopes)
		if err != nil {
			return nil, fmt.Errorf("issue user token: %w", err)
		}
	}
	pbReq := sessionRequestToProto(req.Request)
	openData, err := proto.Marshal(&pb.SessionOpen{
		SessionId: req.SessionID,
		NodeId:    n.nodeID,
		Request:   pbReq,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal SessionOpen: %w", err)
	}

	ctrlStream, err := transport.PkgOpenLightStream(ctx, n.nodeID,
		transport.FirstFrame(types.NamespaceSession, "open", openData, identity))
	if err != nil {
		return nil, fmt.Errorf("open ctrl stream: %w", err)
	}

	codec := transport.DefaultCodec()
	respFrame, err := codec.ReadFrame(ctrlStream)
	if err != nil {
		ctrlStream.Close()
		return nil, fmt.Errorf("read opened: %w", err)
	}
	switch respFrame.Kind {
	case "opened":
		// OK
	case "error":
		var se pb.SessionError
		proto.Unmarshal(respFrame.Data, &se)
		ctrlStream.Close()
		return nil, fmt.Errorf("session open rejected: %s: %s", se.Code, se.Message)
	default:
		ctrlStream.Close()
		return nil, fmt.Errorf("unexpected response kind: %s", respFrame.Kind)
	}

	dataData, err := proto.Marshal(&pb.SessionData{SessionId: req.SessionID})
	if err != nil {
		ctrlStream.Close()
		return nil, fmt.Errorf("marshal SessionData: %w", err)
	}
	// The data stream is a separate Light stream: its first frame repeats the
	// identity so a node that only sees this stream can still bind the
	// session to its caller.
	dataStream, err := transport.PkgOpenLightStream(ctx, n.nodeID,
		transport.FirstFrame(types.NamespaceSession, "data", dataData, identity))
	if err != nil {
		ctrlStream.Close()
		return nil, fmt.Errorf("open data stream: %w", err)
	}

	// Extract DatagramConn from the ctrl LightStream for Realtime audio.
	var dgConn transport.DatagramConn
	if ls, ok := ctrlStream.(*transport.LightStream); ok {
		dgConn = ls.DatagramConn()
	}

	resumeFn := n.makeResumeFn(req.SessionID, req.Scopes, req.Anonymous)
	sess := session.New(req.SessionID, n.nodeID, ctrlStream, dataStream, codec, session.Options{
		ResumeFn:     resumeFn,
		OnEvent:      req.OnEvent,
		DatagramConn: dgConn,
		Identity:     identity,
	})

	if !req.Anonymous {
		// Keep the daemon-side user credential fresh for long-running
		// sessions: each tick renews the credential and pushes it over the
		// Control stream (identity.md §4.3).
		tr := session.NewTokenRefresher(req.SessionID, n.userTokenRefresher(identity, req.Scopes), n.SendControl)
		go func() {
			<-sess.Done()
			tr.Stop()
		}()
	}

	return sess, nil
}

func (n *remoteNode) ResumeSession(ctx context.Context, req *types.ResumeSessionRequest) (types.Session, error) {
	codec := transport.DefaultCodec()
	newCtrl, newData, _, identity, err := n.doResume(ctx, req.SessionID, req.Scopes, req.Anonymous, req.LastCtrlSeq, req.LastDataSeq, codec)
	if err != nil {
		return nil, err
	}
	// Extract DatagramConn from the ctrl LightStream for Realtime audio.
	var dgConn transport.DatagramConn
	if ls, ok := newCtrl.(*transport.LightStream); ok {
		dgConn = ls.DatagramConn()
	}

	resumeFn := n.makeResumeFn(req.SessionID, req.Scopes, req.Anonymous)
	sess := session.New(req.SessionID, n.nodeID, newCtrl, newData, codec, session.Options{
		ResumeFn:     resumeFn,
		OnEvent:      req.OnEvent,
		DatagramConn: dgConn,
		Identity:     identity,
	})
	return sess, nil
}

// makeResumeFn creates a ResumeFunc closure for automatic session resume.
// A fresh user credential is issued on every resume attempt.
func (n *remoteNode) makeResumeFn(sessionID string, scopes types.Scope, anonymous bool) types.ResumeFunc {
	return func(ctx context.Context, lastCtrlSeq, lastDataSeq uint64) (io.ReadWriteCloser, io.ReadWriteCloser, uint64, error) {
		codec := transport.DefaultCodec()
		ctrl, data, lastSeq, _, err := n.doResume(ctx, sessionID, scopes, anonymous, lastCtrlSeq, lastDataSeq, codec)
		return ctrl, data, lastSeq, err
	}
}

// doResume opens new ctrl (with resume first frame) and data streams. A fresh
// user credential is issued on every resume: the resumed stream may represent
// a different business context than the original open.
func (n *remoteNode) doResume(ctx context.Context, sessionID string, scopes types.Scope, anonymous bool, lastCtrlSeq, lastDataSeq uint64, codec *transport.FrameCodec) (io.ReadWriteCloser, io.ReadWriteCloser, uint64, *pb.Identity, error) {
	var identity *pb.Identity
	if !anonymous {
		var err error
		identity, err = n.issueIdentity(ctx, scopes)
		if err != nil {
			return nil, nil, 0, nil, fmt.Errorf("issue user token: %w", err)
		}
	}
	resumeData, err := proto.Marshal(&pb.SessionResume{
		SessionId:   sessionID,
		LastCtrlSeq: lastCtrlSeq,
		LastDataSeq: lastDataSeq,
	})
	if err != nil {
		return nil, nil, 0, nil, fmt.Errorf("marshal SessionResume: %w", err)
	}

	ctrlStream, err := transport.PkgOpenLightStream(ctx, n.nodeID,
		transport.FirstFrame(types.NamespaceSession, "resume", resumeData, identity))
	if err != nil {
		return nil, nil, 0, nil, fmt.Errorf("open resume ctrl stream: %w", err)
	}

	respFrame, err := codec.ReadFrame(ctrlStream)
	if err != nil {
		ctrlStream.Close()
		return nil, nil, 0, nil, fmt.Errorf("read resume response: %w", err)
	}
	switch respFrame.Kind {
	case "opened":
		// OK
	case "error":
		var se pb.SessionError
		proto.Unmarshal(respFrame.Data, &se)
		ctrlStream.Close()
		return nil, nil, 0, nil, fmt.Errorf("session resume rejected: %s: %s", se.Code, se.Message)
	default:
		ctrlStream.Close()
		return nil, nil, 0, nil, fmt.Errorf("unexpected resume response kind: %s", respFrame.Kind)
	}

	var opened pb.SessionOpened
	if err := proto.Unmarshal(respFrame.Data, &opened); err != nil {
		ctrlStream.Close()
		return nil, nil, 0, nil, fmt.Errorf("unmarshal SessionOpened: %w", err)
	}

	dataData, err := proto.Marshal(&pb.SessionData{SessionId: sessionID})
	if err != nil {
		ctrlStream.Close()
		return nil, nil, 0, nil, fmt.Errorf("marshal SessionData: %w", err)
	}
	// The data stream is a separate Light stream: its first frame repeats the
	// identity so a node that only sees this stream can still bind the
	// session to its caller.
	dataStream, err := transport.PkgOpenLightStream(ctx, n.nodeID,
		transport.FirstFrame(types.NamespaceSession, "data", dataData, identity))
	if err != nil {
		ctrlStream.Close()
		return nil, nil, 0, nil, fmt.Errorf("open resume data stream: %w", err)
	}

	return ctrlStream, dataStream, opened.LastDataSeq, identity, nil
}

// ---------- Control ----------

func (n *remoteNode) SendControl(msg *types.ControlMsg) error {
	return n.registry.SendControl(n.nodeID, &pb.Payload{
		Namespace: types.NamespaceControl,
		Kind:      msg.Kind,
		Data:      msg.Data,
	})
}

func (n *remoteNode) GoAway(reason string, reconnectAfter uint32) error {
	return n.registry.GoAway(n.nodeID, reason, reconnectAfter)
}

// ---------- Helpers ----------

// sessionRequestToProto converts types.SessionRequest → pb.SessionRequest.
func sessionRequestToProto(req *types.SessionRequest) *pb.SessionRequest {
	if req == nil {
		return &pb.SessionRequest{}
	}
	var metaBytes []byte
	if req.Meta != nil {
		metaBytes, _ = json.Marshal(req.Meta)
	}
	pr := &pb.SessionRequest{
		Runtime:               req.Runtime,
		Isolation:             req.Isolation,
		IdleTimeoutSec:        req.IdleTimeout,
		MaxDurationSec:        req.MaxDuration,
		Cwd:                   req.Cwd,
		AdditionalDirectories: req.AdditionalDirectories,
		AssistantId:           req.AssistantID,
		WorkspaceId:           req.WorkspaceID,
		Llm:                   []byte(req.LLM),
		Env:                   req.Env,
		Config:                []byte(req.Config),
		Metadata:              req.Metadata,
		Meta:                  metaBytes,
	}
	for _, ms := range req.McpServers {
		pbMs := &pb.McpServer{
			Type:    ms.Type,
			Name:    ms.Name,
			Command: ms.Command,
			Args:    ms.Args,
			Url:     ms.URL,
			Headers: ms.Headers,
		}
		for _, ev := range ms.Env {
			pbMs.Env = append(pbMs.Env, &pb.EnvVariable{
				Name:  ev.Name,
				Value: ev.Value,
			})
		}
		pr.McpServers = append(pr.McpServers, pbMs)
	}
	return pr
}
