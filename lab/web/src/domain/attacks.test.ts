import { describe, expect, it } from 'vitest';

import type { Attack } from '../api/attacks';
import type { AttackId } from '../api/ids';
import { attackCopy, attackOfScenario, attacksText, totalAttacks } from './attacks';

const clock = 'clock' as AttackId;
const everything = 'everything' as AttackId;
const menu: Attack[] = [
  { id: everything, scenarios: ['clock-skew', 'smoke'] },
  { id: clock, scenarios: ['clock-skew'] },
];

describe('attacks', () => {
  it('describes a known attack and falls back to the id for an unknown one', () => {
    expect(attackCopy(clock).name).toBe('Relógio adiantado e worker zumbi');
    expect(attackCopy('new-attack' as AttackId).name).toBe('new-attack');
  });

  it('names a scenario after its own family, never after the mix of all', () => {
    expect(attackOfScenario(menu, 'clock-skew')?.id).toBe('clock');
    expect(attackOfScenario(menu, 'smoke')).toBeNull();
  });

  it('adds up every kind of fault', () => {
    const counters = {
      kills: 1,
      failpointFires: 2,
      networkEpisodes: 3,
      databaseFaults: 4,
      freezes: 5,
      failovers: 6,
      clockSkews: 7,
      partnerLies: 8,
    };

    expect(totalAttacks(counters)).toBe(36);
    expect(attacksText(1)).toBe('um ataque');
    expect(attacksText(1200)).toBe('1.200 ataques');
  });
});
