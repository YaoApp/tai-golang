import { useEffect, useRef, useState, useCallback } from 'react';

export interface WSMessage {
  seq?: number;
  kind: string;
  data?: unknown;
  receivedAt: number; // Date.now() when message was received/sent
  direction?: 'in' | 'out'; // 'in' = received from server, 'out' = sent by client
}

export function useWebSocket(url: string | null) {
  const wsRef = useRef<WebSocket | null>(null);
  const [messages, setMessages] = useState<WSMessage[]>([]);
  const [connected, setConnected] = useState(false);

  useEffect(() => {
    if (!url) return;

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}${url}`;
    const ws = new WebSocket(wsUrl);
    wsRef.current = ws;

    ws.onopen = () => setConnected(true);
    ws.onclose = () => setConnected(false);
    ws.onmessage = (e) => {
      try {
        const parsed = JSON.parse(e.data);
        const msg: WSMessage = { ...parsed, receivedAt: Date.now(), direction: 'in' };
        setMessages((prev) => [...prev, msg]);
      } catch {
        // ignore non-JSON
      }
    };

    return () => {
      ws.close();
      wsRef.current = null;
      setConnected(false);
    };
  }, [url]);

  const send = useCallback((msg: WSMessage) => {
    const outMsg: WSMessage = { ...msg, receivedAt: Date.now(), direction: 'out' };
    wsRef.current?.send(JSON.stringify(msg));
    setMessages((prev) => [...prev, outMsg]);
  }, []);

  const clear = useCallback(() => setMessages([]), []);

  return { messages, connected, send, clear };
}
