'use strict';

// The panel. Three modes on one page: live (the default), ?replay=<file> plays
// an exported round, and ?compare=<round>,<round> puts two finished rounds side
// by side. Every text comes from messages.js.

const REFRESH_MS = 2000;
const FRAME_INTERVAL_MS = 250;
const MS_PER_SECOND = 1000;
const SECONDS_PER_MINUTE = 60;
const SECONDS_PER_HOUR = 3600;
const COMPARED_ROUNDS = 2;
const MAX_LISTED_ATTACKS = 12;
const SVG = 'http://www.w3.org/2000/svg';
const PHASE_IDS = Object.freeze(['attack', 'heal', 'check', 'verdict']);

// The geometry of the timeline, in viewBox units.
const TRACK = Object.freeze({
  width: 1000,
  compareWidth: 620,
  left: 150,
  right: 12,
  attackTop: 4,
  attackHeight: 34,
  clientsTop: 52,
  clientsHeight: 150,
  latencyTop: 218,
  latencyHeight: 56,
  axisTop: 290,
  height: 312,
  markerRadius: 9,
  markerStagger: 8,
});

// Tick spacing of the time axis, by how long the round lasts.
const TICKS = Object.freeze([
  { upTo: 120, every: 15 },
  { upTo: 420, every: 30 },
  { upTo: 1200, every: 60 },
  { upTo: Infinity, every: 300 },
]);

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

function svgElement(tag, attributes, text) {
  const node = document.createElementNS(SVG, tag);

  for (const [name, value] of Object.entries(attributes)) {
    node.setAttribute(name, value);
  }

  if (text !== undefined) {
    node.textContent = text;
  }

  return node;
}

function formatNumber(value) {
  return Math.round(value).toLocaleString(LOCALE);
}

function formatDuration(totalSeconds) {
  const seconds = Math.max(0, Math.floor(totalSeconds));
  const hours = Math.floor(seconds / SECONDS_PER_HOUR);
  const minutes = Math.floor((seconds % SECONDS_PER_HOUR) / SECONDS_PER_MINUTE);

  if (hours > 0) {
    return format(MESSAGES.durationHours, { hours, minutes });
  }

  if (minutes > 0) {
    return format(MESSAGES.durationMinutes, { minutes, seconds: seconds % SECONDS_PER_MINUTE });
  }

  return format(MESSAGES.durationSeconds, { seconds });
}

function formatClock(milliseconds) {
  const seconds = Math.floor(milliseconds / MS_PER_SECOND);
  const rest = String(seconds % SECONDS_PER_MINUTE).padStart(2, '0');

  return Math.floor(seconds / SECONDS_PER_MINUTE) + ':' + rest;
}

function field(root, name) {
  return root.querySelector('[data-field="' + name + '"]');
}

// A round, as far as the panel has seen it: up to uptoMillis in a replay, all
// of it live.
function roundUpTo(round, uptoMillis) {
  const seconds = round.seconds.filter((second) => second.t * MS_PER_SECOND <= uptoMillis);
  const events = round.events.filter((event) => event.t_ms <= uptoMillis);
  const lastSecond = round.seconds.length > 0 ? round.seconds[round.seconds.length - 1].t + 1 : 0;
  const isOver = uptoMillis >= lastSecond * MS_PER_SECOND;

  return { ...round, seconds, events, isOver };
}

function isHeal(event) {
  return event.family === 'heal';
}

function isAttack(event) {
  return event.family !== 'heal';
}

// phaseOf reads where the round is from the heal events the history records:
// before the cure starts it is under attack, between its start and its end it
// heals, after it the checker runs, and the verdict closes it.
function phaseOf(round) {
  const hasVerdict = Boolean(round.verdict) && round.isOver;
  if (hasVerdict) {
    return 'verdict';
  }

  const heals = round.events.filter(isHeal).length;

  if (heals >= 2) {
    return 'check';
  }

  if (heals === 1) {
    return 'heal';
  }

  return 'attack';
}

