// Tests the panel's message catalog without a browser: run with node --test.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import vm from 'node:vm';

const staticDir = fileURLToPath(new URL('../static/', import.meta.url));
const messagesSource = readFileSync(staticDir + 'messages.js', 'utf8');
const appSource = readFileSync(staticDir + 'app.js', 'utf8');

function loadMessages() {
  const context = vm.createContext({});
  vm.runInContext(messagesSource + '\nthis.MESSAGES = MESSAGES; this.format = format;', context);

  return context;
}

test('format fills every placeholder and leaves unknown ones', () => {
  const { format } = loadMessages();

  assert.equal(format('rodada {round} de {total}', { round: 'r0001', total: 3 }), 'rodada r0001 de 3');
  assert.equal(format('{missing} fica', {}), '{missing} fica');
});

test('every message the panel reads exists in the catalog', () => {
  const { MESSAGES } = loadMessages();
  const used = new Set([...appSource.matchAll(/MESSAGES\.(\w+)/g)].map((match) => match[1]));

  for (const key of used) {
    assert.ok(key in MESSAGES, 'missing message: ' + key);
  }
});

test('the panel writes no Portuguese text of its own', () => {
  const literals = [...appSource.matchAll(/'([^'\n]*)'/g)].map((match) => match[1]);
  const portuguese = literals.filter((text) => /[áàâãéêíóôõúç]/i.test(text));

  assert.deepEqual(portuguese, []);
});
