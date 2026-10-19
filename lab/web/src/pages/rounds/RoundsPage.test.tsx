import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import { appRoutes } from '../../App';
import { API_ROUTES } from '../../api/routes';
import { expectNoAxeViolations } from '../../test/axe';
import { answerFetch } from '../../test/fetch';

const counters = {
  kills: 2,
  failpoint_fires: 0,
  network_episodes: 0,
  database_faults: 0,
  freezes: 0,
  failovers: 0,
  clock_skews: 0,
  partner_lies: 0,
  resolved: 0,
};

function aRound(round: string, verdict: string): Record<string, unknown> {
  return { round, scenario: 'kill-after-commit', verdict, seconds: 180, operations: 3600, fails: 0, counters };
}

describe('the rounds page', () => {
  it('lists the rounds, compares two of them and asks for the next page with the cursor', async () => {
    const page = { rounds: [aRound('r0003', 'pass'), aRound('r0002', 'fail')], next_cursor: 'r0002' };
    answerFetch({
      [API_ROUTES.lab]: { status: 200, body: { status: 'idle' } },
      [API_ROUTES.rounds]: { status: 200, body: page },
    });
    const user = userEvent.setup();
    const router = createMemoryRouter(appRoutes, { initialEntries: ['/rounds'] });

    const { container } = render(<RouterProvider router={router} />);

    await user.click(await screen.findByRole('checkbox', { name: 'Marcar a r0003 para comparar' }));
    await user.click(screen.getByRole('checkbox', { name: 'Marcar a r0002 para comparar' }));

    expect(screen.getByRole('link', { name: 'Ver as duas lado a lado' })).toHaveAttribute(
      'href',
      '/compare/r0003/r0002',
    );
    await expectNoAxeViolations(container);

    await user.click(screen.getByRole('button', { name: 'Carregar mais rodadas' }));

    await waitFor(() => {
      const urls = vi.mocked(globalThis.fetch).mock.calls.map(urlOf);

      expect(urls).toContain(`${API_ROUTES.rounds}?limit=25&cursor=r0002`);
    });
  });
});

function urlOf(call: Parameters<typeof fetch>): string {
  const [input] = call;

  return typeof input === 'string' ? input : '';
}
