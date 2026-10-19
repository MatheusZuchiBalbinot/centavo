import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import { appRoutes } from '../../App';
import { API_ROUTES } from '../../api/routes';
import { expectNoAxeViolations } from '../../test/axe';
import { answerFetch } from '../../test/fetch';
import type { FetchAnswers } from '../../test/fetch';

const menu = {
  attacks: [
    { id: 'workers-down', scenarios: ['kill-after-commit'] },
    { id: 'clock', scenarios: ['clock-skew'] },
  ],
  durations_seconds: [180, 300, 600],
};

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

const rounds = {
  rounds: [
    {
      round: 'r0003',
      scenario: 'kill-after-commit',
      verdict: 'pass',
      seconds: 300,
      operations: 5840,
      fails: 3,
      counters: { ...quietCounters, kills: 9 },
    },
    {
      round: 'r0002',
      scenario: 'zombie-reconciler',
      verdict: 'fail',
      seconds: 900,
      operations: 9112,
      fails: 0,
      counters: { ...quietCounters, freezes: 13 },
    },
    {
      round: 'r0001',
      scenario: 'smoke',
      verdict: 'pass',
      seconds: 180,
      operations: 3631,
      fails: 0,
      counters: quietCounters,
    },
  ],
};

const running = {
  status: 'running',
  attack: 'clock',
  scenario: 'clock-skew',
  duration_seconds: 300,
  started_at: '2026-10-19T11:35:33Z',
};

function homeAnswers(lab: unknown): FetchAnswers {
  return {
    [API_ROUTES.lab]: { status: 200, body: lab },
    [API_ROUTES.attacks]: { status: 200, body: menu },
    [API_ROUTES.rounds]: { status: 200, body: rounds },
  };
}

function renderHome(): ReturnType<typeof render> {
  const router = createMemoryRouter(appRoutes, { initialEntries: ['/'] });

  return render(<RouterProvider router={router} />);
}

function postedBodies(): unknown[] {
  const calls = vi.mocked(globalThis.fetch).mock.calls;

  return calls.filter(isPost).map(bodyOf);
}

function isPost(call: Parameters<typeof fetch>): boolean {
  return call[1]?.method === 'POST';
}

function bodyOf(call: Parameters<typeof fetch>): unknown {
  const body = call[1]?.body;

  return typeof body === 'string' ? JSON.parse(body) : null;
}

describe('the home page', () => {
  it('starts the attack and the duration that were chosen', async () => {
    answerFetch({ ...homeAnswers({ status: 'idle' }), [`POST ${API_ROUTES.rounds}`]: { status: 202, body: running } });
    const user = userEvent.setup();

    renderHome();

    await user.click(await screen.findByRole('radio', { name: /Relógio adiantado/ }));
    await user.click(screen.getByRole('radio', { name: '10 min' }));
    await user.click(screen.getByRole('button', { name: 'Começar ataque' }));

    await waitFor(() => {
      expect(postedBodies()).toEqual([{ attack: 'clock', duration_seconds: 600 }]);
    });
    expect(await screen.findByText('Rodada começou: Relógio adiantado e worker zumbi, 10 min.')).toBeInTheDocument();
  });

  it('opens on the first attack and five minutes, and says what to expect', async () => {
    answerFetch(homeAnswers({ status: 'idle' }));

    renderHome();

    expect(await screen.findByRole('radio', { name: /Derrubar os workers/ })).toBeChecked();
    expect(screen.getByRole('radio', { name: '5 min' })).toBeChecked();
    expect(
      screen.getByText('O outro worker retoma cada transferência parada, e nenhuma é enviada duas vezes.'),
    ).toBeInTheDocument();
  });

  it('locks the menu while a round runs', async () => {
    answerFetch(homeAnswers(running));

    renderHome();

    expect(await screen.findByText(/Uma rodada já está em andamento/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Começar ataque' })).toBeDisabled();
    expect(screen.getByRole('radio', { name: /Relógio adiantado/ })).toBeDisabled();
  });

  it('says why the lab refused, in an urgent notice', async () => {
    const refusal = { error: { code: 'rate_limited', message: 'too many' } };
    answerFetch({ ...homeAnswers({ status: 'idle' }), [`POST ${API_ROUTES.rounds}`]: { status: 429, body: refusal } });
    const user = userEvent.setup();

    renderHome();
    await user.click(await screen.findByRole('button', { name: 'Começar ataque' }));

    const alert = await screen.findByRole('alert');
    expect(within(alert).getByText(/Muitas rodadas começaram na última hora/)).toBeInTheDocument();
  });

  it('tells each recent round in one sentence and passes axe', async () => {
    answerFetch(homeAnswers({ status: 'idle' }));

    const { container } = renderHome();

    expect(await screen.findByText('Passou: 9 ataques, nenhuma garantia quebrada.')).toBeInTheDocument();
    expect(screen.getByText('Quebrou depois de 13 ataques.')).toBeInTheDocument();
    expect(screen.getByText('Passou sem ataques: tudo conferido.')).toBeInTheDocument();
    expect(screen.getByText('18.583')).toBeInTheDocument();
    await expectNoAxeViolations(container);
  });
});
