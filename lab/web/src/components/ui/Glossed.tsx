import { splitTerms } from '../../domain/terms';

type GlossedProps = { readonly text: string };

export function Glossed({ text }: GlossedProps): React.JSX.Element {
	return (
		<>
			{splitTerms(text).map((segment, index) =>
				segment.kind === 'text' ? (
					segment.text
				) : (
					<button key={index} type="button" className="term" data-term={segment.term}>
						{segment.text}
					</button>
				),
			)}
		</>
	);
}
