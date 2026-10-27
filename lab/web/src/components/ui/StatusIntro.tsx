import styles from './StatusIntro.module.css';

export type StatusTone = 'healthy' | 'warning' | 'danger' | 'info';

type StatusIntroProps = {
	readonly tone: StatusTone;
	readonly eyebrow: string;
	readonly title: React.ReactNode;
	readonly titleId?: string;
	readonly description: React.ReactNode;
	// isLive makes the signal ring breathe, the sign that the banner is
	// following something as it happens.
	readonly isLive?: boolean;
	readonly icon?: React.ReactNode;
	readonly footer?: React.ReactNode;
	readonly headingLevel?: 1 | 2;
};

// StatusIntro is the banner that opens a page with its one-line state.
export function StatusIntro({
	tone,
	eyebrow,
	title,
	titleId,
	description,
	isLive = false,
	icon,
	footer,
	headingLevel = 2,
}: StatusIntroProps): React.JSX.Element {
	const Heading = headingLevel === 1 ? 'h1' : 'h2';

	return (
		<div className={styles.intro} data-tone={tone} data-live={isLive}>
			<div className={styles.signal} aria-hidden="true">
				{icon ?? (
					<span className={styles.ring}>
						<span />
					</span>
				)}
			</div>
			<div className={styles.body}>
				<p className={styles.eyebrow}>{eyebrow}</p>
				<Heading id={titleId} className={styles.title} tabIndex={headingLevel === 1 ? -1 : undefined}>
					{title}
				</Heading>
				<div className={styles.description}>{description}</div>
				{footer === undefined ? null : <div className={styles.footer}>{footer}</div>}
			</div>
		</div>
	);
}
