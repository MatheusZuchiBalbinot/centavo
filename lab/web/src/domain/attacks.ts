import type { Attack } from '../api/attacks';
import type { AttackId } from '../api/ids';
import type { ChaosCounters } from '../api/rounds';
import { format } from '../i18n/format';
import { messages } from '../i18n/pt-BR';

type AttackCopy = {
  readonly name: string;
  readonly detail: string;
  readonly family: string;
  readonly expect: string;
};

// The attack that draws from every other family; a scenario is named after
// the specific family it belongs to, not after this one.
const EVERYTHING = 'everything';

const knownAttacks: Readonly<Record<string, AttackCopy>> = messages.attacks;

// attackCopy is how the panel describes an attack; one the panel does not
// know yet is shown by its id.
export function attackCopy(id: AttackId): AttackCopy {
  return knownAttacks[id] ?? { name: id, ...messages.unknownAttack };
}

// attackOfScenario finds the family a scenario belongs to.
export function attackOfScenario(attacks: readonly Attack[], scenario: string): Attack | null {
  return attacks.find(isFamilyOf(scenario)) ?? null;
}

function isFamilyOf(scenario: string): (attack: Attack) => boolean {
  return (attack) => attack.id !== EVERYTHING && attack.scenarios.includes(scenario);
}

// totalAttacks adds up every fault the nemesis injected in a round.
export function totalAttacks(counters: ChaosCounters): number {
  return (
    counters.kills +
    counters.failpointFires +
    counters.networkEpisodes +
    counters.databaseFaults +
    counters.freezes +
    counters.failovers +
    counters.clockSkews +
    counters.partnerLies
  );
}

export function attacksText(count: number): string {
  if (count === 1) {
    return messages.attacksCount.one;
  }

  return format(messages.attacksCount.many, { count: count.toLocaleString('pt-BR') });
}
