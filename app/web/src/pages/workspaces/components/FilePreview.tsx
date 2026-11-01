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

  if (loading) return <p style={{ color: '#666' }}>Loading...</p>;

  switch (previewType) {
    case 'text':
      return (
        <pre style={{
          background: '#f6f8fa', padding: 12, borderRadius: 6, overflow: 'auto',
          maxHeight: 500, fontSize: 13, lineHeight: 1.5, whiteSpace: 'pre-wrap',
          wordBreak: 'break-word', border: '1px solid #e1e4e8',
        }}>
          {content}
        </pre>
      );
    case 'image':
      return <img src={previewUrl} alt={path} style={{ maxWidth: '100%', borderRadius: 4 }} />;
    case 'video':
      return (
        <video controls style={{ maxWidth: '100%', borderRadius: 4 }}>
          <source src={previewUrl} />
        </video>
      );
    case 'pdf':
      return <iframe src={previewUrl} style={{ width: '100%', height: 600, border: '1px solid #ddd', borderRadius: 4 }} />;
    default:
      return (
        <div style={{ textAlign: 'center', padding: 20, color: '#666' }}>
          <p>Binary file — cannot preview</p>
          <a href={previewUrl} download style={{ color: '#0066cc' }}>Download</a>
        </div>
      );
  }
}
