package node

import (
	"context"
	"encoding/json"
	"fmt"

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

	firstFrame := &pb.Payload{
		Namespace: workspace.NamespaceWorkspace,
		Kind:      workspace.KindWSOpen,
		Data:      data,
	}
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

func (n *remoteNode) CreateWorkspace(ctx context.Context, path string, meta types.Meta) (types.Workspace, error) {
	req := workspace.WSCreateRequest{Path: path, Meta: meta}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("workspace: marshal create: %w", err)
	}

	firstFrame := &pb.Payload{
		Namespace: workspace.NamespaceWorkspace,
		Kind:      workspace.KindWSCreate,
		Data:      data,
	}
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

	firstFrame := &pb.Payload{
		Namespace: workspace.NamespaceWorkspace,
		Kind:      workspace.KindWSList,
		Data:      data,
	}
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

	firstFrame := &pb.Payload{
		Namespace: workspace.NamespaceWorkspace,
		Kind:      workspace.KindWSDelete,
		Data:      data,
	}
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
	pbReq := sessionRequestToProto(req.Request)
	openData, err := proto.Marshal(&pb.SessionOpen{
		SessionId: req.SessionID,
		NodeId:    n.nodeID,
		Token:     req.Token,
		Request:   pbReq,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal SessionOpen: %w", err)
	}

	ctrlStream, err := transport.PkgOpenLightStream(ctx, n.nodeID, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "open",
		Data:      openData,
	})
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
	dataStream, err := transport.PkgOpenLightStream(ctx, n.nodeID, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "data",
		Data:      dataData,
	})
	if err != nil {
		ctrlStream.Close()
		return nil, fmt.Errorf("open data stream: %w", err)
	}

	sess := session.New(req.SessionID, n.nodeID, ctrlStream, dataStream, codec)
	return sess, nil
}

func (n *remoteNode) ResumeSession(_ context.Context, _ *types.ResumeSessionRequest) (types.Session, error) {
	return nil, fmt.Errorf("tai: ResumeSession not implemented")
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
