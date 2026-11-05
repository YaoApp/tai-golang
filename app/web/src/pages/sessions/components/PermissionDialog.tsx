import type { PermissionRequestPayload } from '../../../types';

interface Props {
  request: PermissionRequestPayload;
  onResolve: (optionId: string) => void;
  onCancel: () => void;
}

export default function PermissionDialog({ request, onResolve, onCancel }: Props) {
  return (
    <div style={overlay}>
      <div style={dialog}>
        <h3 style={titleStyle}>Permission Request</h3>
        <p style={descStyle}>{request.description}</p>
        <div style={optionsRow}>
          {request.options.map((opt) => (
            <button
              key={opt.id}
              onClick={() => onResolve(opt.id)}
              style={optionBtn}
              title={opt.description}
            >
              {opt.label}
            </button>
          ))}
          <button onClick={onCancel} style={cancelBtnStyle}>Cancel</button>
        </div>
      </div>
    </div>
  );
}

const overlay: React.CSSProperties = {
  position: 'fixed', top: 0, left: 0, right: 0, bottom: 0,
  background: 'rgba(0,0,0,0.4)', display: 'flex',
  alignItems: 'center', justifyContent: 'center', zIndex: 1000,
};

const dialog: React.CSSProperties = {
  background: '#fff', borderRadius: 8, padding: 20,
  maxWidth: 480, width: '90%', boxShadow: '0 8px 32px rgba(0,0,0,0.2)',
};

const titleStyle: React.CSSProperties = { margin: '0 0 8px', fontSize: 16 };
const descStyle: React.CSSProperties = { margin: '0 0 16px', fontSize: 13, color: '#555', lineHeight: 1.5 };

const optionsRow: React.CSSProperties = { display: 'flex', gap: 8, flexWrap: 'wrap' };

const optionBtn: React.CSSProperties = {
  padding: '8px 16px', border: 'none', borderRadius: 4,
  background: '#0066cc', color: '#fff', cursor: 'pointer',
  fontSize: 13, fontWeight: 600,
};

const cancelBtnStyle: React.CSSProperties = {
  padding: '8px 16px', border: '1px solid #ccc', borderRadius: 4,
  background: '#fff', color: '#666', cursor: 'pointer', fontSize: 13,
};
