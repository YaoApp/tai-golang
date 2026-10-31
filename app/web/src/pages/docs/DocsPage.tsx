import { useEffect, useState } from 'react';
import { api } from '../../api';
import type { DocEntry } from '../../api';
import DocNav from './components/DocNav';
import MarkdownRenderer from './components/MarkdownRenderer';

export default function DocsPage() {
  const [docs, setDocs] = useState<DocEntry[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [content, setContent] = useState('');

  useEffect(() => {
    api.docs.list().then((list) => {
      setDocs(list);
      if (list.length > 0 && !selected) {
        setSelected(list[0].name);
      }
    });
  }, []);

  useEffect(() => {
    if (selected) {
      api.docs.get(selected).then(setContent);
    }
  }, [selected]);

  return (
    <div style={{ display: 'flex', gap: 20 }}>
      <DocNav docs={docs} selected={selected} onSelect={setSelected} />
      <div style={{ flex: 1 }}>
        {content ? (
          <MarkdownRenderer content={content} />
        ) : (
          <p style={{ color: '#999' }}>Select a document from the sidebar.</p>
        )}
      </div>
    </div>
  );
}
