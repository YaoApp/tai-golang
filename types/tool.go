package types

import "context"

// ToolDefinition describes a server tool registered via Server.RegisterTool().
type ToolDefinition struct {
	// Name is the tool identifier used in `tai call <name>`.
	Name string

	// Description is a human-readable summary shown in `tai call --list`.
	Description string

	// InputSchema is the JSON Schema for validating tool input.
	InputSchema []byte

	// Handler processes a tool invocation and returns the result.
	Handler ToolHandler
}

// ToolHandler is the function signature for server tool implementations.
type ToolHandler func(ctx context.Context, input []byte) (output []byte, err error)
