interface Props {
  /** Direction the caret points; `up` and `down` indicate expansion state. */
  direction?: 'up' | 'down' | 'right';
  /** Rendered square size in pixels. */
  size?: number;
  className?: string;
}

const rotation: Record<NonNullable<Props['direction']>, number> = {
  down: 0,
  up: 180,
  right: -90,
};

/**
 * Disclosure chevron.
 *
 * Text glyphs like ▾ render at a size the font decides and vary between
 * platforms, which leaves them too small to hit comfortably in a dense
 * console. This draws an SVG at an explicit size instead.
 *
 * @param direction rotation of the base chevron
 * @param size square size in pixels
 * @returns an inline, non-selectable icon that inherits the text color
 */
export default function Caret({ direction = 'down', size = 12, className }: Props) {
  return (
    <svg
      className={className}
      width={size}
      height={size}
      viewBox="0 0 12 12"
      fill="none"
      aria-hidden="true"
      style={{
        flexShrink: 0,
        transform: `rotate(${rotation[direction]}deg)`,
        transition: 'transform 0.12s ease',
      }}
    >
      <path
        d="M2.5 4.5 6 8l3.5-3.5"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}
