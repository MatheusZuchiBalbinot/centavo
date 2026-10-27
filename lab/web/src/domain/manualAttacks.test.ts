import { describe, expect, it } from 'vitest';

import { manualActionsOf, manualAttackOf } from './manualAttacks';

describe('manual attacks', () => {
	it('offers what the lab takes: processes are killed or frozen, links cut or slowed', () => {
		expect(manualActionsOf({ kind: 'node', id: 'worker1' })).toEqual(['kill', 'freeze']);
		expect(manualActionsOf({ kind: 'link', id: 'api_pg' })).toEqual(['cut', 'slow']);
		expect(manualAttackOf({ kind: 'node', id: 'worker1' }, 'kill')).toEqual({ action: 'kill', target: 'bank-worker-1' });
		expect(manualAttackOf({ kind: 'link', id: 'worker2_partner' }, 'slow')).toEqual({ action: 'slow', target: 'worker2_partner' });
	});

	it('leaves the lab’s own view of the bank alone', () => {
		expect(manualActionsOf({ kind: 'node', id: 'auditor' })).toEqual([]);
		expect(manualActionsOf({ kind: 'node', id: 'replica' })).toEqual([]);
		expect(manualActionsOf({ kind: 'node', id: 'clients' })).toEqual([]);
		expect(manualActionsOf({ kind: 'link', id: 'auditor_pg' })).toEqual([]);
		expect(manualAttackOf({ kind: 'node', id: 'api' }, 'cut')).toBeNull();
	});
});
