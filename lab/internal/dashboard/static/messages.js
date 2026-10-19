'use strict';

// Every text the panel writes, in the panel's one language. app.js reads them
// by key; a text with {placeholders} goes through format().

const LOCALE = 'pt-BR';

const MESSAGES = Object.freeze({
  state: Object.freeze({
    ok: 'ok',
    violated: 'violada',
    unknown: 'aguarda',
    failed: 'falhou',
    not_checked: 'não verificada',
    pass: 'PASSOU',
    fail: 'FALHOU',
    inconclusive: 'INCONCLUSIVA',
  }),
  invariants: Object.freeze({
    I1: 'conservação do dinheiro',
    I2: 'exatamente uma vez',
    I3: 'indeterminadas resolvidas',
    I4: 'saldo nunca negativo',
    I5: 'toda saga termina',
    I6: 'concorda com o parceiro',
    I7: 'só transições válidas',
    I8: 'o passado não muda',
  }),
  families: Object.freeze({
    process: 'processo',
    failpoint: 'failpoint',
    network: 'rede',
    database: 'banco',
    clock: 'relógio',
    partner: 'parceiro',
    heal: 'cura',
  }),
  counters: Object.freeze([
    ['kills', 'processos mortos'],
    ['failpoint_fires', 'failpoints disparados'],
    ['network_episodes', 'falhas de rede'],
    ['database_faults', 'falhas do banco'],
    ['freezes', 'congelamentos'],
    ['failovers', 'failovers'],
    ['clock_skews', 'relógios desviados'],
    ['resolved', 'indeterminadas resolvidas'],
  ]),
  durationHours: '{hours}h{minutes}min',
  durationMinutes: '{minutes} min',
  chartLastSecond: 'último segundo: {summary}',
  noFaultsYet: 'Nenhuma falha injetada ainda.',
  violations: '{count} violação(ões)',
  roundVerdict: 'Veredito da rodada: {verdict}',
  strip: '{mode} · rodada {round} · cenário {scenario} · semente {seed}',
  modeLive: 'ao vivo',
  modeReplay: 'replay',
  failedOf: '{failed} de {total}',
  reportOf: 'relatório de {round}',
  lostContact: 'O painel perdeu contato com o laboratório; tentando de novo.',
  pause: 'Pausar',
  resume: 'Continuar',
  clockOf: '{position} de {total}',
  sideBySide: 'lado a lado · {rounds}',
  sideBySideRound: '{round} ({scenario}, semente {seed})',
  sideBySideJoin: ' e ',
  columnTitle: 'Rodada {round}: {verdict}',
  replayFailed: 'Não foi possível carregar o replay {file}.',
  compareFailed: 'Não foi possível carregar as rodadas {rounds}.',
});

// format fills a message's {placeholders} with values.
function format(message, values) {
  return message.replace(/\{(\w+)\}/g, (match, name) => (name in values ? String(values[name]) : match));
}
