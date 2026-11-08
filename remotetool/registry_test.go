package remotetool

import (
	"context"
	"errors"
	"testing"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
)

func tool() *Tool {
	return &Tool{Entry: &pb.RemoteToolEntry{Name: "echo", Group: "demo"}, Handler: func(_ context.Context, in []byte) ([]byte, error) {
		if string(in) == "bad" {
			return nil, ErrInvalidInput
		}
		return in, nil
	}}
}

func TestRegistry(t *testing.T) {
	changed := 0
	r := New(func() { changed++ })
	if err := r.Set([]*Tool{tool()}); err != nil {
		t.Fatal(err)
	}
	if changed != 1 {
		t.Fatalf("changed=%d", changed)
	}
	if _, err := r.Find("demo.echo"); err != nil {
		t.Fatal(err)
	}
	identity := &types.UserIdentity{Scope: types.Scope{types.RemoteToolCall}}
	res, err := r.Call(context.Background(), identity, &pb.RemoteToolCall{Tool: "echo", Input: []byte("x"), RequestId: "r1"})
	if err != nil || string(res.Output) != "x" || res.RequestId != "r1" {
		t.Fatalf("call: %+v %v", res, err)
	}
	if _, err := r.Call(context.Background(), nil, &pb.RemoteToolCall{Tool: "echo"}); err == nil {
		t.Fatal("expected scope error")
	}
	res, err = r.Call(context.Background(), identity, &pb.RemoteToolCall{Tool: "echo", Input: []byte("bad")})
	if err != nil || res.Error == nil || res.Error.Code != "invalid_input" {
		t.Fatalf("bad input: %+v %v", res, err)
	}
	if err := r.Register(tool()); err == nil {
		t.Fatal("duplicate")
	}
	if err := r.Unregister("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing")
	}
	if err := r.Set([]*Tool{{}}); err == nil {
		t.Fatal("invalid")
	}
	sync := r.Sync()
	if len(sync.Tools) != 1 || len(sync.Groups) != 1 {
		t.Fatalf("sync: %+v", sync)
	}
}

func TestRegistryMore(t *testing.T) {
	r := New(nil)
	if err := r.Register(tool()); err != nil {
		t.Fatal(err)
	}
	if err := r.Unregister("echo"); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(nil); err == nil {
		t.Fatal("nil")
	}
	if _, err := r.Find("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal("not found")
	}
	if err := r.Set([]*Tool{tool()}); err != nil {
		t.Fatal(err)
	}
	identity := &types.UserIdentity{Scope: types.Scope{types.RemoteToolCall}}
	if _, err := r.Call(context.Background(), identity, &pb.RemoteToolCall{Tool: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing call = %v", err)
	}
	// handler_error path
	h := &Tool{Entry: &pb.RemoteToolEntry{Name: "bad", Group: "g"}, Handler: func(context.Context, []byte) ([]byte, error) {
		return nil, errors.New("boom")
	}}
	if err := r.Register(h); err != nil {
		t.Fatal(err)
	}
	res, err := r.Call(context.Background(), identity, &pb.RemoteToolCall{Tool: "g.bad"})
	if err != nil || res.Error == nil || res.Error.Code != "handler_error" {
		t.Fatalf("handler error: %+v %v", res, err)
	}
}

func TestSyncPayload(t *testing.T) {
	r := New(nil)
	_ = r.Set([]*Tool{tool()})
	p := r.Payload()
	if p.Namespace != "control" || p.Kind != "remote_tool.sync" || len(p.Data) == 0 {
		t.Fatalf("payload: %+v", p)
	}
}

func TestPush(t *testing.T) {
	r := New(nil)
	var got *pb.Payload
	if err := r.Push(func(p *pb.Payload) error { got = p; return nil }); err != nil || got == nil {
		t.Fatal("push")
	}
	if err := r.Push(nil); err != nil {
		t.Fatal("nil push")
	}
}

func TestSetPlugins(t *testing.T) {
	r := New(nil)
	r.SetPlugins([]*pb.PluginEntry{{Name: "p", Version: "1.0.0"}})
	p := r.Payload()
	if len(p.Data) == 0 {
		t.Fatal("empty")
	}
}

func TestProgressHandlerAndContext(t *testing.T) {
	r := New(nil)
	tool := &Tool{Entry: &pb.RemoteToolEntry{Name: "p"}, ProgressHandler: func(ctx context.Context, input []byte, emit Emitter) ([]byte, error) {
		if ToolCallID(ctx) != "r1" || SessionID(ctx) != "s1" || User(ctx) == nil {
			t.Fatal("context missing")
		}
		emit(&pb.RemoteToolEvent{Kind: "tool_call_update", Data: []byte(`{"x":1}`)})
		return input, nil
	}}
	if err := r.Register(tool); err != nil {
		t.Fatal(err)
	}
	var events int
	identity := &types.UserIdentity{Scope: types.Scope{types.RemoteToolCall}}
	res, err := r.CallWithEmitter(context.Background(), identity, &pb.RemoteToolCall{Tool: "p", RequestId: "r1", SessionId: "s1"}, func(*pb.RemoteToolEvent) { events++ })
	if err != nil || events != 1 || res.Error != nil {
		t.Fatalf("res=%+v events=%d err=%v", res, events, err)
	}
}
