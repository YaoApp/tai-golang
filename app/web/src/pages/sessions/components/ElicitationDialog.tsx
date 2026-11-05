import { useState } from 'react';
import type { ElicitationRequestPayload } from '../../../types';

interface Props {
  request: ElicitationRequestPayload;
  onAccept: (content: Record<string, unknown>) => void;
  onDecline: () => void;
  onCancel: () => void;
}

export default function ElicitationDialog({ request, onAccept, onDecline, onCancel }: Props) {
  const [values, setValues] = useState<Record<string, unknown>>(() => {
    const init: Record<string, unknown> = {};
    request.schema.fields.forEach((f) => {
      if (f.default !== undefined) init[f.name] = f.default;
    });
    return init;
  });

  const handleSubmit = () => onAccept(values);

  return (
    <div style={overlay}>
      <div style={dialog}>
        {request.title && <h3 style={titleStyle}>{request.title}</h3>}
        {request.description && <p style={descStyle}>{request.description}</p>}
        <div style={formBody}>
          {request.schema.fields.map((field) => (
            <div key={field.name} style={fieldRow}>
              <label style={labelStyle}>
                {field.label || field.name}
                {field.required && <span style={{ color: '#dc3545' }}> *</span>}
              </label>
              {field.description && <p style={fieldDesc}>{field.description}</p>}
              {renderField(field, values[field.name], (v) => setValues({ ...values, [field.name]: v }))}
            </div>
          ))}
        </div>
        <div style={actionsRow}>
          <button onClick={handleSubmit} style={acceptBtn}>Accept</button>
          <button onClick={onDecline} style={declineBtn}>Decline</button>
          <button onClick={onCancel} style={cancelBtnStyle}>Cancel</button>
        </div>
      </div>
    </div>
  );
}

function renderField(
  field: { type: string; options?: { value: string; label: string }[] },
  value: unknown,
  onChange: (v: unknown) => void,
) {
  switch (field.type) {
    case 'boolean':
      return (
        <input type="checkbox" checked={!!value} onChange={(e) => onChange(e.target.checked)} />
      );
    case 'select':
      return (
        <select value={String(value ?? '')} onChange={(e) => onChange(e.target.value)} style={inputStyle}>
          <option value="">Select...</option>
          {field.options?.map((o) => (
            <option key={o.value} value={o.value}>{o.label}</option>
          ))}
        </select>
      );
    case 'number':
      return (
        <input type="number" value={String(value ?? '')} onChange={(e) => onChange(Number(e.target.value))} style={inputStyle} />
      );
    default:
      return (
        <input type="text" value={String(value ?? '')} onChange={(e) => onChange(e.target.value)} style={inputStyle} />
      );
  }
}

const overlay: React.CSSProperties = {
  position: 'fixed', top: 0, left: 0, right: 0, bottom: 0,
  background: 'rgba(0,0,0,0.4)', display: 'flex',
  alignItems: 'center', justifyContent: 'center', zIndex: 1000,
};

const dialog: React.CSSProperties = {
  background: '#fff', borderRadius: 8, padding: 20,
  maxWidth: 520, width: '90%', boxShadow: '0 8px 32px rgba(0,0,0,0.2)',
  maxHeight: '80vh', overflow: 'auto',
};

const titleStyle: React.CSSProperties = { margin: '0 0 4px', fontSize: 16 };
const descStyle: React.CSSProperties = { margin: '0 0 12px', fontSize: 13, color: '#555' };
const formBody: React.CSSProperties = { marginBottom: 16 };

const fieldRow: React.CSSProperties = { marginBottom: 12 };
const labelStyle: React.CSSProperties = { display: 'block', fontWeight: 600, fontSize: 13, marginBottom: 4 };
const fieldDesc: React.CSSProperties = { margin: '0 0 4px', fontSize: 11, color: '#888' };
const inputStyle: React.CSSProperties = { width: '100%', padding: '6px 10px', borderRadius: 4, border: '1px solid #ccc', fontSize: 13 };

const actionsRow: React.CSSProperties = { display: 'flex', gap: 8 };

const acceptBtn: React.CSSProperties = {
  padding: '8px 16px', border: 'none', borderRadius: 4,
  background: '#28a745', color: '#fff', cursor: 'pointer', fontWeight: 600, fontSize: 13,
};

const declineBtn: React.CSSProperties = {
  padding: '8px 16px', border: '1px solid #fd7e14', borderRadius: 4,
  background: '#fff', color: '#fd7e14', cursor: 'pointer', fontSize: 13,
};

const cancelBtnStyle: React.CSSProperties = {
  padding: '8px 16px', border: '1px solid #ccc', borderRadius: 4,
  background: '#fff', color: '#666', cursor: 'pointer', fontSize: 13,
};
