export type BadgeTone = 'success' | 'warning' | 'danger' | 'info' | 'muted';

type BadgeProps = { readonly tone: BadgeTone; readonly children: React.ReactNode };

// Badge is a short state label; the text always says the state, the color
// and the dot only repeat it.
export function Badge({ tone, children }: BadgeProps): React.JSX.Element {
  return (
    <span className={`badge badge-${tone}`}>
      <span className="dot" aria-hidden="true" />
      {children}
    </span>
  );
}
