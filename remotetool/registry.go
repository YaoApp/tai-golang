// Package remotetool implements the SDK-side Remote Tool registry and Handler.
package remotetool

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// Handler executes a Remote Tool.
type Handler func(ctx context.Context, input []byte) ([]byte, error)

// Errors map to the design error codes.
var (
	ErrInvalidInput = errors.New("invalid_input")
	ErrNotFound     = errors.New("not_found")
	ErrHandler      = errors.New("handler_error")
	ErrUnauthorized = errors.New("unauthorized")
)

// Tool pairs an entry with its Handler.
type Tool struct {
	Entry   *pb.RemoteToolEntry
	Handler Handler
	// ProgressHandler, when set, is used instead of Handler and may emit progress.
	ProgressHandler RemoteToolHandler
}

// Registry is a concurrency-safe Remote Tool registry.
type Registry struct {
	mu      sync.RWMutex
	tools   map[string]*Tool
	changed func()
	plugins []*pb.PluginEntry
}

// New creates a registry; changed is called only when the set changes.
func New(changed func()) *Registry {
	return &Registry{tools: map[string]*Tool{}, changed: changed}
}

// Set replaces the registry atomically.
func (r *Registry) Set(tools []*Tool) error {
	next := make(map[string]*Tool, len(tools))
	for _, t := range tools {
		if err := validate(t); err != nil {
			return err
		}
		if _, ok := next[t.Entry.Name]; ok {
			return fmt.Errorf("remotetool: duplicate %s", t.Entry.Name)
		}
		next[t.Entry.Name] = t
	}
	r.mu.Lock()
	r.tools = next
	r.mu.Unlock()
	r.notify()
	return nil
}

// Register adds one tool.
func (r *Registry) Register(t *Tool) error {
	if err := validate(t); err != nil {
		return err
	}
	r.mu.Lock()
	if _, ok := r.tools[t.Entry.Name]; ok {
		r.mu.Unlock()
		return fmt.Errorf("remotetool: duplicate %s", t.Entry.Name)
	}
	r.tools[t.Entry.Name] = t
	r.mu.Unlock()
	r.notify()
	return nil
}

// Unregister removes a tool.
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	if _, ok := r.tools[name]; !ok {
		r.mu.Unlock()
		return ErrNotFound
	}
	delete(r.tools, name)
	r.mu.Unlock()
	r.notify()
	return nil
}

// Find resolves `<name>` or `<group>.<name>`.
func (r *Registry) Find(name string) (*Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if t, ok := r.tools[name]; ok {
		return t, nil
	}
	for _, t := range r.tools {
		if t.Entry.Group+"."+t.Entry.Name == name {
			return t, nil
		}
	}
	return nil, ErrNotFound
}

// Call validates the user token/scope and invokes the Handler.
func (r *Registry) Call(ctx context.Context, identity *types.UserIdentity, call *pb.RemoteToolCall) (*pb.RemoteToolResult, error) {
	return r.CallWithEmitter(ctx, identity, call, nil)
}

// CallWithEmitter invokes the tool, forwarding progress through emit.
func (r *Registry) CallWithEmitter(ctx context.Context, identity *types.UserIdentity, call *pb.RemoteToolCall, emit Emitter) (*pb.RemoteToolResult, error) {
	if identity == nil || identity.Scope == nil || !identity.Scope.Has(types.RemoteToolCall) {
		return nil, fmt.Errorf("%w: remote_tool:call scope required", ErrUnauthorized)
	}
	tool, err := r.Find(call.Tool)
	if err != nil {
		return nil, err
	}
	ctx = withCallContext(ctx, call.RequestId, identity, call.SessionId)
	var out []byte
	var callErr error
	if tool.ProgressHandler != nil {
		out, callErr = tool.ProgressHandler(ctx, call.Input, emit)
	} else {
		out, callErr = tool.Handler(ctx, call.Input)
	}
	res := &pb.RemoteToolResult{RequestId: call.RequestId, Output: out}
	if callErr != nil {
		code := "handler_error"
		if errors.Is(callErr, ErrInvalidInput) {
			code = "invalid_input"
		}
		res.Error = &pb.RemoteToolError{Code: code, Message: callErr.Error()}
	}
	return res, nil
}

// Sync returns tools/groups for RemoteToolSync.
func (r *Registry) Sync() *pb.RemoteToolSync {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	sync := &pb.RemoteToolSync{}
	groups := map[string]*pb.RemoteToolGroup{}
	for _, n := range names {
		t := r.tools[n]
		sync.Tools = append(sync.Tools, t.Entry)
		g, ok := groups[t.Entry.Group]
		if !ok {
			g = &pb.RemoteToolGroup{Name: t.Entry.Group}
			groups[t.Entry.Group] = g
			sync.Groups = append(sync.Groups, g)
		}
		g.Tools = append(g.Tools, t.Entry.Name)
	}
	return sync
}

func validate(t *Tool) error {
	if t == nil || t.Entry == nil || t.Entry.Name == "" || (t.Handler == nil && t.ProgressHandler == nil) {
		return ErrInvalidInput
	}
	return nil
}

func (r *Registry) notify() {
	if r.changed != nil {
		r.changed()
	}
}

// SetPlugins sets the plugin metadata included in sync frames.
func (r *Registry) SetPlugins(entries []*pb.PluginEntry) {
	r.mu.Lock()
	r.plugins = append([]*pb.PluginEntry(nil), entries...)
	r.mu.Unlock()
}

// Payload returns the Control `remote_tool.sync` frame for this registry.
func (r *Registry) Payload() *pb.Payload {
	sync := r.Sync()
	r.mu.RLock()
	sync.Plugins = append([]*pb.PluginEntry(nil), r.plugins...)
	r.mu.RUnlock()
	data, _ := proto.Marshal(sync)
	return &pb.Payload{Namespace: "control", Kind: "remote_tool.sync", Data: data}
}

// Push sends the current sync payload through sender.
func (r *Registry) Push(sender func(*pb.Payload) error) error {
	if sender == nil {
		return nil
	}
	return sender(r.Payload())
}
