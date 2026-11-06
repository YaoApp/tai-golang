import { useState } from 'react';
import type { ElicitationRequestPayload } from '../../../types';
import Button from '../../../components/ui/Button';

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
    <div className="overlay">
      <div className="dialog" role="dialog" aria-label={request.title || 'Input requested'}>
        <div className="dialog__header">{request.title || 'Input required'}</div>
        <div className="dialog__body">
          {request.description && (
            <p style={{ marginBottom: 'var(--space-4)' }}>{request.description}</p>
          )}
          <div className="stack" style={{ gap: 'var(--space-3)' }}>
            {request.schema.fields.map((field) => (
              <div key={field.name}>
                <label className="field__label" style={{ display: 'block', marginBottom: 4 }}>
                  {field.label || field.name}
                  {field.required && <span style={{ color: 'var(--danger)' }}> *</span>}
                </label>
                {field.description && (
                  <p className="faint" style={{ marginBottom: 4, fontSize: 'var(--text-xs)' }}>
                    {field.description}
                  </p>
                )}
                {renderField(field, values[field.name], (v) => setValues({ ...values, [field.name]: v }))}
              </div>
            ))}
          </div>
        </div>
        <div className="dialog__footer">
          <Button variant="primary" onClick={handleSubmit}>Accept</Button>
          <Button onClick={onDecline}>Decline</Button>
          <Button variant="ghost" onClick={onCancel}>Cancel</Button>
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
        <select className="select" value={String(value ?? '')} onChange={(e) => onChange(e.target.value)}>
          <option value="">Select…</option>
          {field.options?.map((o) => (
            <option key={o.value} value={o.value}>{o.label}</option>
          ))}
        </select>
      );
    case 'number':
      return (
        <input
          className="input"
          type="number"
          value={String(value ?? '')}
          onChange={(e) => onChange(Number(e.target.value))}
        />
      );
    default:
      return (
        <input
          className="input"
          type="text"
          value={String(value ?? '')}
          onChange={(e) => onChange(e.target.value)}
        />
      );
  }
}
