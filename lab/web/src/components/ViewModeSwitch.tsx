import { useViewMode, VIEW_MODES } from '../hooks/viewModeContext';
import { messages } from '../i18n/pt-BR';
import styles from './Layout.module.css';

// ViewModeSwitch is the top bar's "Como mostrar" choice.
export default function ViewModeSwitch(): React.JSX.Element {
	const { mode, setMode } = useViewMode();

	return (
		<div className={styles.modeSwitch} role="group" aria-label={messages.modes.label}>
			<span className={styles.modeLabel} aria-hidden="true">
				{messages.modes.label}
			</span>
			{VIEW_MODES.map((option) => (
				<button
					key={option}
					type="button"
					className="btn btn-pill"
					aria-pressed={option === mode}
					data-tooltip={messages.modes.help[option]}
					onClick={() => setMode(option)}
				>
					{messages.modes.names[option]}
				</button>
			))}
		</div>
	);
}
