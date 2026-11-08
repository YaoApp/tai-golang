// Package demo defines the Demo App Remote Tool handlers.
package demo

import (
	"context"
	"encoding/json"

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

// Tools returns the handlers keyed by fully-qualified tool name.
func Tools() map[string]remotetool.Handler {
	return map[string]remotetool.Handler{
		"demo.wasm_echo": Echo("demo.wasm_echo"),
		"demo.echo":      Echo("demo.echo"),
	}
}
