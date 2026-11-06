export type StatusTone = 'accent' | 'success' | 'warning' | 'danger' | 'neutral';

const toneColor: Record<StatusTone, string> = {
  accent: 'var(--accent)',
  success: 'var(--success)',
  warning: 'var(--warning)',
  danger: 'var(--danger)',
  neutral: 'var(--text-tertiary)',
};

interface Props {
  /** Raw session status; tone is derived from it. */
  status: string;
  /** Overrides the displayed text; defaults to the raw status. */
  label?: string;
}

/**
 * Maps a session status string to its display tone.
 *
 * @param status the backend session status
 * @returns the tone conveying whether the session is healthy, degraded, or done
 */
function toneFor(status: string): StatusTone {
  switch (status) {
    case 'running':
      return 'success';
    case 'starting':
    case 'resuming':
      return 'accent';
    case 'disconnected':
    case 'recovery_gap':
      return 'warning';
    case 'resume_error':
    case 'terminated':
      return 'danger';
    default:
      return 'neutral';
  }
}

/**
 * Colored dot with the status text, used for connection and session state.
 *
 * @returns an inline element colored via `currentColor`, pulsing while running
 */
export default function StatusDot({ status, label }: Props) {
  const tone = toneFor(status);
  const live = status === 'running' || status === 'starting' || status === 'resuming';
  const classes = ['status-dot', `status-dot--${tone}`];
  if (live) classes.push('status-dot--live');

  return (
    <span className={classes.join(' ')} style={{ color: toneColor[tone] }}>
      {label ?? status}
    </span>
  );
}
