import { splitTerms } from '../../domain/terms';

type GlossedProps = { readonly text: string };

// Glossed writes a text with its glossary terms underlined: each term is a
// button that opens its entry on hover, focus or tap.
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
