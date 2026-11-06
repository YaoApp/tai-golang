import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { api } from '../../api';
import type { DocEntry } from '../../api';
import DocNav from './components/DocNav';
import MarkdownRenderer from './components/MarkdownRenderer';

/** Document shown when the URL carries no name; it is the docs index. */
const DEFAULT_DOC = 'index.md';

/**
 * Normalizes a doc identifier from the URL into a file name. The URL omits the
 * `.md` suffix so routes read as `/docs/quickstart`.
 *
 * @param raw the `:name` route parameter, possibly empty
 * @returns a `.md` file name
 */
function toDocName(raw: string | undefined): string {
  const name = (raw || '').trim();
  if (!name) return DEFAULT_DOC;
  return name.endsWith('.md') ? name : `${name}.md`;
}

export default function DocsPage() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const [docs, setDocs] = useState<DocEntry[]>([]);
  const [content, setContent] = useState('');
  const [error, setError] = useState('');

  // The URL is the single source of truth for the open document: reloading and
  // sharing a link must land on the same page.
  const selected = toDocName(name);

  useEffect(() => {
    api.docs.list().then(setDocs).catch(() => setDocs([]));
  }, []);

  useEffect(() => {
    let cancelled = false;
    setError('');
    api.docs
      .get(selected)
      .then((text) => {
        if (!cancelled) setContent(text);
      })
      .catch(() => {
        if (!cancelled) {
          setContent('');
          setError(`Document "${selected}" not found.`);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [selected]);

  const handleSelect = (docName: string) => {
    const base = docName.replace(/\.md$/, '');
    navigate(base === 'index' ? '/docs' : `/docs/${base}`);
  };

  return (
    <div className="page--fixed">
      <div className="docs-layout">
        <DocNav docs={docs} selected={selected} onSelect={handleSelect} />
        <div className="docs-content">
          {error ? (
            <div className="notice notice--error">{error}</div>
          ) : content ? (
            <MarkdownRenderer content={content} onNavigate={handleSelect} />
          ) : (
            <p className="faint">Select a document from the sidebar.</p>
          )}
        </div>
      </div>
    </div>
  );
}
