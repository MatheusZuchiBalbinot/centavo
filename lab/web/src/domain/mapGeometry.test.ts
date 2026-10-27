import { describe, expect, it } from 'vitest';

import { curveOf, frameOf, pointOf } from './mapGeometry';
import { MAP_LINKS, MAP_TARGETS, targetKeyOf } from './topology';

describe('the map geometry', () => {
	it('places nodes in pixels, and turns the map on a narrow screen', () => {
		const wide = frameOf(1000, 500);
		const narrow = frameOf(400, 900);

		expect(pointOf('postgres', wide)).toEqual({ x: 500, y: 250 });
		expect(pointOf('clients', wide)).toEqual({ x: 80, y: 250 });
		expect(narrow.isTurned).toBe(true);
		expect(pointOf('clients', narrow)).toEqual({ x: 200, y: 72 });
	});

	it('draws straight links straight and bends the webhooks over the map', () => {
		const frame = frameOf(1000, 500);
		const apiToDatabase = MAP_LINKS.find((link) => link.id === 'api_pg');
		const webhooks = MAP_LINKS.find((link) => link.id === 'partner_webhooks');

		if (apiToDatabase === undefined || webhooks === undefined) {
			throw new Error('the map lost a link');
		}

		expect(curveOf(apiToDatabase, frame).middle).toEqual({ x: 390, y: 250 });
		expect(curveOf(webhooks, frame).middle.y).toBeLessThan(pointOf('auditor', frame).y);
	});

	it('lists the targets the way the map reads, for the Tab key', () => {
		const order = MAP_TARGETS.map(targetKeyOf);

		expect(order.slice(0, 5)).toEqual(['node:clients', 'link:client_api', 'node:api', 'link:api_pg', 'node:auditor']);
		expect(order.indexOf('node:worker1')).toBeLessThan(order.indexOf('link:worker1_partner'));
		expect(order.at(-1)).toBe('node:partner');
	});
});