function attacksText(count) {
  if (count === 1) {
    return MESSAGES.attacksOne;
  }

  return format(MESSAGES.attacksMany, { count: formatNumber(count) });
}

function moneyText(liveChecks) {
  if (liveChecks.length === 0) {
    return MESSAGES.moneyUnknown;
  }

  const isBroken = liveChecks.some((check) => check.state === 'violated');
  if (isBroken) {
    return MESSAGES.moneyBroken;
  }

  return MESSAGES.moneyOk;
}

function brokenGuarantees(checks) {
  const failed = new Set(checks.filter((check) => check.status === 'failed').map((check) => check.id));

  return MESSAGES.guarantees.filter((guarantee) => failed.has(guarantee.id)).map((guarantee) => guarantee.name);
}

function headlineOf(round, phase, context) {
  if (phase === 'attack') {
    const elapsed = context.elapsedSeconds;
    const total = round.duration_seconds || elapsed;

    return format(MESSAGES.headlineAttack, { elapsed: formatDuration(elapsed), total: formatDuration(total), money: moneyText(context.liveChecks) });
  }

  if (phase === 'heal') {
    return MESSAGES.headlineHeal;
  }

  if (phase === 'check') {
    return MESSAGES.headlineCheck;
  }

  return verdictHeadline(round);
}

function verdictHeadline(round) {
  const attacks = round.events.filter(isAttack).length;

  const isCalmPass = round.verdict === 'pass' && attacks === 0;
  if (isCalmPass) {
    return MESSAGES.headlinePassCalm;
  }

  if (round.verdict === 'pass') {
    return format(MESSAGES.headlinePass, { attacks: attacksText(attacks) });
  }

  if (round.verdict === 'fail') {
    return format(MESSAGES.headlineFail, { guarantees: brokenGuarantees(round.checks || []).join(MESSAGES.listSeparator) });
  }

  return MESSAGES.headlineInconclusive;
}

// expectationOf says what the defense profile should make of the scenario.
function expectationOf(defenses) {
  if (!defenses) {
    return MESSAGES.expectUnknown;
  }

  const [base, ...changes] = defenses.split(',');

  if (base === 'naive') {
    return MESSAGES.expectNaive;
  }

  const disabled = changes.filter((change) => change.startsWith('-')).map((change) => change.slice(1));

  if (disabled.length === 0) {
    return MESSAGES.expectRobust;
  }

  const names = disabled.map((name) => MESSAGES.defenses[name] || name);

  return format(MESSAGES.expectWithout, { defenses: names.join(', ') });
}

function renderHead(article, round, phase, context) {
  const scenario = field(article, 'scenario');

  field(article, 'round-id').textContent = round.round + ' · ' + round.seed;
  field(article, 'headline').textContent = headlineOf(round, phase, context);

  scenario.replaceChildren(element('strong', format(MESSAGES.scenario, { name: round.scenario })));

  if (round.description) {
    scenario.append(': ' + round.description);
  }

  field(article, 'expectation').textContent = expectationOf(round.defenses);
  article.setAttribute('data-verdict', phase === 'verdict' ? round.verdict : '');
}

function renderPhases(article, phase) {
  const list = field(article, 'phases');
  const current = PHASE_IDS.indexOf(phase);

  field(article, 'phases-title').textContent = MESSAGES.phasesTitle;
  list.replaceChildren();

  MESSAGES.phases.forEach((step, index) => list.append(phaseItem(step, index, current)));
}

function phaseItem(step, index, current) {
  const state = phaseState(index, current);
  const item = element('li', undefined, { class: 'phase', 'data-state': state });

  if (state === 'current') {
    item.setAttribute('aria-current', 'step');
  }

  item.append(element('span', step.name, { class: 'phase-name' }));
  item.append(element('span', step.detail, { class: 'phase-detail' }));
  item.append(element('span', MESSAGES.phaseState[state], { class: 'phase-state' }));

  return item;
}

