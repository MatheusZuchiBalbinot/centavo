'use strict';

const REFRESH_MS = 5000;

const STATE_TEXT = {
  ok: 'ok',
  violated: 'violada',
  unknown: 'desconhecido',
  failed: 'falhou',
  not_checked: 'não verificada',
  pass: 'PASSOU',
  fail: 'FALHOU',
};

function element(tag, text, attributes) {
  const node = document.createElement(tag);

  if (text !== undefined) {
    node.textContent = text;
  }

  for (const [name, value] of Object.entries(attributes || {})) {
    node.setAttribute(name, value);
  }

  return node;
}

function renderLive(live) {
  const at = document.getElementById('live-at');
  const list = document.getElementById('live-checks');

  at.textContent = live.at ? 'Leitura de ' + new Date(live.at).toLocaleTimeString('pt-BR') + ', a cada 5 s.' : 'Aguardando a primeira leitura do auditor.';
  list.replaceChildren();

  for (const check of live.checks) {
    const item = element('li', undefined, { class: 'check', 'data-state': check.state });
    item.append(element('strong', check.id + ' ' + check.label + ': ' + STATE_TEXT[check.state]));
    item.append(element('span', check.detail));
    list.append(item);
  }
}

function renderRound(round) {
  const container = document.getElementById('round');

  if (!round) {
    return;
  }

  const verdict = element('p', undefined, { class: 'check', 'data-state': round.verdict });
  verdict.append(element('strong', 'Rodada ' + round.round + ': ' + STATE_TEXT[round.verdict]));
  verdict.append(element('span', 'cenário ' + round.scenario + ', semente ' + round.seed + ', ' + round.operations.total + ' operações (ok ' + round.operations.ok + ', fail ' + round.operations.fail + ', info ' + round.operations.info + ')'));

  const table = element('table');
  const head = element('tr');
  for (const title of ['Invariante', 'Resultado', 'Observação']) {
    head.append(element('th', title, { scope: 'col' }));
  }
  table.append(head);

  for (const invariant of round.invariants) {
    const row = element('tr');
    const result = invariant.violations > 0 ? STATE_TEXT[invariant.status] + ' (' + invariant.violations + ')' : STATE_TEXT[invariant.status];
    row.append(element('td', invariant.id));
    row.append(element('td', result));
    row.append(element('td', invariant.note || ''));
    table.append(row);
  }

  const reportLine = element('p');
  reportLine.append(element('a', 'Relatório completo da rodada ' + round.round, { href: round.report_url }));
  container.replaceChildren(verdict, table, reportLine);
}

async function refresh() {
  try {
    const response = await fetch('/api/status', { cache: 'no-store' });
    if (!response.ok) {
      return;
    }

    const status = await response.json();
    renderLive(status.live);
    renderRound(status.last_round);
  } catch (error) {
    document.getElementById('live-at').textContent = 'O painel perdeu contato com o laboratório; tentando de novo.';
  }
}

refresh();
setInterval(refresh, REFRESH_MS);
