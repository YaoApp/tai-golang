import { useState, useEffect, useCallback } from 'react';
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
 * Returns [currentNode, setCurrentNode, nodes].
 * On first load, defaults to the stored value or the first online node.
 */
export function useCurrentNode(): [string, (id: string) => void, NodeInfo[]] {
  const [nodes, setNodes] = useState<NodeInfo[]>([]);
  const [current, setCurrent] = useState(readStorage);

  useEffect(() => {
    api.nodes.list().then((list) => {
      setNodes(list);
      if (!current && list.length > 0) {
        setCurrent(list[0].id);
        writeStorage(list[0].id);
      }
    }).catch(() => {});
  }, []);

  const setCurrentNode = useCallback((id: string) => {
    setCurrent(id);
    writeStorage(id);
  }, []);

  return [current, setCurrentNode, nodes];
}
