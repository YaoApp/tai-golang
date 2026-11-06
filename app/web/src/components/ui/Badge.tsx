import type { ReactNode } from 'react';

type Tone = 'default' | 'accent' | 'success' | 'warning' | 'danger' | 'info';

interface Props {
  children: ReactNode;
  tone?: Tone;
  title?: string;
}

/**
 * Compact mono label for counts, kinds, and states.
 *
 * @param tone semantic color role
 */
export default function Badge({ children, tone = 'default', title }: Props) {
  return (
    <span className={`badge${tone !== 'default' ? ` badge--${tone}` : ''}`} title={title}>
      {children}
    </span>
  );
}
