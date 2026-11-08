// Package demo defines the Demo App Remote Tool handlers.
package demo

import (
	"context"
	"encoding/json"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/remotetool"
)

// Echo returns the input plus the verified caller identity.
func Echo(tool string) remotetool.Handler {
	return func(ctx context.Context, input []byte) ([]byte, error) {
		var echo any
		_ = json.Unmarshal(input, &echo)
		identity := remotetool.User(ctx)
		out := map[string]any{"echo": echo, "tool": tool}
		if identity != nil {
			out["user_id"] = identity.UserID
			out["team_id"] = identity.TeamID
			out["scope"] = identity.Scope
		}
		return json.Marshal(out)
	}
}

// ProgressEcho emits one progress event before returning the echo result.
func ProgressEcho() remotetool.RemoteToolHandler {
	return func(ctx context.Context, input []byte, emit remotetool.Emitter) ([]byte, error) {
		emit(&pb.RemoteToolEvent{Kind: "tool_call_update", Data: []byte(`{"stage":"echo"}`)})
		return Echo("demo.echo")(ctx, input)
	}
}

// Tools returns the handlers keyed by fully-qualified tool name.
func Tools() map[string]remotetool.Handler {
	return map[string]remotetool.Handler{
		"demo.bad_fetch": Echo("demo.bad_fetch"),
		"demo.wasm_echo": Echo("demo.wasm_echo"),
		"demo.echo":      Echo("demo.echo"),
	}
}
