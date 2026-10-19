'use strict';

// The panel. Three modes on one page: live (the default), ?replay=<file> plays
// an exported round, and ?compare=<round>,<round> puts two finished rounds side
// by side. Every text comes from messages.js.

const REFRESH_MS = 2000;
const FRAME_INTERVAL_MS = 250;
const MS_PER_SECOND = 1000;
const SECONDS_PER_MINUTE = 60;
const SECONDS_PER_HOUR = 3600;
const CLOCK_DIGITS = 2;
const SVG = 'http://www.w3.org/2000/svg';
const CHART = Object.freeze({ width: 600, height: 140, padding: 4 });
const TIMELINE_LENGTH = 40;
const COMPARED_ROUNDS = 2;

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

function twoDigits(value) {
  return String(value).padStart(CLOCK_DIGITS, '0');
}

function formatDuration(seconds) {
  const hours = Math.floor(seconds / SECONDS_PER_HOUR);
  const minutes = Math.floor((seconds % SECONDS_PER_HOUR) / SECONDS_PER_MINUTE);

  if (hours > 0) {
    return format(MESSAGES.durationHours, { hours, minutes: twoDigits(minutes) });
  }

  return format(MESSAGES.durationMinutes, { minutes });
}

function formatClock(milliseconds) {
  const seconds = Math.floor(milliseconds / MS_PER_SECOND);

  return Math.floor(seconds / SECONDS_PER_MINUTE) + ':' + twoDigits(seconds % SECONDS_PER_MINUTE);
}

function formatNumber(value) {
  return value.toLocaleString(LOCALE);
}

function stateText(state) {
  return MESSAGES.state[state] || state;
}

function setStrip(text) {
  document.getElementById('strip').textContent = text;
}

// lineChart draws one polyline per series; the caption and the text summary
// carry the numbers, so the chart is never the only way to read them.
function lineChart(container, seconds, series) {
  const svg = svgElement('svg', { viewBox: '0 0 ' + CHART.width + ' ' + CHART.height, role: 'img', preserveAspectRatio: 'none' });
  const values = seconds.flatMap((second) => series.map((one) => second[one.key]));
  const peak = Math.max(1, ...values);
  const step = seconds.length > 1 ? CHART.width / (seconds.length - 1) : CHART.width;
  const summary = [];

  for (const one of series) {
    const points = seconds.map((second, index) => pointOf(index * step, second[one.key], peak));
    svg.append(svgElement('polyline', { points: points.join(' '), class: 'series series-' + one.label, fill: 'none' }));

    const last = seconds.length > 0 ? seconds[seconds.length - 1][one.key] : 0;
    summary.push(one.label + ' ' + formatNumber(Math.round(last)));
  }

  svg.setAttribute('aria-label', format(MESSAGES.chartLastSecond, { summary: summary.join(', ') }));

  const legend = element('p', summary.join(' · '), { class: 'legend' });
  container.replaceChildren(svg, legend);
}

function pointOf(x, value, peak) {
  const y = CHART.height - (value / peak) * (CHART.height - CHART.padding);

  return x.toFixed(1) + ',' + y.toFixed(1);
}

function renderTimeline(events, uptoMillis) {
  const list = document.getElementById('timeline');
  const visible = events.filter((event) => event.t_ms <= uptoMillis).slice(-TIMELINE_LENGTH).reverse();

  list.replaceChildren();

  if (visible.length === 0) {
    list.append(element('li', MESSAGES.noFaultsYet));

    return;
  }

  for (const event of visible) {
    const item = element('li', undefined, { 'data-family': event.family });
    item.append(element('time', formatClock(event.t_ms)));
    item.append(element('span', MESSAGES.families[event.family] || event.family, { class: 'family' }));
    item.append(element('span', event.text));
    list.append(item);
  }
}

function renderCounters(container, counters) {
  container.replaceChildren();

  for (const [key, label] of MESSAGES.counters) {
    const row = element('div');
    row.append(element('dt', label));
    row.append(element('dd', formatNumber(counters[key] || 0)));
    container.append(row);
  }
}

