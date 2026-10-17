'use strict';

// The panel of docs/architecture.md, section 13. Three modes on one page:
// live (the default), ?replay=<file> plays an exported round, and
// ?compare=<round>,<round> puts two finished rounds side by side.

const REFRESH_MS = 2000;
const SVG = 'http://www.w3.org/2000/svg';
const CHART = { width: 600, height: 140 };
const TIMELINE_LENGTH = 40;

const STATE_TEXT = {
  ok: 'ok',
  violated: 'violada',
  unknown: 'aguarda',
  failed: 'falhou',
  not_checked: 'não verificada',
  pass: 'PASSOU',
  fail: 'FALHOU',
  inconclusive: 'INCONCLUSIVA',
};

const INVARIANT_LABELS = {
  I1: 'conservação do dinheiro',
  I2: 'exatamente uma vez',
  I3: 'indeterminadas resolvidas',
  I4: 'saldo nunca negativo',
  I5: 'toda saga termina',
  I6: 'concorda com o parceiro',
  I7: 'só transições válidas',
  I8: 'o passado não muda',
};

const FAMILY_TEXT = {
  process: 'processo',
  failpoint: 'failpoint',
  network: 'rede',
  database: 'banco',
  clock: 'relógio',
  partner: 'parceiro',
  heal: 'cura',
};

const COUNTER_TEXT = [
  ['kills', 'processos mortos'],
  ['failpoint_fires', 'failpoints disparados'],
  ['network_episodes', 'falhas de rede'],
  ['database_faults', 'falhas do banco'],
  ['freezes', 'congelamentos'],
  ['failovers', 'failovers'],
  ['clock_skews', 'relógios desviados'],
  ['resolved', 'indeterminadas resolvidas'],
];

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

function svgElement(tag, attributes) {
  const node = document.createElementNS(SVG, tag);

  for (const [name, value] of Object.entries(attributes)) {
    node.setAttribute(name, value);
  }

  return node;
}

function formatDuration(seconds) {
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);

  if (hours > 0) {
    return hours + 'h' + String(minutes).padStart(2, '0') + 'min';
  }

  return minutes + ' min';
}

function formatClock(milliseconds) {
  const seconds = Math.floor(milliseconds / 1000);

  return Math.floor(seconds / 60) + ':' + String(seconds % 60).padStart(2, '0');
}

function formatNumber(value) {
  return value.toLocaleString('pt-BR');
}

// lineChart draws one polyline per series; the caption and the text summary
// carry the numbers, so the chart is never the only way to read them.
function lineChart(container, seconds, series) {
  const svg = svgElement('svg', { viewBox: '0 0 ' + CHART.width + ' ' + CHART.height, role: 'img', preserveAspectRatio: 'none' });
  const peak = Math.max(1, ...seconds.flatMap((second) => series.map((one) => second[one.key])));
  const step = seconds.length > 1 ? CHART.width / (seconds.length - 1) : CHART.width;
  const summary = [];

  for (const one of series) {
    const points = seconds.map((second, index) => (index * step).toFixed(1) + ',' + (CHART.height - (second[one.key] / peak) * (CHART.height - 4)).toFixed(1));
    svg.append(svgElement('polyline', { points: points.join(' '), class: 'series series-' + one.label, fill: 'none' }));

    const last = seconds.length > 0 ? seconds[seconds.length - 1][one.key] : 0;
    summary.push(one.label + ' ' + formatNumber(Math.round(last)));
  }

  svg.setAttribute('aria-label', 'último segundo: ' + summary.join(', '));

  const legend = element('p', summary.join(' · '), { class: 'legend' });
  container.replaceChildren(svg, legend);
}

function renderTimeline(events, uptoMillis) {
  const list = document.getElementById('timeline');
  const visible = events.filter((event) => event.t_ms <= uptoMillis).slice(-TIMELINE_LENGTH).reverse();

  list.replaceChildren();

  if (visible.length === 0) {
    list.append(element('li', 'Nenhuma falha injetada ainda.'));

    return;
  }

  for (const event of visible) {
    const item = element('li', undefined, { 'data-family': event.family });
    item.append(element('time', formatClock(event.t_ms)));
    item.append(element('span', FAMILY_TEXT[event.family] || event.family, { class: 'family' }));
    item.append(element('span', event.text));
    list.append(item);
  }
}

