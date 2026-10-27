import { render, screen } from '@testing-library/react';
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

const replay = {
  round: 'r0012',
  scenario: 'clock-skew',
  seed: '0x5eed',
  seconds: [{ t: 0, ok: 20, fail: 0, info: 0, p50_ms: 8, p99_ms: 12 }],
  events: [],
  counters: quietCounters,
  description: '',
  duration_seconds: 180,
  defenses: 'robust',
  verdict: 'pass',
  checks: [],
};

function renderCompare(left: string, right: string): void {
  const router = createMemoryRouter(appRoutes, { initialEntries: [`/compare/${left}/${right}`] });

  render(<RouterProvider router={router} />);
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('the comparison page', () => {
  it('names only the round that does not exist', async () => {
    answerFetch({
      [API_ROUTES.lab]: { status: 200, body: { status: 'idle' } },
      [replayPath('r0012' as RoundId)]: { status: 200, body: replay },
    });

    renderCompare('r0012', 'r9999');

    expect(await screen.findByText('A rodada r9999 não existe.')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Escolher outras rodadas' })).toHaveAttribute('href', '/rounds');
  });

  it('says when neither round exists', async () => {
    answerFetch({ [API_ROUTES.lab]: { status: 200, body: { status: 'idle' } } });

    renderCompare('r9998', 'r9999');

    expect(await screen.findByText('As rodadas r9998 e r9999 não existem.')).toBeInTheDocument();
  });
});