function renderChecks(container, checks, verdict) {
  container.replaceChildren();

  for (const id of Object.keys(MESSAGES.invariants)) {
    const check = checks.find((one) => one.id === id);
    const state = check ? check.state || check.status : 'unknown';
    const item = element('li', undefined, { class: 'check', 'data-state': state });
    item.append(element('strong', id + ' ' + MESSAGES.invariants[id]));
    item.append(element('span', stateText(state)));

    const hasViolations = check && check.violations > 0;
    if (hasViolations) {
      item.append(element('span', format(MESSAGES.violations, { count: check.violations })));
    }

    container.append(item);
  }

  if (verdict) {
    const text = format(MESSAGES.roundVerdict, { verdict: stateText(verdict) });
    container.append(element('li', text, { class: 'check verdict', 'data-state': verdict }));
  }
}

// renderReplay paints one round up to a point in time.
function renderReplay(root, round, uptoMillis) {
  const seconds = round.seconds.filter((second) => second.t * MS_PER_SECOND <= uptoMillis);
  const throughput = root.querySelector('[data-chart="throughput"]') || document.getElementById('throughput');
  const latency = root.querySelector('[data-chart="latency"]') || document.getElementById('latency');

  lineChart(throughput, seconds, [{ key: 'ok', label: 'ok' }, { key: 'fail', label: 'fail' }, { key: 'info', label: 'info' }]);
  lineChart(latency, seconds, [{ key: 'p50_ms', label: 'p50' }, { key: 'p99_ms', label: 'p99' }]);
}

function renderStrip(round, mode) {
  if (!round) {
    return;
  }

  setStrip(format(MESSAGES.strip, { mode, round: round.round, scenario: round.scenario, seed: round.seed }));
}

