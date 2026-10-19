import axe from 'axe-core';
import { expect } from 'vitest';

// expectNoAxeViolations runs axe on a rendered tree. jsdom has no layout, so
// color contrast is checked by hand and in the browser instead.
export async function expectNoAxeViolations(container: Element): Promise<void> {
  const results = await axe.run(container, { rules: { 'color-contrast': { enabled: false } } });
  const violations = results.violations.map(describeViolation);

  expect(violations).toEqual([]);
}

function describeViolation(violation: axe.Result): string {
  return `${violation.id}: ${violation.nodes.map(targetOf).join(', ')}`;
}

function targetOf(node: axe.NodeResult): string {
  return node.target.join(' ');
}
