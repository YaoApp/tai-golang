import type { ButtonHTMLAttributes, ReactNode } from 'react';

type Variant = 'default' | 'primary' | 'ghost' | 'danger';
type Size = 'sm' | 'md' | 'lg' | 'icon';

interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  children?: ReactNode;
}

/**
 * Themed button.
 *
 * @param variant visual weight; `primary` is the single affirmative action on a
 *   surface, `ghost` for low-emphasis controls, `danger` for destructive ones
 * @param size control height and horizontal padding
 * @returns a native button carrying the shared `.btn` classes
 */
export default function Button({
  variant = 'default',
  size = 'md',
  className,
  children,
  ...rest
}: Props) {
  const classes = ['btn'];
  if (variant !== 'default') classes.push(`btn--${variant}`);
  if (size !== 'md') classes.push(`btn--${size}`);
  if (className) classes.push(className);

  return (
    <button className={classes.join(' ')} {...rest}>
      {children}
    </button>
  );
}
