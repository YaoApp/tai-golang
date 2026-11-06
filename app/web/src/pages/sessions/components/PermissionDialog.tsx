import type { PermissionRequestPayload } from '../../../types';
import Button from '../../../components/ui/Button';

interface Props {
  request: PermissionRequestPayload;
  onResolve: (optionId: string) => void;
  onCancel: () => void;
}

export default function PermissionDialog({ request, onResolve, onCancel }: Props) {
  return (
    <div className="overlay">
      <div className="dialog" role="dialog" aria-label="Permission request">
        <div className="dialog__header">Permission Request</div>
        <div className="dialog__body">
          <p style={{ marginBottom: 'var(--space-4)' }}>{request.description}</p>
          <div className="row row--wrap">
            {request.options.map((opt) => (
              <Button
                key={opt.id}
                variant="primary"
                onClick={() => onResolve(opt.id)}
                title={opt.description}
              >
                {opt.label}
              </Button>
            ))}
            <Button variant="ghost" onClick={onCancel}>Cancel</Button>
          </div>
        </div>
      </div>
    </div>
  );
}
