import { useEffect, useState } from 'react';
import { api } from '../../../api';

interface Props {
  nodeId: string;
  wsId: string;
  branch: string;
  path: string;
}

type PreviewType = 'text' | 'image' | 'video' | 'pdf' | 'binary';

const TEXT_EXTS = new Set(['txt', 'md', 'go', 'js', 'ts', 'tsx', 'jsx', 'json', 'yaml', 'yml', 'toml', 'html', 'css', 'sh', 'py', 'rs', 'sql', 'xml', 'csv', 'log', 'env', 'gitignore', 'dockerfile', 'makefile']);
const IMAGE_EXTS = new Set(['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp', 'ico', 'bmp']);
const VIDEO_EXTS = new Set(['mp4', 'webm', 'ogg', 'mov']);

function getPreviewType(path: string): PreviewType {
  const ext = path.split('.').pop()?.toLowerCase() || '';
  const name = path.split('/').pop()?.toLowerCase() || '';
  if (TEXT_EXTS.has(ext) || TEXT_EXTS.has(name)) return 'text';
  if (IMAGE_EXTS.has(ext)) return 'image';
  if (VIDEO_EXTS.has(ext)) return 'video';
  if (ext === 'pdf') return 'pdf';
  return 'binary';
}

export default function FilePreview({ nodeId, wsId, branch, path }: Props) {
  const [content, setContent] = useState('');
  const [loading, setLoading] = useState(true);
  const previewType = getPreviewType(path);
  const previewUrl = api.workspaces.files.previewUrl(nodeId, wsId, branch, path);

  useEffect(() => {
    setLoading(true);
    setContent('');
    if (previewType === 'text') {
      api.workspaces.files
        .content(nodeId, wsId, branch, path)
        .then((r) => r.text())
        .then(setContent)
        .catch((e) => setContent(`Error: ${e.message}`))
        .finally(() => setLoading(false));
    } else {
      setLoading(false);
    }
  }, [nodeId, wsId, branch, path, previewType]);

  if (loading) return <p className="faint">Loading…</p>;

  switch (previewType) {
    case 'text':
      return (
        <pre
          className="code-block mono"
          style={{ maxHeight: 520, fontSize: 'var(--text-sm)', whiteSpace: 'pre-wrap' }}
        >
          {content}
        </pre>
      );
    case 'image':
      return <img src={previewUrl} alt={path} className="preview-media" />;
    case 'video':
      return (
        <video controls className="preview-media">
          <source src={previewUrl} />
        </video>
      );
    case 'pdf':
      return (
        <iframe
          src={previewUrl}
          title={path}
          className="preview-frame"
          style={{ height: 600 }}
        />
      );
    default:
      return (
        <div className="empty">
          <div className="empty__title">Binary file</div>
          <div>This file type cannot be previewed.</div>
          <a href={previewUrl} download>Download</a>
        </div>
      );
  }
}
