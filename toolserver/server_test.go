package toolserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/remotetool"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

func testServer(t *testing.T, allow bool) (*Server, *httptest.Server) {
	t.Helper()
	reg := remotetool.New(nil)
	tool := &remotetool.Tool{Entry: &pb.RemoteToolEntry{Name: "echo", Group: "demo"}, Handler: func(_ context.Context, in []byte) ([]byte, error) {
		return in, nil
	}}
	if err := reg.Set([]*remotetool.Tool{tool}); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Registry: reg,
		NodeAuth: func(*http.Request) bool { return true },
		UserAuth: func(string) (*types.UserIdentity, error) {
			if !allow {
				return nil, io.ErrUnexpectedEOF
			}
			return &types.UserIdentity{Scope: types.Scope{types.RemoteToolCall}}, nil
		},
		Artifacts: func(digest string) ([]byte, error) {
			if digest != strings.Repeat("a", 64) {
				return nil, io.ErrUnexpectedEOF
			}
			return []byte("wasm"), nil
		},
	}
	ts := httptest.NewUnstartedServer(s.Handler())
	ts.EnableHTTP2 = true
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return s, ts
}

func client(ts *httptest.Server) *http.Client { return ts.Client() }

func callBody(t *testing.T, payload *pb.Payload) []byte {
	t.Helper()
	var buf bytes.Buffer
	codec, _ := transport.NewFrameCodec(1 << 20)
	if err := codec.WriteFrame(&buf, payload); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestServerEndpoints(t *testing.T) {
	_, ts := testServer(t, true)
	cl := client(ts)

	resp, err := cl.Get(ts.URL + "/v1/tai/tool/health")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("health: %v %v", resp, err)
	}
	_ = resp.Body.Close()

	call := &pb.RemoteToolCall{Tool: "demo.echo", Input: []byte("hi"), RequestId: "r1"}
	data, _ := proto.Marshal(call)
	body := callBody(t, &pb.Payload{Namespace: "remote_tool", Kind: "call", Data: data, Identity: &pb.Identity{UserToken: "u"}})
	resp, err = cl.Post(ts.URL+"/v1/tai/tool/call", "application/x-protobuf", bytes.NewReader(body))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("call: %v %v", resp, err)
	}
	defer resp.Body.Close()
	codec, _ := transport.NewFrameCodec(1 << 20)
	frame, err := codec.ReadFrame(resp.Body)
	if err != nil || frame.Kind != "result" {
		t.Fatalf("frame: %+v %v", frame, err)
	}
	var result pb.RemoteToolResult
	if err := proto.Unmarshal(frame.Data, &result); err != nil {
		t.Fatal(err)
	}
	if string(result.Output) != "hi" || result.RequestId != "r1" {
		t.Fatalf("result output=%q request_id=%q", string(result.Output), result.RequestId)
	}

	resp, err = cl.Get(ts.URL + "/v1/tai/tool/plugins/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("artifact: %v %v", resp, err)
	}
	_ = resp.Body.Close()
	resp, err = cl.Get(ts.URL + "/v1/tai/tool/plugins/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("missing artifact: %d", resp.StatusCode)
	}
}