function phaseState(index, current) {
  const isFinished = current === PHASE_IDS.length - 1;
  if (isFinished) {
    return 'done';
  }

  if (index < current) {
    return 'done';
  }

  if (index === current) {
    return 'current';
  }

  return 'upcoming';
}

// spanOf is how many seconds the time axis covers: the planned round, or what
// the history already holds when the cure ran past it.
function spanOf(round, nowSeconds) {
  const lastSecond = round.seconds.length > 0 ? round.seconds[round.seconds.length - 1].t + 1 : 0;
  const lastEvent = round.events.reduce((latest, event) => Math.max(latest, event.t_ms / MS_PER_SECOND), 0);

  return Math.max(1, round.duration_seconds || 0, lastSecond, lastEvent, nowSeconds || 0);
}

function tickEvery(span) {
  return TICKS.find((tick) => span <= tick.upTo).every;
}

// renderTrack draws the round on one time axis: attacks on top, numbered as in
// the list below, the clients' answers per second in the middle, the response
// time underneath, and the cure shaded across all three.
// A narrower viewBox in the side by side view keeps the chart's text readable
// when the column is half as wide.
function renderTrack(article, round, nowSeconds, isCompare) {
  const width = isCompare ? TRACK.compareWidth : TRACK.width;
  const span = spanOf(round, nowSeconds);
  const plot = width - TRACK.left - TRACK.right;
  const x = (seconds) => TRACK.left + (seconds / span) * plot;
  const svg = svgElement('svg', { viewBox: '0 0 ' + width + ' ' + TRACK.height, role: 'img' });

  svg.append(...laneLabels());
  svg.append(...healBand(round, x, span));
  svg.append(...clientBars(round, x, plot / span, width));
  svg.append(...latencyLines(round, x, width));
  svg.append(...timeAxis(span, x, isCompare));
  svg.append(...attackMarks(round, x));

  if (nowSeconds !== undefined) {
    svg.append(...nowMark(x(nowSeconds)));
  }

  svg.setAttribute('aria-label', trackSummary(round));
  field(article, 'track-title').textContent = MESSAGES.trackTitle;
  field(article, 'track').replaceChildren(svg);
  renderLegend(field(article, 'legend'));
}

function laneLabels() {
  return [
    svgElement('text', { class: 'lane-label', x: 0, y: TRACK.attackTop + 22 }, MESSAGES.trackAttacks),
    svgElement('text', { class: 'lane-label', x: 0, y: TRACK.clientsTop + 14 }, MESSAGES.trackClients),
    svgElement('text', { class: 'lane-label', x: 0, y: TRACK.clientsTop + 30 }, MESSAGES.trackClientsUnit),
    svgElement('text', { class: 'lane-label', x: 0, y: TRACK.latencyTop + 14 }, MESSAGES.trackLatency),
  ];
}

function healBand(round, x, span) {
  const heals = round.events.filter(isHeal).map((event) => event.t_ms / MS_PER_SECOND);

  if (heals.length === 0) {
    return [];
  }

  const start = heals[0];
  const end = heals.length > 1 ? heals[1] : span;
  const top = TRACK.attackTop;
  const height = TRACK.latencyTop + TRACK.latencyHeight - top;

  return [
    svgElement('rect', { class: 'heal-band', x: x(start), y: top, width: Math.max(1, x(end) - x(start)), height }),
    svgElement('text', { class: 'heal-label', x: x(start) + 6, y: TRACK.clientsTop - 4 }, MESSAGES.trackHeal),
  ];
}

