import { describe, expect, it } from 'vitest';

import { aReading, aReplay, aSecond, anEvent } from '../test/fixtures';
import { feedOf } from './feed';
import { guaranteeView } from './guarantees';
import { expectationOf, headlineOf } from './headline';
import { phaseOf } from './phase';
import { initialRoundState, mergeSeconds, reduceRound } from './roundState';
import { bucketsOf, clusterMarkers, spanOf } from './timeline';

describe('reduceRound', () => {
  it('follows a round from its snapshot to its verdict, counting what came after the snapshot', () => {
    const snapshot = reduceRound(initialRoundState, { type: 'snapshot', replay: aReplay() });
    const withEvent = reduceRound(snapshot, { type: 'event', event: anEvent(3_000, 'freeze') });
    const withSeconds = reduceRound(withEvent, { type: 'seconds', seconds: [aSecond(1, 25), aSecond(2)] });
    const withReading = reduceRound(withSeconds, { type: 'auditor', reading: aReading() });
    const finished = reduceRound(withReading, { type: 'finished', replay: aReplay({ verdict: 'pass' }) });

    expect(withSeconds.kind === 'following' && withSeconds.replay.seconds.map((second) => second.ok)).toEqual([
      20, 25, 20,
    ]);
    expect(withSeconds.kind === 'following' && withSeconds.newEvents).toBe(1);
    expect(finished.kind).toBe('finished');
    expect(finished.kind === 'finished' && finished.reading).toEqual(aReading());
  });

  it('starts over on a new snapshot, which is how a reconnection catches up', () => {
    const first = reduceRound(initialRoundState, { type: 'snapshot', replay: aReplay() });
    const withEvent = reduceRound(first, { type: 'event', event: anEvent(3_000, 'freeze') });
    const again = reduceRound(withEvent, { type: 'snapshot', replay: aReplay({ events: [] }) });

    expect(again.kind === 'following' && again.newEvents).toBe(0);
    expect(again.kind === 'following' && again.replay.events).toEqual([]);
  });

  it('ignores updates before the snapshot', () => {
    expect(reduceRound(initialRoundState, { type: 'event', event: anEvent(1, 'kill') })).toBe(initialRoundState);
  });
});

describe('mergeSeconds', () => {
  it('keeps the known seconds when the update is empty', () => {
    const known = [aSecond(0)];

    expect(mergeSeconds(known, [])).toBe(known);
  });
});

describe('phaseOf', () => {
  it('reads the phase from the cure events and the end of the stream', () => {
    expect(phaseOf(aReplay(), false)).toBe('attack');
    expect(phaseOf(aReplay({ events: [anEvent(1, 'heal_start')] }), false)).toBe('heal');
    expect(phaseOf(aReplay({ events: [anEvent(1, 'heal_start'), anEvent(2, 'heal_complete')] }), false)).toBe('check');
    expect(phaseOf(aReplay(), true)).toBe('verdict');
  });
});

describe('guaranteeView', () => {
  it('prefers the checker, follows the auditor for the money and waits for the rest', () => {
    const checks = [{ id: 'I2', status: 'failed' as const, violations: 3 }];

    expect(guaranteeView('I2', checks, null)).toEqual({ kind: 'final', status: 'failed', violations: 3 });
    expect(guaranteeView('I1', [], aReading(0))).toEqual({ kind: 'live', isHolding: true });
    expect(guaranteeView('I1', [], aReading(3_750))).toEqual({ kind: 'live', isHolding: false });
    expect(guaranteeView('I4', [], aReading(0, -1))).toEqual({ kind: 'live', isHolding: false });
    expect(guaranteeView('I5', [], aReading())).toEqual({ kind: 'later' });
  });
});

describe('feedOf', () => {
  it('numbers the attacks in order and lists the newest first', () => {
    const events = [anEvent(1, 'kill'), anEvent(2, 'freeze', 'bank-worker-2'), anEvent(3, 'heal_start')];
    const feed = feedOf(events);

    expect(feed.map((item) => item.number)).toEqual([null, 2, 1]);
    expect(feed[1]?.what).toBe('Congelei o bank-worker-2 por 45s.');
    expect(feed[2]?.expected).toMatch(/^Esperado: /);
  });
});

describe('headlineOf', () => {
  it('says what happens now, and how the round ended', () => {
    const attacked = headlineOf({ replay: aReplay(), phase: 'attack', elapsedSeconds: 192, reading: aReading() });
    const passed = headlineOf({
      replay: aReplay({ verdict: 'pass' }),
      phase: 'verdict',
      elapsedSeconds: 0,
      reading: null,
    });
    const broken = aReplay({ verdict: 'fail', checks: [{ id: 'I1', status: 'failed', violations: 2 }] });

    expect(attacked).toBe('O banco está sob ataque há 3 min 12 s, e nenhum centavo sumiu.');
    expect(passed).toBe('Passou: um ataque, nenhum centavo perdido.');
    expect(headlineOf({ replay: broken, phase: 'verdict', elapsedSeconds: 0, reading: null })).toBe(
      'Quebrou: nenhum dinheiro some nem aparece.',
    );
  });
});

describe('expectationOf', () => {
  it('expects a robust bank to pass and a control to break', () => {
    expect(expectationOf('robust')).toMatch(/devem se manter/);
    expect(expectationOf('robust,-fencing')).toBe(
      'Sem fencing, alguma garantia deve quebrar: é o controle que prova que a defesa funciona.',
    );
    expect(expectationOf('')).toMatch(/ainda não foi lido/);
  });
});

describe('timeline', () => {
  it('covers the planned attack and buckets long rounds into few bars', () => {
    const seconds = Array.from({ length: 1_000 }, (_, t) => aSecond(t));

    expect(spanOf({ durationSeconds: 300, seconds: [aSecond(0)], lastEventSeconds: 0, nowSeconds: null })).toBe(300);
    expect(bucketsOf(seconds, 1_000).length).toBeLessThanOrEqual(450);
    expect(bucketsOf(seconds, 1_000)[0]?.ok).toBe(20);
  });
});

describe('clusterMarkers', () => {
  it('merges attacks too close to read apart and keeps the rest', () => {
    const markers = clusterMarkers([100, 105, 108, 200, 300, 302], 22);

    expect(markers.map((marker) => marker.label)).toEqual(['1–3', '4', '5–6']);
    expect(clusterMarkers([], 22)).toEqual([]);
  });
});
