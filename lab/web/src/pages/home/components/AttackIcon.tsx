import type { AttackId } from '../../../api/ids';

type AttackIconProps = { readonly attack: AttackId; readonly className?: string };

// The paths of each attack's icon, drawn on a 32-unit grid with a stroke.
const ICON_PATHS: Readonly<Record<string, readonly string[]>> = {
  'workers-down': ['M4 6h24v8H4z', 'M4 18h24v8H4z', 'M20 4 12 28'],
  network: ['M4 12c7-6 17-6 24 0', 'M8 17c5-4 11-4 16 0', 'M12 22c2.5-2 5.5-2 8 0', 'M5 27 27 5'],
  'lying-partner': [
    'M10 12a6 6 0 1 0 12 0a6 6 0 1 0-12 0',
    'M6 28c1.5-6 5.5-9 10-9s8.5 3 10 9',
    'M24 6l4-2',
    'M25 12h4',
  ],
  'database-failover': [
    'M6 8c0-2.2 4.5-4 10-4s10 1.8 10 4-4.5 4-10 4-10-1.8-10-4z',
    'M6 8v16c0 2.2 4.5 4 10 4s10-1.8 10-4V8',
    'M6 16c0 2.2 4.5 4 10 4s10-1.8 10-4',
  ],
  clock: ['M5 16a11 11 0 1 0 22 0a11 11 0 1 0-22 0', 'M16 9v7l5 3'],
  everything: ['M16 3 4 9v7c0 7 5 11 12 13 7-2 12-6 12-13V9z', 'M11 16l4 4 6-8'],
  calm: ['M4 20c4-6 8-6 12 0s8 6 12 0', 'M4 12c4-6 8-6 12 0s8 6 12 0'],
};

const FALLBACK_PATHS = ['M8 8h16v16H8z'];

// AttackIcon is decorative: the card's text names the attack.
export default function AttackIcon({ attack, className }: AttackIconProps): React.JSX.Element {
  const paths = ICON_PATHS[attack] ?? FALLBACK_PATHS;

  return (
    <svg
      className={className}
      viewBox="0 0 32 32"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {paths.map((path) => (
        <path key={path} d={path} />
      ))}
    </svg>
  );
}
