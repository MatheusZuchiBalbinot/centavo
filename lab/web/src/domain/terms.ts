import { glossary } from '../i18n/glossary';
import type { TermId } from '../i18n/glossary';

// A text cut into plain runs and glossary terms.
export type TextSegment =
  | { readonly kind: 'text'; readonly text: string }
  | { readonly kind: 'term'; readonly term: TermId; readonly text: string };

type Form = { readonly term: TermId; readonly form: string };

// Every way a term is written, longest first, so "chave de idempotência"
// wins over "idempotência".
const FORMS: readonly Form[] = (Object.keys(glossary) as TermId[])
  .flatMap((term) => glossary[term].forms.map((form) => ({ term, form })))
  .sort((a, b) => b.form.length - a.form.length);

const PATTERN = new RegExp(`(?<!\\p{L})(${FORMS.map((entry) => escape(entry.form)).join('|')})(?!\\p{L})`, 'giu');

// splitTerms marks the glossary terms of a text. Each term is marked once,
// at its first appearance, so a sentence never turns into a row of links.
export function splitTerms(text: string): readonly TextSegment[] {
  const segments: TextSegment[] = [];
  const seen = new Set<TermId>();
  let cursor = 0;

  for (const match of text.matchAll(PATTERN)) {
    const found = match[0];
    const term = termOf(found);
    const start = match.index;

    if (term === null || seen.has(term)) {
      continue;
    }

    seen.add(term);

    if (start > cursor) {
      segments.push({ kind: 'text', text: text.slice(cursor, start) });
    }

    segments.push({ kind: 'term', term, text: found });
    cursor = start + found.length;
  }

  if (cursor < text.length) {
    segments.push({ kind: 'text', text: text.slice(cursor) });
  }

  return segments;
}

function termOf(found: string): TermId | null {
  const lower = found.toLocaleLowerCase('pt-BR');

  return FORMS.find((entry) => entry.form === lower)?.term ?? null;
}

function escape(form: string): string {
  return form.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}
