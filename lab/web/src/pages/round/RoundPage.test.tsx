import { act, render, screen } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { appRoutes } from '../../App';
import type { RoundId } from '../../api/ids';
import { API_ROUTES, roundEventsPath } from '../../api/routes';
import { expectNoAxeViolations } from '../../test/axe';
import { FakeEventSource, installFakeEventSource } from '../../test/eventSource';
import { answerFetch } from '../../test/fetch';

const counters = {
  kills: 1,
  failpoint_fires: 0,
  network_episodes: 0,
  database_faults: 0,
  freezes: 0,
  failovers: 0,
  clock_skews: 0,
  partner_lies: 0,
  resolved: 0,
};

const snapshot = {
  round: 'r0007',
  scenario: 'kill-after-commit',
  seed: '0x5eed',
  seconds: [
    { t: 0, ok: 20, fail: 0, info: 0, p50_ms: 8, p99_ms: 12 },
    { t: 1, ok: 18, fail: 1, info: 1, p50_ms: 9, p99_ms: 30 },
  ],
  events: [{ t_ms: 1500, family: 'process', kind: 'kill', target: 'bank-api', text: 'processo morto: bank-api' }],
  counters,
  description: 'Mata a API depois de aplicar a escrita e antes de responder.',
  duration_seconds: 180,
  started_at: new Date().toISOString(),
  defenses: 'robust',
  built_at: new Date().toISOString(),
};

const running = {
  status: 'running',
  round: 'r0007',
  attack: 'workers-down',
  scenario: 'kill-after-commit',
  duration_seconds: 180,
  started_at: snapshot.started_at,
};

const allHeld = ['I1', 'I2', 'I3', 'I4', 'I5', 'I6', 'I7', 'I8'].map((id) => ({ id, status: 'ok', violations: 0 }));

function renderRound(): ReturnType<typeof render> {
  const router = createMemoryRouter(appRoutes, { initialEntries: ['/rounds/r0007'] });

  return render(<RouterProvider router={router} />);
}

function stream(): FakeEventSource {
  const source = FakeEventSource.last;

  if (source === null) {
    throw new Error('the page opened no stream');
  }

  return source;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('the round page', () => {
  it('follows the attack live and offers to stop the round the lab runs', async () => {
    installFakeEventSource();
    answerFetch({ [API_ROUTES.lab]: { status: 200, body: running } });

    const { container } = renderRound();

    expect(screen.getByText('Conectando à rodada…')).toBeInTheDocument();
    expect(stream().url).toBe(roundEventsPath('r0007' as RoundId));

    act(() => {
      stream().send('snapshot', snapshot);
      stream().send('auditor', {
        at: new Date().toISOString(),
        sum_cents: 0,
        min_customer_balance_cents: 500,
        unavailable: false,
      });
    });

    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(
      /O banco está sob ataque há .*, e nenhum centavo sumiu\./,
    );
    expect(screen.getByText('R$ 0,00')).toBeInTheDocument();
    expect(screen.getByText('Derrubei o bank-api.')).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: 'Parar rodada' })).toBeInTheDocument();
    await expectNoAxeViolations(container);
  });

  it('shows the verdict next to what was expected, and closes the stream', () => {
    installFakeEventSource();
    answerFetch({ [API_ROUTES.lab]: { status: 200, body: { status: 'idle' } } });

    renderRound();

    act(() => {
      stream().send('snapshot', snapshot);
      stream().send('finished', { ...snapshot, verdict: 'pass', checks: allHeld });
    });

    expect(
      screen.getByRole('heading', { level: 1, name: 'Passou: um ataque, nenhum centavo perdido.' }),
    ).toBeInTheDocument();
    expect(screen.getByText('Bateu com o esperado.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Parar rodada' })).not.toBeInTheDocument();
    expect(stream().isClosed).toBe(true);
  });

  it('turns a broken control round into the expected outcome', () => {
    installFakeEventSource();
    answerFetch({ [API_ROUTES.lab]: { status: 200, body: { status: 'idle' } } });

    renderRound();

    const broken = allHeld.map((check) => (check.id === 'I1' ? { ...check, status: 'failed', violations: 2 } : check));

    act(() => {
      stream().send('finished', { ...snapshot, defenses: 'robust,-fencing', verdict: 'fail', checks: broken });
    });

    expect(
      screen.getByRole('heading', { level: 1, name: 'Quebrou: nenhum dinheiro some nem aparece.' }),
    ).toBeInTheDocument();
    expect(screen.getByText('Bateu com o esperado: a defesa faz falta, e a rodada prova isso.')).toBeInTheDocument();
    expect(screen.getByText('2 violação(ões)')).toBeInTheDocument();
  });
});
