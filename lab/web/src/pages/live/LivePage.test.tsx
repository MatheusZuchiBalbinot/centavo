import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { appRoutes } from '../../App';
import type { RoundId } from '../../api/ids';
import { API_ROUTES, replayPath } from '../../api/routes';
import { answerFetch } from '../../test/fetch';

const quietCounters = {
  kills: 0,
  failpoint_fires: 0,
  network_episodes: 0,
  database_faults: 0,
  freezes: 0,
  failovers: 0,
  clock_skews: 0,
  partner_lies: 0,
  resolved: 0,
};

const summary = { scenario: 'kill-after-commit', seconds: 180, operations: 900, fails: 0, counters: quietCounters };

const rounds = {
  rounds: [
    { ...summary, round: 'r0012', verdict: 'inconclusive' },
    { ...summary, round: 'r0010', verdict: 'pass' },
  ],
};

const replay = {
  round: 'r0010',
  scenario: 'kill-after-commit',
  seed: '0x5eed',
  seconds: [{ t: 0, ok: 20, fail: 0, info: 0, p50_ms: 8, p99_ms: 12 }],
  events: [{ t_ms: 500, family: 'process', kind: 'kill', target: 'bank-api', text: 'x' }],
  counters: quietCounters,
  description: '',
  duration_seconds: 180,
  defenses: 'robust',
  verdict: 'pass',
  checks: [],
};

afterEach(() => {
  window.localStorage.clear();
  vi.restoreAllMocks();
});

describe('the live page', () => {
  it('replays the newest settled round in Presentation mode while the lab is free, and leaves for it', async () => {
    window.localStorage.setItem('centavo.viewMode', '"presentation"');
    answerFetch({
      [API_ROUTES.lab]: { status: 200, body: { status: 'idle' } },
      [API_ROUTES.rounds]: { status: 200, body: rounds },
      [replayPath('r0010' as RoundId)]: { status: 200, body: replay },
    });
    const user = userEvent.setup();
    const router = createMemoryRouter(appRoutes, { initialEntries: ['/live'] });

    render(<RouterProvider router={router} />);

    const arena = await screen.findByRole('dialog');

    expect(within(arena).getByText('Vitrine · rodada r0010, em replay')).toBeInTheDocument();
    expect(within(arena).getByRole('button', { name: '30×' })).toHaveAttribute('aria-pressed', 'true');

    await user.click(within(arena).getByRole('button', { name: 'Fechar' }));

    expect(router.state.location.pathname).toBe('/rounds/r0010');
    expect(router.state.location.search).toBe('?arena=0');
  });

  it('shows no showcase outside Presentation mode', async () => {
    answerFetch({ [API_ROUTES.lab]: { status: 200, body: { status: 'idle' } } });

    render(<RouterProvider router={createMemoryRouter(appRoutes, { initialEntries: ['/live'] })} />);

    expect(await screen.findByText('Nenhuma rodada ao vivo agora.')).toBeInTheDocument();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
});