function clientBars(round, x, secondWidth, width) {
  const peak = Math.max(1, ...round.seconds.map((second) => second.ok + second.fail + second.info));
  const scale = TRACK.clientsHeight / peak;
  const barWidth = Math.max(1, secondWidth - 0.4);
  const bottom = TRACK.clientsTop + TRACK.clientsHeight;
  const bars = [svgElement('line', { class: 'axis-line', x1: TRACK.left, x2: width - TRACK.right, y1: bottom, y2: bottom })];

  for (const second of round.seconds) {
    let top = bottom;

    for (const [key, className] of [['ok', 'bar-ok'], ['fail', 'bar-refused'], ['info', 'bar-unknown']]) {
      const height = second[key] * scale;

      if (height > 0) {
        top -= height;
        bars.push(svgElement('rect', { class: className, x: x(second.t), y: top, width: barWidth, height }));
      }
    }
  }

  return bars;
}

function latencyLines(round, x, width) {
  const peak = Math.max(1, ...round.seconds.map((second) => second.p99_ms));
  const bottom = TRACK.latencyTop + TRACK.latencyHeight;
  const y = (value) => bottom - (value / peak) * TRACK.latencyHeight;
  const pointsOf = (key) => round.seconds.map((second) => x(second.t + 0.5).toFixed(1) + ',' + y(second[key]).toFixed(1)).join(' ');

  return [
    svgElement('line', { class: 'axis-line', x1: TRACK.left, x2: width - TRACK.right, y1: bottom, y2: bottom }),
    svgElement('polyline', { class: 'latency-worst', points: pointsOf('p99_ms') }),
    svgElement('polyline', { class: 'latency-typical', points: pointsOf('p50_ms') }),
  ];
}

// The half-width view labels every other tick so the times don't collide.
function timeAxis(span, x, isCompare) {
  const every = tickEvery(span) * (isCompare ? 2 : 1);
  const marks = [];

  for (let seconds = 0; seconds <= span; seconds += every) {
    marks.push(svgElement('line', { class: 'grid-line', x1: x(seconds), x2: x(seconds), y1: TRACK.clientsTop, y2: TRACK.axisTop - 10 }));
    marks.push(svgElement('text', { class: 'axis-label', x: x(seconds), y: TRACK.axisTop + 8, 'text-anchor': 'middle' }, formatClock(seconds * MS_PER_SECOND)));
  }

  return marks;
}

function attackMarks(round, x) {
  return round.events.filter(isAttack).flatMap((event, index) => attackMark(x(event.t_ms / MS_PER_SECOND), index));
}

// attackMark alternates the markers above and below the lane's middle, since
// attacks often come in pairs a fraction of a second apart.
function attackMark(at, index) {
  const middle = TRACK.attackTop + TRACK.attackHeight / 2;
  const center = index % 2 === 0 ? middle - TRACK.markerStagger : middle + TRACK.markerStagger;

  return [
    svgElement('line', { class: 'attack-line', x1: at, x2: at, y1: center, y2: TRACK.latencyTop + TRACK.latencyHeight }),
    svgElement('circle', { class: 'attack-marker', cx: at, cy: center, r: TRACK.markerRadius }),
    svgElement('text', { class: 'attack-number', x: at, y: center + 4 }, String(index + 1)),
  ];
}

function nowMark(at) {
  return [
    svgElement('line', { class: 'now-line', x1: at, x2: at, y1: TRACK.attackTop, y2: TRACK.latencyTop + TRACK.latencyHeight }),
    svgElement('text', { class: 'now-label', x: at + 4, y: TRACK.axisTop + 8 }, MESSAGES.trackNow),
  ];
}

function trackSummary(round) {
  const totals = round.seconds.reduce(addSecond, { ok: 0, fail: 0, info: 0, p50: 0, p99: 0 });
  const last = round.seconds[round.seconds.length - 1] || { p50_ms: 0, p99_ms: 0 };

  return format(MESSAGES.trackSummary, {
    ok: formatNumber(totals.ok), refused: formatNumber(totals.fail), unknown: formatNumber(totals.info),
    p50: formatNumber(last.p50_ms), p99: formatNumber(last.p99_ms),
  });
}

function addSecond(totals, second) {
  return { ...totals, ok: totals.ok + second.ok, fail: totals.fail + second.fail, info: totals.info + second.info };
}