func TestServerErrors(t *testing.T) {
	_, ts := testServer(t, false)
	cl := client(ts)
	call := &pb.RemoteToolCall{Tool: "demo.echo", Input: []byte("hi")}
	data, _ := proto.Marshal(call)
	body := callBody(t, &pb.Payload{Namespace: "remote_tool", Kind: "call", Data: data})
	resp, err := cl.Post(ts.URL+"/v1/tai/tool/call", "application/x-protobuf", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("auth: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
	// invalid first frame
	resp, err = cl.Post(ts.URL+"/v1/tai/tool/call", "application/x-protobuf", strings.NewReader("bad"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad frame: %d", resp.StatusCode)
	}
}

func TestServerMoreBranches(t *testing.T) {
	reg := remotetool.New(nil)
	_ = reg.Set([]*remotetool.Tool{{Entry: &pb.RemoteToolEntry{Name: "echo", Group: "demo"}, Handler: func(context.Context, []byte) ([]byte, error) { return nil, io.ErrUnexpectedEOF }}})
	s := &Server{
		Registry: reg,
		NodeAuth: func(*http.Request) bool { return false },
		UserAuth: func(string) (*types.UserIdentity, error) { return nil, io.ErrUnexpectedEOF },
	}
	ts := httptest.NewUnstartedServer(s.Handler())
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()
	cl := ts.Client()
	if resp, _ := cl.Get(ts.URL + "/v1/tai/tool/health"); resp.StatusCode != 401 {
		t.Fatalf("health auth: %d", resp.StatusCode)
	}
	if resp, _ := cl.Get(ts.URL + "/v1/tai/tool/plugins/x"); resp.StatusCode != 401 {
		t.Fatalf("plugin auth: %d", resp.StatusCode)
	}
	// allow node, user auth failure
	s.NodeAuth = func(*http.Request) bool { return true }
	call := &pb.RemoteToolCall{Tool: "demo.echo"}
	data, _ := proto.Marshal(call)
	body := callBody(t, &pb.Payload{Namespace: "remote_tool", Kind: "call", Data: data})
	resp, err := cl.Post(ts.URL+"/v1/tai/tool/call", "application/x-protobuf", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("user auth: %d", resp.StatusCode)
	}
	// scope failure
	s.UserAuth = func(string) (*types.UserIdentity, error) { return &types.UserIdentity{}, nil }
	resp, err = cl.Post(ts.URL+"/v1/tai/tool/call", "application/x-protobuf", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("scope: %d", resp.StatusCode)
	}
	// wrong namespace
	s.UserAuth = func(string) (*types.UserIdentity, error) {
		return &types.UserIdentity{Scope: types.Scope{types.RemoteToolCall}}, nil
	}
	wrong := callBody(t, &pb.Payload{Namespace: "x", Kind: "call"})
	if resp, _ := cl.Post(ts.URL+"/v1/tai/tool/call", "application/x-protobuf", bytes.NewReader(wrong)); resp.StatusCode != 400 {
		t.Fatalf("namespace: %d", resp.StatusCode)
	}
	// artifacts nil
	s.Artifacts = nil
	if resp, _ := cl.Get(ts.URL + "/v1/tai/tool/plugins/x"); resp.StatusCode != 404 {
		t.Fatalf("nil artifacts: %d", resp.StatusCode)
	}
}

func TestServerHandlerErrorAndBadProto(t *testing.T) {
	reg := remotetool.New(nil)
	_ = reg.Set([]*remotetool.Tool{{Entry: &pb.RemoteToolEntry{Name: "echo", Group: "demo"}, Handler: func(context.Context, []byte) ([]byte, error) { return nil, io.ErrUnexpectedEOF }}})
	s := &Server{Registry: reg, UserAuth: func(string) (*types.UserIdentity, error) {
		return &types.UserIdentity{Scope: types.Scope{types.RemoteToolCall}}, nil
	}}
	ts := httptest.NewUnstartedServer(s.Handler())
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()
	cl := ts.Client()
	// invalid proto data with valid frame
	frame := &pb.Payload{Namespace: "remote_tool", Kind: "call", Data: []byte{0xff}}
	body := callBody(t, frame)
	resp, err := cl.Post(ts.URL+"/v1/tai/tool/call", "application/x-protobuf", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("proto: %d", resp.StatusCode)
	}
	// handler error -> 200 result.error
	call := &pb.RemoteToolCall{Tool: "demo.echo"}
	data, _ := proto.Marshal(call)
	body = callBody(t, &pb.Payload{Namespace: "remote_tool", Kind: "call", Data: data})
	resp, err = cl.Post(ts.URL+"/v1/tai/tool/call", "application/x-protobuf", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("handler status: %d", resp.StatusCode)
	}
	codec, _ := transport.NewFrameCodec(1 << 20)
	fr, err := codec.ReadFrame(resp.Body)
	if err != nil || fr.Kind != "result" {
		t.Fatalf("frame: %+v %v", fr, err)
	}
	var out pb.RemoteToolResult
	if err := proto.Unmarshal(fr.Data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Error == nil {
		t.Fatalf("expected result error")
	}
}

func TestCallRegistryUnavailable(t *testing.T) {
	s := &Server{UserAuth: func(string) (*types.UserIdentity, error) {
		return &types.UserIdentity{Scope: types.Scope{types.RemoteToolCall}}, nil
	}}
	ts := httptest.NewUnstartedServer(s.Handler())
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()
	resp, err := ts.Client().Post(ts.URL+"/v1/tai/tool/call", "application/x-protobuf", bytes.NewReader(callBody(t, &pb.Payload{Namespace: "remote_tool", Kind: "call", Data: mustPB(&pb.RemoteToolCall{Tool: "x"})})))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func mustPB(m proto.Message) []byte { b, _ := proto.Marshal(m); return b }

func TestCallErrorMapping(t *testing.T) {
	reg := remotetool.New(nil)
	if err := reg.Set([]*remotetool.Tool{{Entry: &pb.RemoteToolEntry{Name: "bad"}, Handler: func(context.Context, []byte) ([]byte, error) { return nil, remotetool.ErrInvalidInput }}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{Registry: reg, UserAuth: func(string) (*types.UserIdentity, error) {
		return &types.UserIdentity{Scope: types.Scope{types.RemoteToolCall}}, nil
	}}
	ts := httptest.NewUnstartedServer(s.Handler())
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()
	post := func(p *pb.Payload) int {
		resp, err := ts.Client().Post(ts.URL+"/v1/tai/tool/call", "application/x-protobuf", bytes.NewReader(callBody(t, p)))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(&pb.Payload{}); code != 400 {
		t.Fatalf("invalid frame=%d", code)
	}
	badData := &pb.Payload{Namespace: "remote_tool", Kind: "call", Data: []byte{0xff}}
	if code := post(badData); code != 400 {
		t.Fatalf("bad proto=%d", code)
	}
	if code := post(&pb.Payload{Namespace: "remote_tool", Kind: "call", Data: mustPB(&pb.RemoteToolCall{Tool: "missing"})}); code != 404 {
		t.Fatalf("missing=%d", code)
	}
	if code := post(&pb.Payload{Namespace: "remote_tool", Kind: "call", Data: mustPB(&pb.RemoteToolCall{Tool: "bad"})}); code != 200 {
		t.Fatalf("handler_error=%d", code)
	}
}

func TestCallAuthBranches(t *testing.T) {
	reg := remotetool.New(nil)
	body := func() []byte {
		return callBody(t, &pb.Payload{Namespace: "remote_tool", Kind: "call", Data: mustPB(&pb.RemoteToolCall{Tool: "x"})})
	}
	for _, s := range []*Server{
		{Registry: reg},
		{Registry: reg, UserAuth: func(string) (*types.UserIdentity, error) { return nil, errors.New("bad") }},
	} {
		ts := httptest.NewUnstartedServer(s.Handler())
		ts.EnableHTTP2 = true
		ts.StartTLS()
		resp, err := ts.Client().Post(ts.URL+"/v1/tai/tool/call", "application/x-protobuf", bytes.NewReader(body()))
		if err != nil || resp.StatusCode != 401 {
			t.Fatalf("status=%v err=%v", resp, err)
		}
		_ = resp.Body.Close()
		ts.Close()
	}
}

func TestProgressStream(t *testing.T) {
	reg := remotetool.New(nil)
	_ = reg.Set([]*remotetool.Tool{{Entry: &pb.RemoteToolEntry{Name: "p"}, ProgressHandler: func(_ context.Context, input []byte, emit remotetool.Emitter) ([]byte, error) {
		emit(&pb.RemoteToolEvent{Kind: "tool_call_update", Data: []byte("{}")})
		return input, nil
	}}})
	s := &Server{Registry: reg, UserAuth: func(string) (*types.UserIdentity, error) {
		return &types.UserIdentity{Scope: types.Scope{types.RemoteToolCall}}, nil
	}}
	ts := httptest.NewUnstartedServer(s.Handler())
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()
	frame := &pb.Payload{Namespace: "remote_tool", Kind: "call", Data: mustPB(&pb.RemoteToolCall{Tool: "p"}), Identity: &pb.Identity{UserToken: "u"}}
	resp, err := ts.Client().Post(ts.URL+"/v1/tai/tool/call", "application/x-protobuf", bytes.NewReader(callBody(t, frame)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	codec, _ := transport.NewFrameCodec(16 << 20)
	f1, err := codec.ReadFrame(resp.Body)
	if err != nil || f1.Kind != "progress" {
		t.Fatalf("progress: %+v %v", f1, err)
	}
	f2, err := codec.ReadFrame(resp.Body)
	if err != nil || f2.Kind != "result" {
		t.Fatalf("result: %+v %v", f2, err)
	}
}

func TestArtifactInvalidDigestAndRequestID(t *testing.T) {
	reg := remotetool.New(nil)
	_ = reg.Set([]*remotetool.Tool{{Entry: &pb.RemoteToolEntry{Name: "x"}, Handler: func(context.Context, []byte) ([]byte, error) { return nil, nil }}})
	s := &Server{Registry: reg, NodeAuth: func(*http.Request) bool { return true }, Artifacts: func(string) ([]byte, error) { return nil, errors.New("missing") }, UserAuth: func(string) (*types.UserIdentity, error) {
		return &types.UserIdentity{Scope: types.Scope{types.RemoteToolCall}}, nil
	}}
	ts := httptest.NewUnstartedServer(s.Handler())
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()
	cl := ts.Client()
	if resp, _ := cl.Get(ts.URL + "/v1/tai/tool/plugins/short"); resp.StatusCode != 400 {
		t.Fatalf("short digest=%d", resp.StatusCode)
	}
	resp, err := cl.Get(ts.URL + "/v1/tai/tool/plugins/" + strings.Repeat("g", 64))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("bad hex=%d", resp.StatusCode)
	}
	frame := &pb.Payload{Namespace: "remote_tool", Kind: "call", Data: mustPB(&pb.RemoteToolCall{Tool: "x"})}
	resp, err = cl.Post(ts.URL+"/v1/tai/tool/call", "application/x-protobuf", bytes.NewReader(callBody(t, frame)))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("call: %v %v", resp, err)
	}
	_ = resp.Body.Close()
}

func TestProgressThenError(t *testing.T) {
	reg := remotetool.New(nil)
	if err := reg.Set([]*remotetool.Tool{{Entry: &pb.RemoteToolEntry{Name: "p"}, ProgressHandler: func(_ context.Context, _ []byte, emit remotetool.Emitter) ([]byte, error) {
		emit(&pb.RemoteToolEvent{Kind: "tool_call_update"})
		return nil, errors.New("boom")
	}}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{Registry: reg, UserAuth: func(string) (*types.UserIdentity, error) {
		return &types.UserIdentity{Scope: types.Scope{types.RemoteToolCall}}, nil
	}}
	ts := httptest.NewUnstartedServer(s.Handler())
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()
	frame := &pb.Payload{Namespace: "remote_tool", Kind: "call", Data: mustPB(&pb.RemoteToolCall{Tool: "p"})}
	resp, err := ts.Client().Post(ts.URL+"/v1/tai/tool/call", "application/x-protobuf", bytes.NewReader(callBody(t, frame)))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("call: %v %v", resp, err)
	}
	codec, _ := transport.NewFrameCodec(16 << 20)
	if f, err := codec.ReadFrame(resp.Body); err != nil || f.Kind != "progress" {
		t.Fatalf("progress %v", err)
	}
	if f, err := codec.ReadFrame(resp.Body); err != nil || f.Kind != "result" {
		t.Fatalf("result %v", err)
	}
}