function renderCounters(container, counters) {
  container.replaceChildren();

  for (const [key, label] of COUNTER_TEXT) {
    const row = element('div');
    row.append(element('dt', label));
    row.append(element('dd', formatNumber(counters[key] || 0)));
    container.append(row);
  }
}

function renderChecks(container, checks, verdict) {
  container.replaceChildren();

  for (const id of Object.keys(INVARIANT_LABELS)) {
    const check = checks.find((one) => one.id === id);
    const state = check ? check.state || check.status : 'unknown';
    const item = element('li', undefined, { class: 'check', 'data-state': state });
    item.append(element('strong', id + ' ' + INVARIANT_LABELS[id]));
    item.append(element('span', STATE_TEXT[state] || state));

    if (check && check.violations > 0) {
      item.append(element('span', check.violations + ' violação(ões)'));
    }

    container.append(item);
  }

  if (verdict) {
    container.append(element('li', 'Veredito da rodada: ' + (STATE_TEXT[verdict] || verdict), { class: 'check verdict', 'data-state': verdict }));
  }
}

// renderReplay paints one round up to a point in time.
function renderReplay(root, round, uptoMillis) {
  const seconds = round.seconds.filter((second) => second.t * 1000 <= uptoMillis);

  lineChart(root.querySelector('[data-chart="throughput"]') || document.getElementById('throughput'), seconds, [
    { key: 'ok', label: 'ok' },
    { key: 'fail', label: 'fail' },
    { key: 'info', label: 'info' },
  ]);
  lineChart(root.querySelector('[data-chart="latency"]') || document.getElementById('latency'), seconds, [
    { key: 'p50_ms', label: 'p50' },
    { key: 'p99_ms', label: 'p99' },
  ]);
}

function renderStrip(round, mode) {
  const strip = document.getElementById('strip');

  if (!round) {
    return;
  }

  strip.textContent = mode + ' · rodada ' + round.round + ' · cenário ' + round.scenario + ' · semente ' + round.seed;
}

function renderHeadline(headline) {
  document.getElementById('chaos-time').textContent = formatDuration(headline.chaos_seconds);
  document.getElementById('kills').textContent = formatNumber(headline.kills);

  const failed = document.getElementById('failed-rounds');
  failed.textContent = formatNumber(headline.failed_rounds) + ' de ' + formatNumber(headline.rounds);
  failed.setAttribute('data-state', headline.failed_rounds > 0 ? 'fail' : 'ok');
}

function renderRounds(rounds) {
  const body = document.getElementById('rounds');
  body.replaceChildren();

  for (const round of rounds) {
    const row = element('tr');
    row.append(element('td', round.round));
    row.append(element('td', round.scenario));
    row.append(element('td', formatDuration(round.seconds)));
    row.append(element('td', formatNumber(round.operations)));
    row.append(element('td', formatNumber(round.fails)));
    row.append(element('td', STATE_TEXT[round.verdict] || round.verdict, { 'data-state': round.verdict }));

    const link = element('td');
    link.append(element('a', 'relatório de ' + round.round, { href: round.report_url }));
    row.append(link);
    body.append(row);
  }
}

async function getJSON(url) {
  const response = await fetch(url, { cache: 'no-store' });

  if (!response.ok) {
    throw new Error(url + ' answered ' + response.status);
  }

  return response.json();
}

async function refreshLive() {
  try {
    const [live, status] = await Promise.all([getJSON('api/live'), getJSON('api/status')]);

    renderHeadline(live.headline);
    renderRounds(live.rounds);

    if (live.current) {
      renderStrip(live.current, 'ao vivo');
      renderReplay(document, live.current, Number.MAX_SAFE_INTEGER);
      renderTimeline(live.current.events, Number.MAX_SAFE_INTEGER);
      renderCounters(document.getElementById('counters'), live.current.counters);
    }

    const last = status.last_round;
    const checks = status.live.checks.concat(last ? last.invariants.filter((one) => !status.live.checks.some((live) => live.id === one.id)) : []);
    renderChecks(document.getElementById('checks'), checks, last && live.current && last.round === live.current.round ? last.verdict : undefined);
  } catch (error) {
    document.getElementById('strip').textContent = 'O painel perdeu contato com o laboratório; tentando de novo.';
  }
}

