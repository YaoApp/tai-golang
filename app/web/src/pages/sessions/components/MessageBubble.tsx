import { useState } from 'react';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { MessageItem } from '../../../types';
import Caret from '../../../components/ui/Caret';

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
      <div className="thought">
        <button type="button" className="thought__toggle" onClick={() => setExpanded(!expanded)}>
          <span className="thought__caret">
            <Caret direction={expanded ? 'down' : 'right'} size={10} />
          </span>
          Thinking
          {!expanded && <span className="thought__preview truncate">{text.slice(0, 60)}…</span>}
        </button>
        {expanded && <div className="thought__body">{text}</div>}
      </div>
    );
  }

  return (
    <div className={`msg${isUser ? ' msg--user' : ' msg--assistant'}`}>
      <div className="msg__bubble">
        {isUser && item.sendMode && item.sendMode !== 'prompt' && (
          <div className="msg__mode mono">{item.sendMode}</div>
        )}
        {isUser ? (
          <div className="msg__text">{text}</div>
        ) : (
          <div className="markdown">
            <Markdown remarkPlugins={[remarkGfm]}>{text}</Markdown>
          </div>
        )}
        {!item.complete && <span className="msg__streaming" />}
      </div>
    </div>
  );
}
