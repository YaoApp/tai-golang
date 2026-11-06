import type { ReactNode } from 'react';

interface Props {
  label: string;
  children: ReactNode;
  className?: string;
}

/**
 * Label plus control stack with a consistent vertical rhythm.
 *
 * @param className applied to the wrapper for row-style inline usage
 * @returns a column containing the uppercase label and the control
 */
export default function Field({ label, children, className }: Props) {
  const classes = ['field'];
  if (className) classes.push(className);

  return (
    <label className={classes.join(' ')}>
      <span className="field__label">{label}</span>
      {children}
    </label>
  );
}
