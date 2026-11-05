// TypeScript interfaces aligned with tai-golang/types/*.go

// --- Session Request ---

export interface McpServer {
  type: 'stdio' | 'http';
  name: string;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
  headers?: Record<string, string>;
}

export interface Provider {
  provider: string;
  model: string;
  base_url?: string;
  api_key?: string;
}

export interface ProvidersMatrix {
  light?: Provider;
  default?: Provider;
  realtime?: Provider;
  [role: string]: Provider | undefined;
}

export interface SessionRequest {
  cwd?: string;
  additionalDirectories?: string[];
  mcpServers?: McpServer[];
  assistantId?: string;
  workspaceId?: string;
  runtime: string;
  isolation: string;
  idleTimeout?: number;
  maxDuration?: number;
  llm?: ProvidersMatrix;
  env?: Record<string, string>;
  config?: Record<string, unknown>;
  metadata?: Record<string, string>;
}

// --- User Message ---

export interface ContentBlock {
  type: string;
  text?: string;
  data?: string;
  mimeType?: string;
  uri?: string;
  name?: string;
}

export interface UserMessage {
  id: string;
  content?: ContentBlock[];
  message?: string;
  attachments?: string[];
}

// --- Data Events (RecvEvent) ---

export type AgentState = 'running' | 'idle' | 'requires_action';

export interface StateUpdatePayload {
  state: AgentState;
  stopReason?: string;
  error?: { code: number; message: string };
}

export interface NoticePayload {
  severity: 'info' | 'warning' | 'error';
  title: string;
}

export interface UsagePayload {
  used: number;
  size: number;
  cost?: { amount: number; currency: string };
}

export interface SessionInfoPayload {
  title?: string;
}

export interface MessageChunkPayload {
  messageId: string;
  content: ContentBlock;
}

export interface MessagePayload {
  messageId: string;
  content: ContentBlock[];
}

export interface ToolCallUpdatePayload {
  toolCallId: string;
  name?: string;
  kind?: string;
  status: 'pending' | 'in_progress' | 'completed' | 'failed' | 'cancelled';
  locations?: { path: string }[];
}

export interface ToolCallContentChunkPayload {
  toolCallId: string;
  content: ContentBlock;
}

export interface PlanEntry {
  content: string;
  priority?: string;
  status?: string;
}

export interface PlanUpdatePayload {
  plan: {
    type: string;
    planId: string;
    entries: PlanEntry[];
  };
}

export interface PlanRemovedPayload {
  planId: string;
}

export interface TerminalUpdatePayload {
  terminalId: string;
  command?: string;
  cwd?: string;
  title?: string;
  exitStatus?: { exitCode: number };
}

export interface TerminalOutputChunkPayload {
  terminalId: string;
  data: string;
}

export interface CompactionUpdatePayload {
  compactionId: string;
  status: 'in_progress' | 'completed' | 'failed' | 'cancelled';
}

export interface CompactionSummaryChunkPayload {
  compactionId: string;
  content: ContentBlock;
}

export interface SubagentUpdatePayload {
  sessionId: string;
  title?: string;
  status?: string;
  capabilities?: Record<string, unknown>;
}

export interface SessionMessagePayload {
  messageId: string;
  senderSessionId: string;
  recipientSessionId: string;
  content: ContentBlock[];
}

export interface ConfigOptionPayload {
  configId: string;
  name?: string;
  type?: string;
  currentValue?: unknown;
  options?: { id: string; label: string }[];
}

// --- Ctrl Events (RecvAction) ---

export interface PermissionOption {
  id: string;
  label: string;
  description?: string;
}

export interface PermissionRequestPayload {
  requestId: string;
  description: string;
  options: PermissionOption[];
}

export interface ElicitationField {
  name: string;
  type: string;
  label?: string;
  description?: string;
  required?: boolean;
  default?: unknown;
  options?: { value: string; label: string }[];
}

export interface ElicitationRequestPayload {
  id: string;
  title?: string;
  description?: string;
  schema: {
    fields: ElicitationField[];
  };
}

// --- Display Items ---

export interface MessageItem {
  messageId: string;
  role: 'user' | 'assistant';
  chunks: string[];
  complete: boolean;
  isThought?: boolean;
  sendMode?: 'prompt' | 'followup' | 'steer';
}

export interface ToolCallItem {
  toolCallId: string;
  name: string;
  kind: string;
  status: string;
  output: string[];
  locations?: { path: string }[];
}

export interface PlanItem {
  planId: string;
  entries: PlanEntry[];
  removed?: boolean;
}

export interface TerminalItem {
  terminalId: string;
  command?: string;
  cwd?: string;
  title?: string;
  exitCode?: number;
  output: string[];
}

export interface CompactionItem {
  compactionId: string;
  status: string;
  summary: string[];
}

export interface SubagentItem {
  sessionId: string;
  title?: string;
  status?: string;
}

export type DisplayItem =
  | { type: 'message'; data: MessageItem }
  | { type: 'tool_call'; data: ToolCallItem }
  | { type: 'plan'; data: PlanItem }
  | { type: 'terminal'; data: TerminalItem }
  | { type: 'compaction'; data: CompactionItem }
  | { type: 'subagent'; data: SubagentItem }
  | { type: 'state'; state: AgentState; stopReason?: string; error?: { code: number; message: string } }
  | { type: 'notice'; severity: string; title: string }
  | { type: 'usage'; used: number; size: number; cost?: { amount: number; currency: string } }
  | { type: 'session_info'; title: string }
  | { type: 'config_option'; data: ConfigOptionPayload }
  | { type: 'session_message'; data: SessionMessagePayload };

// --- Queue Action ---

export interface QueueAction {
  action: 'remove' | 'edit' | 'clear' | 'promote';
  messageId?: string;
  content?: ContentBlock[];
}
