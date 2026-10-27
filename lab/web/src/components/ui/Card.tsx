type CardProps = {
	readonly title: string;
	readonly titleId: string;
	readonly aside?: React.ReactNode;
	readonly as?: 'section' | 'article' | 'aside';
	readonly className?: string;
	readonly bodyClassName?: string;
	readonly children: React.ReactNode;
};

export function Card({
	title,
	titleId,
	aside,
	as: Element = 'section',
	className,
	bodyClassName = 'card-body',
	children,
}: CardProps): React.JSX.Element {
	return (
		<Element className={['card', className].filter(Boolean).join(' ')} aria-labelledby={titleId}>
			<div className="card-header">
				<h2 id={titleId} className="card-title">
					{title}
				</h2>
				{aside === undefined ? null : <div className="card-header-side">{aside}</div>}
			</div>
			<div className={bodyClassName}>{children}</div>
		</Element>
	);
}