function renderLegend(list) {
  const entries = [
    [MESSAGES.legendDone, 'ok'], [MESSAGES.legendRefused, 'refused'], [MESSAGES.legendUnknown, 'unknown'],
    [MESSAGES.legendTypical, 'typical'], [MESSAGES.legendWorst, 'worst'],
  ];

  list.replaceChildren(...entries.map(([label, swatch]) => element('li', label, { 'data-swatch': swatch })));
}

function renderEvents(article, round, phase) {
  const list = field(article, 'events');
  const attacks = round.events.filter(isAttack);

  list.replaceChildren();
  list.setAttribute('aria-label', MESSAGES.eventsTitle);

  if (attacks.length === 0) {
    const text = phase === 'attack' ? MESSAGES.noAttacksYet : MESSAGES.noAttacks;
    list.append(element('li', text, { class: 'is-empty' }));

    return;
  }

  attacks.slice(0, MAX_LISTED_ATTACKS).forEach((event, index) => list.append(eventItem(event, index)));

  const hidden = attacks.length - MAX_LISTED_ATTACKS;
  if (hidden > 0) {
    list.append(element('li', format(MESSAGES.moreAttacks, { count: formatNumber(hidden) }), { class: 'is-empty' }));
  }
}

function eventItem(event, index) {
  const item = element('li');
  const what = element('span');

  what.append(element('span', MESSAGES.families[event.family] || event.family, { class: 'event-family' }));
  what.append(event.text);

  item.append(element('span', String(index + 1), { class: 'event-number', 'aria-hidden': 'true' }));
  item.append(element('time', formatClock(event.t_ms), { class: 'event-time' }));
  item.append(what);

  return item;
}

// renderMoney shows the auditor's live reading; a replay has none, so the
// section steps aside and the guarantees take the whole row.
function renderMoney(article, context) {
  const list = field(article, 'money');

  field(article, 'money-section').hidden = !context.isLive;
  article.classList.toggle('is-replay', !context.isLive);

  field(article, 'money-title').textContent = MESSAGES.moneyTitle;
  field(article, 'money-detail').textContent = context.isLive ? MESSAGES.moneyLiveDetail : MESSAGES.moneyReplayDetail;
  list.replaceChildren();

  for (const check of context.liveChecks) {
    const row = element('div', undefined, { class: 'money-row', 'data-state': check.state });
    row.append(element('dt', check.label));
    row.append(element('dd', check.detail || MESSAGES.guaranteeState[check.state] || check.state));
    list.append(row);
  }
}

// guaranteeStateOf prefers what the checker said at the end of the round, and
// shows the auditor's live reading for the guarantees it follows meanwhile.
function guaranteeStateOf(guarantee, finalChecks, liveChecks) {
  const final = finalChecks.find((check) => check.id === guarantee.id);
  if (final) {
    return { state: final.status, violations: final.violations || 0 };
  }

  const live = liveChecks.find((check) => check.id === guarantee.id);

  const isLiveOk = guarantee.isLive && live && live.state === 'ok';
  if (isLiveOk) {
    return { state: 'liveOk', violations: 0 };
  }

  const isLiveBroken = guarantee.isLive && live && live.state === 'violated';
  if (isLiveBroken) {
    return { state: 'violated', violations: 0 };
  }

  return { state: 'unknown', violations: 0 };
}

function renderGuarantees(article, round, context) {
  const list = field(article, 'guarantees');
  const finalChecks = round.isOver ? round.checks || [] : [];

  field(article, 'guarantees-title').textContent = MESSAGES.guaranteesTitle;
  list.replaceChildren();

  for (const guarantee of MESSAGES.guarantees) {
    const { state, violations } = guaranteeStateOf(guarantee, finalChecks, context.liveChecks);
    const item = element('li', undefined, { class: 'guarantee', 'data-state': state });
    const label = violations > 0 ? format(MESSAGES.violations, { count: formatNumber(violations) }) : MESSAGES.guaranteeState[state] || state;

    item.append(element('span', guarantee.id, { class: 'guarantee-id' }));
    item.append(element('span', guarantee.name, { class: 'guarantee-name' }));
    item.append(element('span', guarantee.detail, { class: 'guarantee-detail' }));
    item.append(element('span', label, { class: 'guarantee-state' }));
    list.append(item);
  }
}

