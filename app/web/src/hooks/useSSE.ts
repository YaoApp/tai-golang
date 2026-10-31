import { useEffect, useState } from 'react';

export function useSSE<T>(url: string) {
  const [events, setEvents] = useState<T[]>([]);

  useEffect(() => {
    const source = new EventSource(url);
    source.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data) as T;
        setEvents((prev) => [...prev, data]);
      } catch {
        // ignore
      }
    };
    return () => source.close();
  }, [url]);

  return events;
}
