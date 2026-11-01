// Package workspace provides remote workspace access via the Tai transport layer.
// All file operations use standard Go fs.FS interfaces; the transport channel
// (Light pool / Heavy pool) is chosen automatically and transparently.
package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// NewRemoteWorkspace creates a remote workspace proxy bound to a specific
// node. This is the public constructor used by node/.
func NewRemoteWorkspace(nodeID, wsID, branch string, meta types.Meta, policy *ChannelPolicy) types.Workspace {
	if policy == nil {
		policy = DefaultPolicy()
	}
	rw := &remoteWorkspace{
		nodeID: nodeID,
		wsID:   wsID,
		branch: branch,
		meta:   meta,
		policy: policy,
	}
	rw.fsImpl = &remoteBranchFS{nodeID: nodeID, wsID: wsID, branch: branch, policy: policy}
	return rw
}

// HalfCloser is optionally implemented by streams to support closing the
// write side while keeping the read side open.
type HalfCloser interface {
	CloseWrite() error
}

// ---------- Internal transport helpers ----------

// roundTrip sends a workspace command via Light stream and reads the response.
func roundTrip(ctx context.Context, nodeID, kind string, data []byte) ([]byte, error) {
	firstFrame := &pb.Payload{
		Namespace: NamespaceWorkspace,
		Kind:      kind,
		Data:      data,
	}

	stream, err := transport.PkgOpenLightStream(ctx, nodeID, firstFrame)
	if err != nil {
		return nil, fmt.Errorf("open stream: %w", err)
	}
	defer stream.Close()

	return ReadResponse(stream, kind)
}

// ReadResponse reads a framed response from the stream.
func ReadResponse(r io.Reader, expectedKind string) ([]byte, error) {
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
