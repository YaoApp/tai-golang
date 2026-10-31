package types

import "encoding/json"

// Event is the unified event produced by parse.go.
// JSON serialization is compatible with ACP session/update params.update.
type Event struct {
	Type UpdateType      `json:"sessionUpdate"`
	Data json.RawMessage `json:"-"` // typed data, deserialized by Type
}

// UpdateType identifies the kind of ACP session/update.
type UpdateType string

const (
	// Messages
	AgentMessageChunk   UpdateType = "agent_message_chunk"
	AgentMessage        UpdateType = "agent_message"
	AgentThoughtChunk   UpdateType = "agent_thought_chunk"
	AgentThought        UpdateType = "agent_thought"
	UserMessageChunk    UpdateType = "user_message_chunk"
	UserMessageComplete UpdateType = "user_message"

	// Tools
	ToolCallUpdate       UpdateType = "tool_call_update"
	ToolCallContentChunk UpdateType = "tool_call_content_chunk"

	// State
	StateUpdate UpdateType = "state_update"

	// Notice
	Notice UpdateType = "notice"

	// Usage
	UsageUpdate UpdateType = "usage_update"

	// Meta
	SessionInfoUpdate UpdateType = "session_info_update"

	// Plan
	PlanUpdate  UpdateType = "plan_update"
	PlanRemoved UpdateType = "plan_removed"

	// Compaction
	CompactionUpdate       UpdateType = "compaction_update"
	CompactionSummaryChunk UpdateType = "compaction_summary_chunk"

	// Terminal
	TerminalUpdate      UpdateType = "terminal_update"
	TerminalOutputChunk UpdateType = "terminal_output_chunk"

	// Config
	ConfigOptionUpdate UpdateType = "config_option_update"

	// Commands
	AvailableCommandsUpdate UpdateType = "available_commands_update"

	// SubAgent
	SubagentUpdate      UpdateType = "subagent_update"
	SessionMessage      UpdateType = "session_message"
	SessionMessageChunk UpdateType = "session_message_chunk"
)