// playReplay plays an exported round at the chosen speed.
async function playReplay(url) {
  const round = await getJSON(url);
  const total = round.seconds.length * 1000;
  const play = document.getElementById('play');
  const speed = document.getElementById('speed');
  let position = 0;
  let isPlaying = true;
  let last = performance.now();

  document.getElementById('player').hidden = false;
  document.getElementById('rounds-section').hidden = true;
  renderStrip(round, 'replay');
  renderCounters(document.getElementById('counters'), round.counters);

  play.addEventListener('click', () => {
    isPlaying = !isPlaying;
    play.textContent = isPlaying ? 'Pausar' : 'Continuar';
    play.setAttribute('aria-pressed', String(!isPlaying));
  });

  function frame(now) {
    if (isPlaying) {
      position = Math.min(total, position + (now - last) * Number(speed.value));
    }

    last = now;
    renderReplay(document, round, position);
    renderTimeline(round.events, position);
    document.getElementById('clock').textContent = formatClock(position) + ' de ' + formatClock(total);

    const isDone = position >= total;
    renderChecks(document.getElementById('checks'), isDone ? round.checks || [] : [], isDone ? round.verdict : undefined);

    if (!isDone) {
      setTimeout(() => requestAnimationFrame(frame), 250);
    }
  }

  requestAnimationFrame(frame);
}

// compareRounds puts two finished rounds side by side: usually the same
// seed with the naive and the robust profile.
async function compareRounds(ids) {
  const rounds = await Promise.all(ids.map((id) => getJSON('api/replay/' + encodeURIComponent(id))));
  const columns = document.getElementById('columns');
  const template = document.getElementById('column-main');

  columns.classList.add('side-by-side');
  columns.replaceChildren();
  document.getElementById('strip').textContent = 'lado a lado · ' + rounds.map((round) => round.round + ' (' + round.scenario + ', semente ' + round.seed + ')').join(' e ');

  rounds.forEach((round, index) => {
    const column = template.cloneNode(true);
    column.id = 'column-' + index;
    column.setAttribute('aria-labelledby', 'column-title-' + index);
    column.querySelector('h2').id = 'column-title-' + index;
    column.querySelector('h2').textContent = 'Rodada ' + round.round + ': ' + (STATE_TEXT[round.verdict] || round.verdict);
    column.setAttribute('data-state', round.verdict);

    for (const node of column.querySelectorAll('[id]')) {
      if (node !== column) {
        node.removeAttribute('id');
      }
    }

    column.querySelectorAll('.chart')[0].setAttribute('data-chart', 'throughput');
    column.querySelectorAll('.chart')[1].setAttribute('data-chart', 'latency');
    column.querySelector('.player').remove();
    columns.append(column);

    renderReplay(column, round, Number.MAX_SAFE_INTEGER);
    renderCounters(column.querySelector('.counters'), round.counters);
    renderChecks(column.querySelector('.checks'), round.checks || [], round.verdict);

    const timeline = column.querySelector('.timeline');
    timeline.replaceChildren(...round.events.slice(-TIMELINE_LENGTH).reverse().map((event) => element('li', formatClock(event.t_ms) + ' ' + event.text, { 'data-family': event.family })));
  });
}

function start() {
  const params = new URLSearchParams(window.location.search);
  const replayFile = params.get('replay');
  const compare = params.get('compare');

  if (replayFile) {
    playReplay(replayFile).catch(() => { document.getElementById('strip').textContent = 'Não foi possível carregar o replay ' + replayFile + '.'; });

    return;
  }

  if (compare) {
    compareRounds(compare.split(',').slice(0, 2)).catch(() => { document.getElementById('strip').textContent = 'Não foi possível carregar as rodadas ' + compare + '.'; });

    return;
  }

  refreshLive();
  setInterval(refreshLive, REFRESH_MS);
}

start();