// renderRound fills one round article; context carries what the live mode
// knows besides the round: the auditor's checks and how far the clock is.
function renderRound(article, round, context) {
  const phase = phaseOf(round);

  renderHead(article, round, phase, context);
  renderPhases(article, phase);
  renderTrack(article, round, phase === 'verdict' ? undefined : context.nowSeconds, Boolean(context.isCompare));
  renderEvents(article, round, phase);
  renderMoney(article, context);
  renderGuarantees(article, round, context);
}

function newArticle() {
  return document.getElementById('round-template').content.firstElementChild.cloneNode(true);
}

function showMessage(title, detail) {
  const view = document.getElementById('rounds-view');
  const article = element('article', undefined, { class: 'round' });
  const head = element('header', undefined, { class: 'round-head' });

  head.append(element('h1', title, { class: 'headline' }));

  if (detail) {
    head.append(element('p', detail, { class: 'scenario' }));
  }

  article.append(head);
  view.replaceChildren(article);
}

function renderHistory(rounds) {
  const head = document.getElementById('history-head');
  const rows = document.getElementById('history-rows');
  const columns = [MESSAGES.historyRound, MESSAGES.historyScenario, MESSAGES.historyDuration, MESSAGES.historyOperations, MESSAGES.historyVerdict, MESSAGES.historyReport];

  document.getElementById('history-title').textContent = MESSAGES.historyTitle;
  head.replaceChildren(...columns.map((name) => element('th', name, { scope: 'col' })));
  rows.replaceChildren();

  if (rounds.length === 0) {
    const row = element('tr');
    row.append(element('td', MESSAGES.historyEmpty, { colspan: String(columns.length), class: 'history-empty' }));
    rows.append(row);

    return;
  }

  rounds.forEach((round) => rows.append(historyRow(round)));
}

function historyRow(round) {
  const row = element('tr');
  const link = element('td');

  link.append(element('a', MESSAGES.reportOf, { href: round.report_url }));

  row.append(element('td', round.round));
  row.append(element('td', round.scenario));
  row.append(element('td', formatDuration(round.seconds)));
  row.append(element('td', formatNumber(round.operations)));
  row.append(element('td', MESSAGES.verdict[round.verdict] || round.verdict || '', { class: 'verdict-cell', 'data-state': round.verdict || '' }));
  row.append(link);

  return row;
}

async function getJSON(url) {
  const response = await fetch(url, { cache: 'no-store' });

  if (!response.ok) {
    throw new Error(url + ' answered ' + response.status);
  }

  return response.json();
}

function setMode(text) {
  document.getElementById('mode').textContent = text;
}

// liveContext is what the live mode adds to a round: the auditor's checks and
// how long the round has been running.
function liveContext(round, status) {
  const startedAt = Date.parse(round.started_at);
  const elapsedSeconds = Number.isNaN(startedAt) ? 0 : (Date.now() - startedAt) / MS_PER_SECOND;
  const nowSeconds = round.duration_seconds ? Math.min(elapsedSeconds, spanOf(round, 0)) : undefined;

  return { isLive: true, liveChecks: status.live.checks || [], elapsedSeconds: Math.min(elapsedSeconds, round.duration_seconds || elapsedSeconds), nowSeconds };
}

// finalChecksOf is the checker's verdict of the round on screen, once the
// status says it is the last one that finished.
function finalChecksOf(round, status) {
  const last = status.last_round;
  const isThisRound = Boolean(last) && last.round === round.round;

  return isThisRound ? last.invariants : round.checks || [];
}

let liveArticle;