function renderHeadline(headline) {
  document.getElementById('chaos-time').textContent = formatDuration(headline.chaos_seconds);
  document.getElementById('kills').textContent = formatNumber(headline.kills);

  const failed = document.getElementById('failed-rounds');
  failed.textContent = format(MESSAGES.failedOf, { failed: formatNumber(headline.failed_rounds), total: formatNumber(headline.rounds) });
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
    row.append(element('td', stateText(round.verdict), { 'data-state': round.verdict }));

    const link = element('td');
    link.append(element('a', format(MESSAGES.reportOf, { round: round.round }), { href: round.report_url }));
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

// liveChecks is the auditor's live checks, then the invariants of the last
// finished round that the auditor does not cover.
function liveChecks(status) {
  const last = status.last_round;

  if (!last) {
    return status.live.checks;
  }

  const isNotLive = (one) => !status.live.checks.some((live) => live.id === one.id);

  return status.live.checks.concat(last.invariants.filter(isNotLive));
}

async function refreshLive() {
  try {
    const [live, status] = await Promise.all([getJSON('api/live'), getJSON('api/status')]);

    renderHeadline(live.headline);
    renderRounds(live.rounds);

    if (live.current) {
      renderStrip(live.current, MESSAGES.modeLive);
      renderReplay(document, live.current, Number.MAX_SAFE_INTEGER);
      renderTimeline(live.current.events, Number.MAX_SAFE_INTEGER);
      renderCounters(document.getElementById('counters'), live.current.counters);
    }

    const last = status.last_round;
    const isCurrentRoundDone = Boolean(last && live.current && last.round === live.current.round);
    renderChecks(document.getElementById('checks'), liveChecks(status), isCurrentRoundDone ? last.verdict : undefined);
  } catch (error) {
    setStrip(MESSAGES.lostContact);
  }
}

// playReplay plays an exported round at the chosen speed.
async function playReplay(url) {
  const round = await getJSON(url);
  const total = round.seconds.length * MS_PER_SECOND;
  const play = document.getElementById('play');
  const speed = document.getElementById('speed');
  const player = { position: 0, isPlaying: true, last: performance.now() };

  document.getElementById('player').hidden = false;
  document.getElementById('rounds-section').hidden = true;
  renderStrip(round, MESSAGES.modeReplay);
  renderCounters(document.getElementById('counters'), round.counters);

  function togglePlay() {
    player.isPlaying = !player.isPlaying;
    play.textContent = player.isPlaying ? MESSAGES.pause : MESSAGES.resume;
  }

  function frame(now) {
    if (player.isPlaying) {
      player.position = Math.min(total, player.position + (now - player.last) * Number(speed.value));
    }

    player.last = now;
    renderReplay(document, round, player.position);
    renderTimeline(round.events, player.position);
    document.getElementById('clock').textContent = format(MESSAGES.clockOf, { position: formatClock(player.position), total: formatClock(total) });

    const isDone = player.position >= total;
    renderChecks(document.getElementById('checks'), isDone ? round.checks || [] : [], isDone ? round.verdict : undefined);

    if (!isDone) {
      setTimeout(requestNextFrame, FRAME_INTERVAL_MS);
    }
  }

  function requestNextFrame() {
    requestAnimationFrame(frame);
  }

  play.textContent = MESSAGES.pause;
  play.addEventListener('click', togglePlay);
  requestAnimationFrame(frame);
}

// compareRounds puts two finished rounds side by side: usually the same
// seed with the naive and the robust profile.
async function compareRounds(ids) {
  const urls = ids.map((id) => 'api/replay/' + encodeURIComponent(id));
  const rounds = await Promise.all(urls.map(getJSON));
  const columns = document.getElementById('columns');
  const template = document.getElementById('column-main');
  const described = rounds.map((round) => format(MESSAGES.sideBySideRound, { round: round.round, scenario: round.scenario, seed: round.seed }));

  columns.classList.add('side-by-side');
  columns.replaceChildren();
  setStrip(format(MESSAGES.sideBySide, { rounds: described.join(MESSAGES.sideBySideJoin) }));

  rounds.forEach((round, index) => columns.append(renderColumn(template, round, index)));
}

// renderColumn copies the main column for one compared round.
function renderColumn(template, round, index) {
  const column = template.cloneNode(true);
  const title = column.querySelector('h2');

  column.id = 'column-' + index;
  column.setAttribute('aria-labelledby', 'column-title-' + index);
  column.setAttribute('data-state', round.verdict);
  title.id = 'column-title-' + index;
  title.textContent = format(MESSAGES.columnTitle, { round: round.round, verdict: stateText(round.verdict) });

  for (const node of column.querySelectorAll('[id]')) {
    if (node !== title) {
      node.removeAttribute('id');
    }
  }

  const [throughput, latency] = column.querySelectorAll('.chart');
  throughput.setAttribute('data-chart', 'throughput');
  latency.setAttribute('data-chart', 'latency');
  column.querySelector('.player').remove();

  renderReplay(column, round, Number.MAX_SAFE_INTEGER);
  renderCounters(column.querySelector('.counters'), round.counters);
  renderChecks(column.querySelector('.checks'), round.checks || [], round.verdict);

  const events = round.events.slice(-TIMELINE_LENGTH).reverse();
  column.querySelector('.timeline').replaceChildren(...events.map(timelineItem));

  return column;
}

function timelineItem(event) {
  return element('li', formatClock(event.t_ms) + ' ' + event.text, { 'data-family': event.family });
}

function showReplayError(file) {
  setStrip(format(MESSAGES.replayFailed, { file }));
}

function showCompareError(rounds) {
  setStrip(format(MESSAGES.compareFailed, { rounds }));
}

function start() {
  const params = new URLSearchParams(window.location.search);
  const replayFile = params.get('replay');
  const compare = params.get('compare');

  if (replayFile) {
    playReplay(replayFile).catch(() => showReplayError(replayFile));

    return;
  }

  if (compare) {
    compareRounds(compare.split(',').slice(0, COMPARED_ROUNDS)).catch(() => showCompareError(compare));

    return;
  }

  refreshLive();
  setInterval(refreshLive, REFRESH_MS);
}

start();
