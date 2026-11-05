import { useState } from 'react';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { MessageItem } from '../../../types';

interface Props {
  item: MessageItem;
}

export default function MessageBubble({ item }: Props) {
  const isUser = item.role === 'user';
  const isThought = item.isThought;
  const text = item.chunks.join('');
  const [expanded, setExpanded] = useState(!isThought);

  if (!text) return null;

  if (isThought) {
    return (
      <div style={{ margin: '6px 0' }}>
        <button onClick={() => setExpanded(!expanded)} style={thoughtToggle}>
          {expanded ? '▼' : '▶'} 💭 Thinking
          {!expanded && <span style={{ fontWeight: 400, color: '#999', marginLeft: 8 }}>{text.slice(0, 60)}...</span>}
        </button>
        {expanded && (
          <div style={thoughtBox}>
            <div style={{ fontSize: 13, lineHeight: 1.5, color: '#666', whiteSpace: 'pre-wrap' }}>
              {text}
            </div>
          </div>
        )}
      </div>
    );
  }

  return (
    <div style={{ display: 'flex', justifyContent: isUser ? 'flex-end' : 'flex-start', margin: '8px 0' }}>
      <div style={isUser ? userBubble : assistantBubble}>
        {isUser && item.sendMode && item.sendMode !== 'prompt' && (
          <div style={{ fontSize: 10, opacity: 0.8, marginBottom: 2, textTransform: 'uppercase', letterSpacing: 0.5 }}>
            {item.sendMode}
          </div>
        )}
        {isUser ? (
          <div style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{text}</div>
        ) : (
          <div style={markdownWrap}>
            <Markdown remarkPlugins={[remarkGfm]} components={mdComponents}>
              {text}
            </Markdown>
          </div>
        )}
        {!item.complete && <span style={streamingDot}>●</span>}
      </div>
    </div>
  );
}

const mdComponents = {
  p: ({ children }: { children?: React.ReactNode }) => <p style={{ margin: '4px 0' }}>{children}</p>,
  code: ({ className, children }: { className?: string; children?: React.ReactNode }) => {
    const isBlock = className?.startsWith('language-');
    if (isBlock) {
      return (
        <pre style={codeBlock}>
          <code>{children}</code>
        </pre>
      );
    }
    return <code style={inlineCode}>{children}</code>;
  },
  pre: ({ children }: { children?: React.ReactNode }) => <>{children}</>,
  ul: ({ children }: { children?: React.ReactNode }) => <ul style={{ margin: '4px 0', paddingLeft: 20 }}>{children}</ul>,
  ol: ({ children }: { children?: React.ReactNode }) => <ol style={{ margin: '4px 0', paddingLeft: 20 }}>{children}</ol>,
  li: ({ children }: { children?: React.ReactNode }) => <li style={{ margin: '2px 0' }}>{children}</li>,
  table: ({ children }: { children?: React.ReactNode }) => <table style={{ borderCollapse: 'collapse', fontSize: 12, margin: '8px 0' }}>{children}</table>,
  th: ({ children }: { children?: React.ReactNode }) => <th style={{ border: '1px solid #ddd', padding: '4px 8px', background: '#f5f5f5' }}>{children}</th>,
  td: ({ children }: { children?: React.ReactNode }) => <td style={{ border: '1px solid #ddd', padding: '4px 8px' }}>{children}</td>,
  blockquote: ({ children }: { children?: React.ReactNode }) => (
    <blockquote style={{ margin: '8px 0', padding: '4px 12px', borderLeft: '3px solid #0066cc', color: '#555' }}>{children}</blockquote>
  ),
};

const userBubble: React.CSSProperties = {
  maxWidth: '80%', padding: '8px 12px', borderRadius: 8,
  background: '#0066cc', color: '#fff', fontSize: 13, lineHeight: 1.5,
  wordBreak: 'break-word',
};

const assistantBubble: React.CSSProperties = {
  maxWidth: '85%', padding: '8px 12px', borderRadius: 8,
  background: '#fff', color: '#333', border: '1px solid #e0e0e0',
  fontSize: 13, lineHeight: 1.5, wordBreak: 'break-word',
};

const markdownWrap: React.CSSProperties = {
  overflow: 'hidden',
};

const thoughtToggle: React.CSSProperties = {
  background: 'none', border: 'none', cursor: 'pointer',
  fontSize: 12, color: '#888', fontWeight: 600, padding: 0,
};

const thoughtBox: React.CSSProperties = {
  margin: '4px 0 4px 16px', padding: '8px 12px',
  background: '#f8f8f8', borderRadius: 6, border: '1px solid #eee',
};

const streamingDot: React.CSSProperties = {
  display: 'inline-block', marginLeft: 4,
  color: '#0066cc', animation: 'pulse 1s infinite',
  fontSize: 10,
};

const codeBlock: React.CSSProperties = {
  background: '#f5f5f5', borderRadius: 4, padding: '8px 12px',
  fontSize: 12, fontFamily: '"SF Mono", "Fira Code", Consolas, monospace',
  overflow: 'auto', margin: '6px 0',
  border: '1px solid #e0e0e0',
};

const inlineCode: React.CSSProperties = {
  background: '#eff1f3', padding: '1px 4px', borderRadius: 3,
  fontSize: '0.9em', fontFamily: '"SF Mono", "Fira Code", Consolas, monospace',
  color: '#0550ae',
};