async function refreshLive() {
  try {
    const [live, status] = await Promise.all([getJSON('api/live'), getJSON('api/status')]);

    renderHistory(live.rounds || []);

    if (!live.current) {
      showMessage(MESSAGES.waitingRound, MESSAGES.waitingRoundDetail);
      liveArticle = undefined;

      return;
    }

    if (!liveArticle) {
      liveArticle = newArticle();
      document.getElementById('rounds-view').replaceChildren(liveArticle);
    }

    const round = { ...live.current, checks: finalChecksOf(live.current, status), isOver: true };
    renderRound(liveArticle, round, liveContext(round, status));
  } catch (error) {
    setMode(MESSAGES.lostContact);
  }
}

// playReplay plays an exported round at the chosen speed.
async function playReplay(url) {
  const round = await getJSON(url);
  const article = newArticle();
  const total = round.seconds.length * MS_PER_SECOND;
  const play = field(article, 'play');
  const speed = field(article, 'speed');
  const player = { position: 0, isPlaying: true, last: performance.now() };

  setMode(MESSAGES.modeReplay);
  document.getElementById('history').hidden = true;
  document.getElementById('rounds-view').replaceChildren(article);
  field(article, 'player').hidden = false;
  field(article, 'speed-label').textContent = MESSAGES.speed;
  play.textContent = MESSAGES.pause;

  function togglePlay() {
    player.isPlaying = !player.isPlaying;
    play.textContent = player.isPlaying ? MESSAGES.pause : MESSAGES.resume;
  }

  function frame(now) {
    if (player.isPlaying) {
      player.position = Math.min(total, player.position + (now - player.last) * Number(speed.value));
    }

    player.last = now;

    const seen = roundUpTo(round, player.position);
    const context = { isLive: false, liveChecks: [], elapsedSeconds: player.position / MS_PER_SECOND, nowSeconds: player.position / MS_PER_SECOND };
    renderRound(article, seen, context);
    field(article, 'clock').textContent = format(MESSAGES.clockOf, { position: formatClock(player.position), total: formatClock(total) });

    if (player.position < total) {
      setTimeout(requestNextFrame, FRAME_INTERVAL_MS);
    }
  }

  function requestNextFrame() {
    requestAnimationFrame(frame);
  }

  play.addEventListener('click', togglePlay);
  requestAnimationFrame(frame);
}

// compareRounds puts two finished rounds side by side: usually the same
// scenario with and without a defense.
async function compareRounds(ids) {
  const urls = ids.map((id) => 'api/replay/' + encodeURIComponent(id));
  const rounds = await Promise.all(urls.map(getJSON));
  const view = document.getElementById('rounds-view');

  setMode(MESSAGES.modeCompare);
  view.classList.add('is-compare');
  view.replaceChildren(...rounds.map(compareArticle));
}

function compareArticle(round) {
  const article = newArticle();
  const context = { isLive: false, isCompare: true, liveChecks: [], elapsedSeconds: round.duration_seconds || 0 };

  renderRound(article, { ...round, isOver: true }, context);

  return article;
}

function showReplayError(file) {
  showMessage(format(MESSAGES.replayFailed, { file }));
}

function showCompareError(rounds) {
  showMessage(format(MESSAGES.compareFailed, { rounds }));
}

function start() {
  const params = new URLSearchParams(window.location.search);
  const replayFile = params.get('replay');
  const compare = params.get('compare');

  document.getElementById('lab-notice').textContent = MESSAGES.labBar;

  if (replayFile) {
    playReplay(replayFile).catch(() => showReplayError(replayFile));

    return;
  }

  if (compare) {
    document.getElementById('history').hidden = true;
    compareRounds(compare.split(',').slice(0, COMPARED_ROUNDS)).catch(() => showCompareError(compare));

    return;
  }

  setMode(MESSAGES.modeLive);
  refreshLive();
  setInterval(refreshLive, REFRESH_MS);
}

start();
