import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { oneDark } from 'react-syntax-highlighter/dist/esm/styles/prism';
import mermaid from 'mermaid';
import MermaidDiagram from './MermaidDiagram';

interface Props {
  content: string;
  /** Handles in-app navigation for links to other docs, by file name. */
  onNavigate?: (docName: string) => void;
}

/**
 * Resolves a markdown link target to another document in this doc set.
 *
 * The markdown files use plain relative links (`quickstart.md`) so they render
 * correctly on GitHub. Inside the console those would resolve against the
 * current page URL and 404, so they are routed through the docs view instead.
 *
 * @param href the link target as written in markdown
 * @returns the target doc file name, or null when the link is not an internal doc
 */
function toInternalDoc(href: string): string | null {
  if (!href || href.startsWith('#') || /^[a-z][a-z0-9+.-]*:/i.test(href)) return null;
  const path = href.split('#')[0];
  // Only sibling markdown files are part of this doc set; anything with a
  // directory component is an external resource.
  if (!path.endsWith('.md') || path.includes('/')) return null;
  return path;
}

/**
 * Builds the slug react-markdown's heading ids use, so in-page anchors resolve.
 *
 * @param text the heading text
 * @returns a lowercase, hyphenated slug
 */
function headingSlug(text: string): string {
  return text
    .trim()
    .toLowerCase()
    .replace(/[^\w\u4e00-\u9fff\s-]/g, '')
    .replace(/\s+/g, '-');
}

/**
 * Concatenates the text content of a React node tree.
 *
 * Heading children can contain inline code or emphasis, so the slug has to be
 * built from the rendered text rather than a single string.
 *
 * @param node a React child node
 * @returns its plain text
 */
function flattenText(node: React.ReactNode): string {
  if (node == null || typeof node === 'boolean') return '';
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(flattenText).join('');
  if (typeof node === 'object' && 'props' in node) {
    return flattenText((node as { props: { children?: React.ReactNode } }).props.children);
  }
  return '';
}

// Initialize once for the whole page. startOnLoad stays false so diagrams are
// rendered explicitly by MermaidDiagram rather than scanned for at startup.
// The dark theme matches the console; the light one renders unreadable text on
// this background.
mermaid.initialize({
  startOnLoad: false,
  securityLevel: 'strict',
  theme: 'dark',
  fontFamily: 'inherit',
  darkMode: true,
  themeVariables: {
    background: '#12161f',
    primaryColor: '#171c26',
    primaryTextColor: '#e6edf3',
    primaryBorderColor: '#2b3240',
    lineColor: '#3d4655',
    secondaryColor: '#12283f',
    tertiaryColor: '#0d1117',
  },
});

const codeBlockStyle: React.CSSProperties = {
  margin: 'var(--space-3) 0',
  borderRadius: 'var(--radius-md)',
  border: '1px solid var(--border-subtle)',
  background: 'var(--bg-inset)',
  fontSize: 12,
};

const lineNumberStyle: React.CSSProperties = {
  color: 'var(--text-tertiary)',
  fontSize: 11,
  minWidth: '2.5em',
};

const langMap: Record<string, string> = {
  golang: 'go',
  ts: 'typescript',
  js: 'javascript',
  sh: 'bash',
  shell: 'bash',
  yml: 'yaml',
};

function normalizeLanguage(raw: string): string {
  return langMap[raw] || raw;
}

export default function MarkdownRenderer({ content, onNavigate }: Props) {
  return (
    <div className="prose">
      <Markdown
        remarkPlugins={[remarkGfm]}
        components={{
          a: ({ href, children }) => {
            const doc = href ? toInternalDoc(href) : null;
            if (doc && onNavigate) {
              return (
                <a
                  href={`/docs/${doc.replace(/\.md$/, '')}`}
                  onClick={(e) => {
                    e.preventDefault();
                    onNavigate(doc);
                  }}
                >
                  {children}
                </a>
              );
            }
            // External links open in a new tab so the console stays put.
            const external = href && /^[a-z][a-z0-9+.-]*:/i.test(href);
            return (
              <a
                href={href}
                {...(external ? { target: '_blank', rel: 'noreferrer noopener' } : {})}
              >
                {children}
              </a>
            );
          },
          // Heading ids make the documents' in-page anchors (#open, #history)
          // resolve; react-markdown does not emit them by default.
          h2: ({ children }) => <h2 id={headingSlug(flattenText(children))}>{children}</h2>,
          h3: ({ children }) => <h3 id={headingSlug(flattenText(children))}>{children}</h3>,
          h4: ({ children }) => <h4 id={headingSlug(flattenText(children))}>{children}</h4>,
          code: ({ className, children }) => {
            const match = className?.match(/language-(\w+)/);
            const code = String(children).replace(/\n$/, '');
            const isMultiline = code.includes('\n');

            if (match) {
              const lang = normalizeLanguage(match[1]);
              // Mermaid blocks are diagrams, not source to highlight.
              if (lang === 'mermaid') {
                return <MermaidDiagram chart={code} />;
              }
              return (
                <SyntaxHighlighter
                  style={oneDark}
                  language={lang}
                  customStyle={codeBlockStyle}
                  showLineNumbers={code.split('\n').length > 3}
                  lineNumberStyle={lineNumberStyle}
                >
                  {code}
                </SyntaxHighlighter>
              );
            }

            if (isMultiline) {
              return (
                <SyntaxHighlighter
                  style={oneDark}
                  language="text"
                  customStyle={codeBlockStyle}
                  showLineNumbers={false}
                >
                  {code}
                </SyntaxHighlighter>
              );
            }

            return <code>{children}</code>;
          },
          pre: ({ children }) => <>{children}</>,
        }}
      >
        {content}
      </Markdown>
    </div>
  );
}
