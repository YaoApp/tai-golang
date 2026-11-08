package transport

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
)

func newRemoteToolControlStream(t *testing.T, buf *bytes.Buffer) *controlStream {
	t.Helper()
	reg := &trackingRegistry{testNodeRegistry: testNodeRegistry{pool: &pb.PoolConfig{}}}
	return newControlStream("n1", strings.NewReader(""), &noopResponseWriter{buf}, reg, &testAuth{}, DefaultCodec())
}

func TestRemoteToolCall_EchoesVerifiedIdentity(t *testing.T) {
	var buf bytes.Buffer
	cs := newRemoteToolControlStream(t, &buf)

	reqData, _ := proto.Marshal(&pb.ServerToolRequest{
		Tool:      "demo.echo",
		Input:     []byte(`{"msg":"hi"}`),
		RequestId: "r1",
	})
	cs.dispatch(&pb.Payload{
		Namespace: types.NamespaceControl,
		Kind:      types.KindRemoteToolCall,
		Data:      reqData,
		Identity:  &pb.Identity{UserToken: "user-tok"},
	})

	frame, err := cs.codec.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("read response frame: %v", err)
	}
	if frame.Kind != types.KindRemoteToolResult {
		t.Fatalf("kind = %q, want %q", frame.Kind, types.KindRemoteToolResult)
	}
	resp := &pb.ServerToolResponse{}
	if err := proto.Unmarshal(frame.Data, resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.RequestId != "r1" {
		t.Errorf("request_id = %q", resp.RequestId)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	var out map[string]any
	if err := json.Unmarshal(resp.Output, &out); err != nil {
		t.Fatalf("output json: %v", err)
	}
	if out["user_id"] != "u1" || out["team_id"] != "t1" || out["tool"] != "demo.echo" {
		t.Errorf("output = %v", out)
	}
}

func TestRemoteToolCall_MissingCredential(t *testing.T) {
	var buf bytes.Buffer
	cs := newRemoteToolControlStream(t, &buf)

	reqData, _ := proto.Marshal(&pb.ServerToolRequest{Tool: "t", RequestId: "r2"})
	cs.dispatch(&pb.Payload{
		Namespace: types.NamespaceControl,
		Kind:      types.KindRemoteToolCall,
		Data:      reqData,
	})

	frame, err := cs.codec.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp := &pb.ServerToolResponse{}
	_ = proto.Unmarshal(frame.Data, resp)
	if resp.Error == nil || resp.Error.Code != "unauthorized" {
		t.Errorf("error = %+v, want unauthorized", resp.Error)
	}
}

func TestRemoteToolCall_InvalidJSONInput(t *testing.T) {
	var buf bytes.Buffer
	cs := newRemoteToolControlStream(t, &buf)

	reqData, _ := proto.Marshal(&pb.ServerToolRequest{Tool: "t", Input: []byte("not-json"), RequestId: "r3"})
	cs.dispatch(&pb.Payload{
		Namespace: types.NamespaceControl,
		Kind:      types.KindRemoteToolCall,
		Data:      reqData,
		Identity:  &pb.Identity{UserToken: "user-tok"},
	})

	frame, err := cs.codec.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp := &pb.ServerToolResponse{}
	_ = proto.Unmarshal(frame.Data, resp)
	if resp.Error == nil || resp.Error.Code != "invalid_input" {
		t.Errorf("error = %+v, want invalid_input", resp.Error)
	}
}

func TestRemoteToolCall_BadFrameIgnored(t *testing.T) {
	var buf bytes.Buffer
	cs := newRemoteToolControlStream(t, &buf)
	cs.handleRemoteToolCall(&pb.Payload{
		Namespace: types.NamespaceControl,
		Kind:      types.KindRemoteToolCall,
		Data:      []byte("not-proto"),
	})
	if buf.Len() != 0 {
		t.Errorf("malformed request must not produce a response frame")
	}
}

// noScopeAuth returns an identity without the remote_tool:call scope.
type noScopeAuth struct{ *testAuth }

func (a noScopeAuth) ValidateUserToken(string) (*types.UserIdentity, error) {
	return &types.UserIdentity{UserID: "u1", TeamID: "t1"}, nil
}

func TestRemoteToolCall_RequiresScope(t *testing.T) {
	var buf bytes.Buffer
	reg := &trackingRegistry{testNodeRegistry: testNodeRegistry{pool: &pb.PoolConfig{}}}
	cs := newControlStream("n1", strings.NewReader(""), &noopResponseWriter{&buf}, reg, noScopeAuth{&testAuth{}}, DefaultCodec())

	reqData, _ := proto.Marshal(&pb.ServerToolRequest{Tool: "t", RequestId: "r-scope"})
	cs.dispatch(&pb.Payload{
		Namespace: types.NamespaceControl,
		Kind:      types.KindRemoteToolCall,
		Data:      reqData,
		Identity:  &pb.Identity{UserToken: "tok"},
	})

	frame, err := cs.codec.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp := &pb.ServerToolResponse{}
	_ = proto.Unmarshal(frame.Data, resp)
	if resp.Error == nil || resp.Error.Code != "unauthorized" {
		t.Fatalf("error = %+v, want unauthorized", resp.Error)
	}
	if !strings.Contains(resp.Error.Message, "remote_tool:call") {
		t.Errorf("message = %q, want scope hint", resp.Error.Message)
	}
}
