import type { ApiFailure } from '../api/client';
import { format } from '../i18n/format';
import { messages } from '../i18n/pt-BR';

const knownErrors: Readonly<Partial<Record<string, string>>> = messages.errors;

// errorMessage says what went wrong in the panel's words; a code without its
// own text still names the code, so it can be looked up.
export function errorMessage(failure: ApiFailure): string {
  return knownErrors[failure.code] ?? format(messages.errors.fallback, { code: failure.code });
}
