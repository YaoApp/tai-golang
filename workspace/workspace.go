package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"

	"github.com/yaoapp/tai-golang/pb"
	"google.golang.org/protobuf/proto"
)

// Transport is the interface workspace uses to communicate with the Tai side.
// The SDK transport.Server implements this.
type Transport interface {
	// OpenLightStream opens a bidirectional Light stream with firstFrame as envelope.
	OpenLightStream(ctx context.Context, nodeID string, firstFrame *pb.Payload) (Stream, error)

	// HeavyPick takes an idle Heavy connection for nodeID.
	HeavyPick(nodeID string) (net.Conn, error)

	// HeavyReturn marks a Heavy connection as idle.
	HeavyReturn(conn net.Conn)
}

// Stream is a bidirectional stream (Light H2/H3).
type Stream interface {
	io.ReadWriteCloser
}

// HalfCloser is optionally implemented by Stream to support closing the
// write side while keeping the read side open. H2 streams support this
// natively via END_STREAM on the request body.
type HalfCloser interface {
	CloseWrite() error
}

// ---------- Package-level state ----------

var (
	defaultTransport Transport
	defaultNodeID    string
	defaultPolicy    *ChannelPolicy
)

// Init configures the workspace package with the transport layer.
// Must be called before Open/Create/List/Delete.
func Init(tr Transport, nodeID string, policy *ChannelPolicy) {
	defaultTransport = tr
	defaultNodeID = nodeID
	if policy != nil {
		defaultPolicy = policy
	} else {
		defaultPolicy = DefaultPolicy()
	}
}

// Open creates a Workspace bound to the given workspace ID and branch.
// If no branch is given, defaults to "main".
func Open(ctx context.Context, wsID string, branch ...string) (Workspace, error) {
	if defaultTransport == nil {
		return nil, fmt.Errorf("workspace: not initialized, call workspace.Init first")
	}
	br := "main"
	if len(branch) > 0 && branch[0] != "" {
		br = branch[0]
	}

	// Send ws.open to get metadata
	req := WSRequest{WorkspaceID: wsID}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("workspace: marshal open: %w", err)
	}

	resp, err := roundTrip(ctx, KindWSOpen, data)
	if err != nil {
		return nil, fmt.Errorf("workspace: open %s: %w", wsID, err)
	}

	var meta Meta
	if err := json.Unmarshal(resp, &meta); err != nil {
		return nil, fmt.Errorf("workspace: unmarshal meta: %w", err)
	}

	return newRemoteWorkspace(wsID, br, meta), nil
}

// Create creates a new workspace at the given path.
func Create(ctx context.Context, path string, meta Meta) (Workspace, error) {
	if defaultTransport == nil {
		return nil, fmt.Errorf("workspace: not initialized, call workspace.Init first")
	}

	req := WSCreateRequest{Path: path, Meta: meta}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("workspace: marshal create: %w", err)
	}

	resp, err := roundTrip(ctx, KindWSCreate, data)
	if err != nil {
		return nil, fmt.Errorf("workspace: create: %w", err)
	}

	var result struct {
		ID   string `json:"id"`
		Meta Meta   `json:"meta"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("workspace: unmarshal create result: %w", err)
	}

	return newRemoteWorkspace(result.ID, "main", result.Meta), nil
}

// List returns all workspaces matching the options.
func List(ctx context.Context, opts ListOptions) ([]RegistryEntry, error) {
	if defaultTransport == nil {
		return nil, fmt.Errorf("workspace: not initialized, call workspace.Init first")
	}

	req := WSListRequest{Opts: opts}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("workspace: marshal list: %w", err)
	}

	resp, err := roundTrip(ctx, KindWSList, data)
	if err != nil {
		return nil, fmt.Errorf("workspace: list: %w", err)
	}

	var entries []RegistryEntry
	if err := json.Unmarshal(resp, &entries); err != nil {
		return nil, fmt.Errorf("workspace: unmarshal list: %w", err)
	}
	return entries, nil
}

// Delete removes a workspace.
func Delete(ctx context.Context, wsID string) error {
	if defaultTransport == nil {
		return fmt.Errorf("workspace: not initialized, call workspace.Init first")
	}

	req := WSRequest{WorkspaceID: wsID}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("workspace: marshal delete: %w", err)
	}

	_, err = roundTrip(ctx, KindWSDelete, data)
	if err != nil {
		return fmt.Errorf("workspace: delete %s: %w", wsID, err)
	}
	return nil
}

// ---------- Internal transport helpers ----------

// roundTrip sends a workspace command via Light stream and reads the response.
func roundTrip(ctx context.Context, kind string, data []byte) ([]byte, error) {
	firstFrame := &pb.Payload{
		Namespace: NamespaceWorkspace,
		Kind:      kind,
		Data:      data,
	}

	stream, err := defaultTransport.OpenLightStream(ctx, defaultNodeID, firstFrame)
	if err != nil {
		return nil, fmt.Errorf("open stream: %w", err)
	}
	defer stream.Close()

	return readResponse(stream, kind)
}

// readResponse reads a framed response from the stream.
func readResponse(r io.Reader, expectedKind string) ([]byte, error) {
	resp, err := readPayload(r)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.Kind == KindError {
		var errResp ErrorResponse
		if err := json.Unmarshal(resp.Data, &errResp); err != nil {
			return nil, fmt.Errorf("remote error (unparseable): %s", resp.Data)
		}
		return nil, &errResp
	}

	expectedResult := expectedKind + SuffixResult
	if resp.Kind != expectedResult {
		return nil, fmt.Errorf("unexpected response kind %q, want %q", resp.Kind, expectedResult)
	}

	return resp.Data, nil
}

// readPayload reads a length-prefixed Payload from r.
func readPayload(r io.Reader) (*pb.Payload, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	size := int(header[0])<<24 | int(header[1])<<16 | int(header[2])<<8 | int(header[3])
	if size > 64<<20 {
		return nil, fmt.Errorf("payload too large: %d bytes", size)
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	msg := &pb.Payload{}
	if err := proto.Unmarshal(buf, msg); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}
	return msg, nil
}
