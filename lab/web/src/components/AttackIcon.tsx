import { Activity, Clock3, Database, Handshake, HelpCircle, ServerCrash, Shuffle, WifiOff, type LucideIcon } from 'lucide-react';

import type { AttackId } from '../api/ids';

const ICONS: Readonly<Record<string, LucideIcon>> = {
	'workers-down': ServerCrash,
	network: WifiOff,
	'lying-partner': Handshake,
	'database-failover': Database,
	clock: Clock3,
	everything: Shuffle,
	calm: Activity,
};

const ICON_SIZE = 18;

type AttackIconProps = { readonly attack: AttackId; readonly className?: string };

// AttackIcon is decorative: the card's text names the attack.
export default function AttackIcon({ attack, className }: AttackIconProps): React.JSX.Element {
	const Icon = ICONS[attack] ?? HelpCircle;

	return (
		<span className={className} aria-hidden="true">
			<Icon size={ICON_SIZE} />
		</span>
	);
}
