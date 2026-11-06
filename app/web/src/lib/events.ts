/**
 * Global event bus.
 *
 * The demo app keeps a single global WebSocket (/ws/events); every global event
 * (node.* / session.* / workspace.* / realtime.*) is dispatched from it. This
 * mirrors yao 1.0's /v1/events design: one event source, many listeners.
 *
 * Pages must not open their own WebSocket; they subscribe via on(prefix, fn).
 */

/** Event message, matching app/event.go's Event struct. */
export interface EventMessage {
  type: string;
  timestamp: number;
  data?: Record<string, unknown>;
}

/** Event handler. */
export type EventHandler = (data: Record<string, unknown>, ev: EventMessage) => void;

/** Heartbeat message type; clients ignore it. */
const PING = 'ping';

/** Reconnect backoff parameters. */
const RECONNECT_BASE_MS = 500;
const RECONNECT_MAX_MS = 10000;

/**
 * Singleton event bus.
 *
 * Subscriptions are by prefix: on('node.', fn) receives node.registered and
 * node.disconnected; on('session.status', fn) matches exactly.
 */
class EventBus {
  private ws: WebSocket | null = null;
  private handlers = new Map<string, Set<EventHandler>>();
  private retry = 0;
  private reconnectTimer: number | null = null;
  private closed = false;

  /** Open the connection. Idempotent: does nothing if already connected. */
  connect(): void {
    // Allow reconnecting after disconnect(): React StrictMode mounts and
    // unmounts effects twice, and a permanently set closed flag would leave the
    // bus dead after the second mount.
    this.closed = false;
    if (this.ws) return;

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const url = `${protocol}//${window.location.host}/ws/events`;
    const ws = new WebSocket(url);
    this.ws = ws;

    ws.onopen = () => {
      this.retry = 0;
    };

    ws.onmessage = (e) => {
      let msg: EventMessage;
      try {
        msg = JSON.parse(e.data as string) as EventMessage;
      } catch {
        return; // ignore non-JSON messages
      }
      if (msg.type === PING) return;
      this.dispatch(msg);
    };

    ws.onclose = () => {
      // Ignore the close of a superseded socket (e.g. disconnect() followed
      // immediately by connect()): only the current one schedules a reconnect,
      // otherwise a late onclose would open a second connection.
      if (this.ws && this.ws !== ws) return;
      this.ws = null;
      if (!this.closed) this.scheduleReconnect();
    };

    // The browser always fires onclose after onerror, so no reconnect here.
    ws.onerror = () => {};
  }

  /** Close the connection and stop reconnecting (used on app unmount). */
  disconnect(): void {
    this.closed = true;
    if (this.reconnectTimer !== null) {
      window.clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.ws?.close();
    this.ws = null;
  }

  /**
   * Subscribe to events.
   *
   * @param prefix event type or its prefix ('node.' matches the whole group)
   * @param fn handler
   * @returns unsubscribe function
   */
  on(prefix: string, fn: EventHandler): () => void {
    let set = this.handlers.get(prefix);
    if (!set) {
      set = new Set();
      this.handlers.set(prefix, set);
    }
    set.add(fn);
    // Ensure the connection exists on first use; App.tsx already connects.
    this.connect();

    return () => {
      const s = this.handlers.get(prefix);
      if (!s) return;
      s.delete(fn);
      if (s.size === 0) this.handlers.delete(prefix);
    };
  }

  /** Dispatch an event to every handler whose prefix matches. */
  private dispatch(msg: EventMessage): void {
    const data = msg.data ?? {};
    // Snapshot the matching handlers first: a handler is allowed to subscribe
    // or unsubscribe, which would otherwise mutate the map mid-iteration.
    const matching: EventHandler[] = [];
    for (const [prefix, set] of this.handlers) {
      if (!msg.type.startsWith(prefix)) continue;
      for (const fn of set) matching.push(fn);
    }
    for (const fn of matching) {
      try {
        fn(data, msg);
      } catch (err) {
        console.error('event handler failed:', err);
      }
    }
  }

  /** Reconnect with exponential backoff. */
  private scheduleReconnect(): void {
    if (this.reconnectTimer !== null) return;
    const delay = Math.min(RECONNECT_BASE_MS * 2 ** this.retry, RECONNECT_MAX_MS);
    this.retry++;
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, delay);
  }
}

/** The global event bus instance. */
export const eventBus = new EventBus();
