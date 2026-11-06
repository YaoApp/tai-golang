import { useState, useEffect, useCallback, useRef } from 'react';
import { api } from '../api';
import type { NodeInfo } from '../api';

const STORAGE_KEY = 'tai-demo-current-node';

function readStorage(): string {
  try {
    return localStorage.getItem(STORAGE_KEY) || '';
  } catch {
    return '';
  }
}

function writeStorage(nodeId: string) {
  try {
    localStorage.setItem(STORAGE_KEY, nodeId);
  } catch {
    // ignore
  }
}

/**
 * Global current-node hook backed by localStorage.
 *
 * Returns [currentNode, setCurrentNode, nodes, refreshNodes].
 * On first load, defaults to the stored value or the first online node.
 *
 * refreshNodes is used by global event subscribers: a node.* event refetches
 * the list. Without it the page would discard the fetch result and the UI
 * would never update.
 */
export function useCurrentNode(): [
  string,
  (id: string) => void,
  NodeInfo[],
  () => Promise<void>,
] {
  const [nodes, setNodes] = useState<NodeInfo[]>([]);
  const [current, setCurrent] = useState(readStorage);

  // currentRef mirrors current so load can read the latest value inside an
  // async callback without becoming a dependency.
  const currentRef = useRef(current);

  const load = useCallback(async () => {
    try {
      const list = await api.nodes.list();
      setNodes(list);
      // Pick a default node on first load.
      if (!currentRef.current && list.length > 0) {
        currentRef.current = list[0].id;
        writeStorage(list[0].id);
        setCurrent(list[0].id);
      }
    } catch {
      // Keep the previous list on failure so the UI does not flash empty.
    }
  }, []);

  useEffect(() => {
    currentRef.current = current;
  }, [current]);

  useEffect(() => {
    // Fetch the node list on mount. load is async, but the linter treats
    // setNodes as a synchronous setState; fetching external data is the
    // canonical exception.
    // oxlint-disable-next-line react/set-state-in-effect
    void load();
  }, [load]);

  const setCurrentNode = useCallback((id: string) => {
    currentRef.current = id;
    setCurrent(id);
    writeStorage(id);
  }, []);

  return [current, setCurrentNode, nodes, load];
}
