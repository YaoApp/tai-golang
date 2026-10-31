import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { oneLight } from 'react-syntax-highlighter/dist/esm/styles/prism';

interface Props {
  content: string;
}

const styles: Record<string, React.CSSProperties> = {
  wrapper: {
    maxWidth: 860,
    lineHeight: 1.7,
    fontSize: 15,
    color: '#1a1a2e',
  },
  h1: { fontSize: 28, fontWeight: 700, margin: '0 0 20px', borderBottom: '2px solid #e5e7eb', paddingBottom: 10 },
  h2: { fontSize: 22, fontWeight: 600, margin: '32px 0 12px', borderBottom: '1px solid #e5e7eb', paddingBottom: 8 },
  h3: { fontSize: 18, fontWeight: 600, margin: '24px 0 8px' },
  h4: { fontSize: 16, fontWeight: 600, margin: '20px 0 6px' },
  p: { margin: '8px 0' },
  a: { color: '#2563eb', textDecoration: 'none' },
  table: {
    borderCollapse: 'collapse' as const,
    width: '100%',
    margin: '16px 0',
    fontSize: 14,
  },
  th: {
    textAlign: 'left' as const,
    padding: '10px 14px',
    borderBottom: '2px solid #d1d5db',
    background: '#f8fafc',
    fontWeight: 600,
    whiteSpace: 'nowrap' as const,
  },
  td: {
    padding: '8px 14px',
    borderBottom: '1px solid #e5e7eb',
    verticalAlign: 'top' as const,
  },
  codeInline: {
    background: '#eff1f3',
    padding: '2px 6px',
    borderRadius: 4,
    fontSize: '0.9em',
    fontWeight: 400,
    fontFamily: '"SF Mono", "Fira Code", Consolas, "Liberation Mono", Menlo, monospace',
    color: '#0550ae',
  },
  ul: { margin: '8px 0', paddingLeft: 24 },
  ol: { margin: '8px 0', paddingLeft: 24 },
  li: { margin: '4px 0' },
  hr: { border: 'none', borderTop: '1px solid #e5e7eb', margin: '24px 0' },
  blockquote: {
    margin: '12px 0',
    padding: '8px 16px',
    borderLeft: '4px solid #3b82f6',
    background: '#eff6ff',
    color: '#1e40af',
  },
};

const codeBlockStyle: React.CSSProperties = {
  margin: '12px 0',
  borderRadius: 8,
  border: '1px solid #d0d7de',
  fontSize: 13,
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

export default function MarkdownRenderer({ content }: Props) {
  return (
    <div style={styles.wrapper}>
      <Markdown
        remarkPlugins={[remarkGfm]}
        components={{
          h1: ({ children }) => <h1 style={styles.h1}>{children}</h1>,
          h2: ({ children }) => <h2 style={styles.h2}>{children}</h2>,
          h3: ({ children }) => <h3 style={styles.h3}>{children}</h3>,
          h4: ({ children }) => <h4 style={styles.h4}>{children}</h4>,
          p: ({ children }) => <p style={styles.p}>{children}</p>,
          a: ({ children, href }) => <a style={styles.a} href={href}>{children}</a>,
          table: ({ children }) => <table style={styles.table}>{children}</table>,
          th: ({ children }) => <th style={styles.th}>{children}</th>,
          td: ({ children }) => <td style={styles.td}>{children}</td>,
          ul: ({ children }) => <ul style={styles.ul}>{children}</ul>,
          ol: ({ children }) => <ol style={styles.ol}>{children}</ol>,
          li: ({ children }) => <li style={styles.li}>{children}</li>,
          hr: () => <hr style={styles.hr} />,
          blockquote: ({ children }) => <blockquote style={styles.blockquote}>{children}</blockquote>,
          code: ({ className, children, ...rest }) => {
            const match = className?.match(/language-(\w+)/);
            const code = String(children).replace(/\n$/, '');
            const isMultiline = code.includes('\n');

            if (match) {
              const lang = normalizeLanguage(match[1]);
              return (
                <SyntaxHighlighter
                  style={oneLight}
                  language={lang}
                  customStyle={codeBlockStyle}
                  showLineNumbers={code.split('\n').length > 3}
                  lineNumberStyle={{ color: '#a0aec0', fontSize: 12, minWidth: '2.5em' }}
                >
                  {code}
                </SyntaxHighlighter>
              );
            }

            if (isMultiline) {
              return (
                <SyntaxHighlighter
                  style={oneLight}
                  language="text"
                  customStyle={codeBlockStyle}
                  showLineNumbers={false}
                >
                  {code}
                </SyntaxHighlighter>
              );
            }

            return <code style={styles.codeInline}>{children}</code>;
          },
          pre: ({ children }) => <>{children}</>,
        }}
      >
        {content}
      </Markdown>
    </div>
  );
}
