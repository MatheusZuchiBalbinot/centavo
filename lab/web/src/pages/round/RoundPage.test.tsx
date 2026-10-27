import { act, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { RoundId } from '../../api/ids';
import { API_ROUTES, replayPath, roundEventsPath } from '../../api/routes';
import { appRoutes } from '../../App';
import { formatClock } from '../../domain/duration';
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

function renderRound(entry = '/rounds/r0007'): ReturnType<typeof render> {
	const router = createMemoryRouter(appRoutes, { initialEntries: [entry] });

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

		expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(/O banco está sob ataque há .*, e nenhum centavo sumiu\./);
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

		expect(screen.getByRole('heading', { level: 1, name: 'Passou: um ataque, nenhum centavo perdido.' })).toBeInTheDocument();
		expect(screen.getByText('Bateu com o esperado.')).toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'Parar rodada' })).not.toBeInTheDocument();
		expect(stream().isClosed).toBe(true);
	});

	it('marks the attacks made by hand in the feed and on the chart', () => {
		installFakeEventSource();
		answerFetch({ [API_ROUTES.lab]: { status: 200, body: running } });

		renderRound();

		const manualKill = { t_ms: 1500, family: 'process', kind: 'kill', target: 'bank-worker-1', manual: true, text: 'processo morto: bank-worker-1' };

		act(() => {
			stream().send('snapshot', { ...snapshot, events: [manualKill] });
		});

		const feed = screen.getByRole('region', { name: 'O que está acontecendo' });

		expect(within(feed).getByText('O bank-worker-1 caiu.')).toBeInTheDocument();
		expect(within(feed).getByText('manual')).toBeInTheDocument();
		expect(screen.getByLabelText('Ataques manuais 1, 0:01: O bank-worker-1 caiu.')).toBeInTheDocument();
		expect(screen.getByText('ataque manual')).toBeInTheDocument();
	});

	it('turns a broken control round into the expected outcome', () => {
		installFakeEventSource();
		answerFetch({ [API_ROUTES.lab]: { status: 200, body: { status: 'idle' } } });

		renderRound();

		const broken = allHeld.map((check) => (check.id === 'I1' ? { ...check, status: 'failed', violations: 2 } : check));

		act(() => {
			stream().send('finished', { ...snapshot, defenses: 'robust,-fencing', verdict: 'fail', checks: broken });
		});

		expect(screen.getByRole('heading', { level: 1, name: 'Quebrou: nenhum dinheiro some nem aparece.' })).toBeInTheDocument();
		expect(screen.getByText('Bateu com o esperado: a defesa faz falta, e a rodada prova isso.')).toBeInTheDocument();
		expect(screen.getByText('2 violação(ões)')).toBeInTheDocument();
	});

	it('narrows the feed to the cure and back', async () => {
		installFakeEventSource();
		answerFetch({ [API_ROUTES.lab]: { status: 200, body: { status: 'idle' } } });
		const user = userEvent.setup();

		renderRound();

		const heal = { t_ms: 90_000, family: 'heal', kind: 'heal_start', text: 'cura começou' };

		act(() => {
			stream().send('snapshot', { ...snapshot, events: [...snapshot.events, heal] });
		});

		await user.click(screen.getByRole('button', { name: 'Cura · 1' }));

		expect(screen.queryByText('Derrubei o bank-api.')).not.toBeInTheDocument();
		expect(screen.getByText('Os ataques pararam: começou a cura.')).toBeInTheDocument();

		await user.click(screen.getByRole('button', { name: 'Tudo · 2' }));

		expect(screen.getByText('Derrubei o bank-api.')).toBeInTheDocument();
	});

	it('shows the technical details only in the technical mode, and remembers it', async () => {
		installFakeEventSource();
		window.localStorage.clear();
		answerFetch({ [API_ROUTES.lab]: { status: 200, body: { status: 'idle' } } });
		const user = userEvent.setup();

		renderRound();

		const fired = {
			t_ms: 4_000,
			family: 'failpoint',
			kind: 'failpoint_fired',
			target: 'bank-api',
			detail: 'api.after_commit',
			text: 'x',
		};

		act(() => {
			stream().send('finished', { ...snapshot, events: [fired], verdict: 'pass', checks: allHeld });
		});

		expect(screen.queryByText('api.after_commit')).not.toBeInTheDocument();
		expect(screen.queryByText('I1')).not.toBeInTheDocument();

		await user.click(screen.getByRole('button', { name: 'Técnico' }));

		expect(screen.getByText('api.after_commit')).toBeInTheDocument();
		expect(screen.getByText('I1')).toBeInTheDocument();
		expect(screen.getByText('semente 0x5eed')).toBeInTheDocument();
		expect(window.localStorage.getItem('centavo.viewMode')).toBe('"technical"');
	});

	it('opens the arena from its link, draws the attack on the map, and closes it', async () => {
		installFakeEventSource();
		answerFetch({ [API_ROUTES.lab]: { status: 200, body: running } });
		const user = userEvent.setup();

		renderRound('/rounds/r0007?arena=1');

		act(() => {
			stream().send('snapshot', snapshot);
		});

		const arena = screen.getByRole('dialog', { name: /O banco está sob ataque/ });

		expect(within(arena).getByRole('button', { name: /^API: derrubado há \d+ s$/ })).toBeInTheDocument();
		expect(within(arena).getByRole('group', { name: 'Mapa do sistema' })).toBeInTheDocument();
		await expectNoAxeViolations(arena);

		await user.click(within(arena).getByRole('button', { name: 'Fechar' }));

		expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

		await user.click(screen.getByRole('button', { name: 'Abrir a arena' }));

		expect(screen.getByRole('dialog')).toBeInTheDocument();
	});

	it('opens a process from the map, attacks it by hand, and lets Esc close the panel before the arena', async () => {
		installFakeEventSource();
		answerFetch({
			[API_ROUTES.lab]: { status: 200, body: running },
			[`POST ${API_ROUTES.manualAttacks}`]: { status: 202, body: { action: 'kill', target: 'bank-worker-1' } },
		});
		const user = userEvent.setup();

		renderRound('/rounds/r0007?arena=1');

		act(() => {
			stream().send('snapshot', snapshot);
		});

		const arena = screen.getByRole('dialog', { name: /O banco está sob ataque/ });
		const api = within(arena).getByRole('button', { name: /^API: derrubado/ });

		await user.click(api);

		expect(within(arena).getByRole('heading', { name: 'API' })).toHaveFocus();
		expect(within(within(arena).getByRole('region', { name: 'O que aconteceu aqui' })).getByText('Derrubei o bank-api.')).toBeInTheDocument();
		expect(await within(arena).findByRole('button', { name: 'Derrubar agora' })).toBeDisabled();

		await user.click(within(arena).getByRole('button', { name: 'Voltar ao resumo' }));
		await user.click(within(arena).getByRole('button', { name: /^Worker 1: no ar/ }));
		await user.click(within(arena).getByRole('button', { name: 'Derrubar agora' }));

		expect(await within(arena).findByRole('status')).toHaveTextContent('Enviado.');
		expect(fetch).toHaveBeenCalledWith(
			API_ROUTES.manualAttacks,
			expect.objectContaining({ method: 'POST', body: '{"action":"kill","target":"bank-worker-1"}' }),
		);
		await expectNoAxeViolations(arena);

		fireEvent(arena, new Event('cancel', { cancelable: true }));

		expect(within(arena).queryByRole('heading', { name: 'Worker 1' })).not.toBeInTheDocument();
		expect(within(arena).getByRole('button', { name: /^Worker 1/ })).toHaveFocus();

		fireEvent(arena, new Event('cancel', { cancelable: true }));

		expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
	});

	it('stamps the verdict on the arena when the round ends', () => {
		installFakeEventSource();
		answerFetch({ [API_ROUTES.lab]: { status: 200, body: { status: 'idle' } } });

		renderRound('/rounds/r0007?arena=1');

		act(() => {
			stream().send('finished', { ...snapshot, verdict: 'pass', checks: allHeld });
		});

		const arena = screen.getByRole('dialog');

		expect(within(arena).getByText('Passou')).toBeInTheDocument();
		expect(within(arena).getByText('um ataque · R$ 0,00 · 8 de 8 garantias')).toBeInTheDocument();
	});

	it('settles the bet made in the surprise draw and keeps it in the browser', () => {
		installFakeEventSource();
		window.localStorage.clear();
		answerFetch({ [API_ROUTES.lab]: { status: 200, body: { status: 'idle' } } });

		renderRound('/rounds/r0007?arena=1&surprise=1&bet=breaks');

		act(() => {
			stream().send('finished', { ...snapshot, verdict: 'pass', checks: allHeld });
		});

		expect(screen.getByText('Você apostou que quebraria. O banco aguentou.')).toBeInTheDocument();
		expect(window.localStorage.getItem('centavo.bets')).toBe('{"r0007":"breaks"}');
	});

	it('replays a finished round from the moment in its link, and keeps the moment on pause', async () => {
		installFakeEventSource();
		answerFetch({ [API_ROUTES.lab]: { status: 200, body: { status: 'idle' } } });
		const user = userEvent.setup();
		const router = createMemoryRouter(appRoutes, { initialEntries: ['/rounds/r0007?arena=1&t=0:01'] });

		render(<RouterProvider router={router} />);

		act(() => {
			// Three minutes of seconds, so the replay is still playing when the
			// test pauses it.
			const seconds = Array.from({ length: 180 }, (_, t) => ({ t, ok: 20, fail: 0, info: 0, p50_ms: 8, p99_ms: 12 }));

			stream().send('finished', { ...snapshot, seconds, verdict: 'pass', checks: allHeld });
		});

		const arena = screen.getByRole('dialog', { name: /./ });

		expect(within(arena).getByText('Rodada r0007 · replay')).toBeInTheDocument();
		expect(within(arena).getByRole('slider', { name: 'Momento da rodada' })).toBeInTheDocument();
		expect(within(arena).getByRole('button', { name: '10×' })).toHaveAttribute('aria-pressed', 'true');
		await expectNoAxeViolations(arena);

		await user.click(within(arena).getByRole('button', { name: 'Pausar' }));

		// How far the replay got before the click depends on the machine; the
		// link has to keep exactly where the player stopped.
		const stoppedAt = Number(within(arena).getByRole('slider', { name: 'Momento da rodada' }).getAttribute('value'));
		const moment = new URLSearchParams(router.state.location.search).get('t');

		expect(moment).toBe(formatClock(stoppedAt / 1_000));
		expect(stoppedAt).toBeGreaterThanOrEqual(1_000);
		expect(within(arena).getByRole('button', { name: 'Tocar' })).toBeInTheDocument();
	});

	it('offers the recorded round without the defense, side by side', async () => {
		installFakeEventSource();
		const control = {
			round: 'r0004',
			scenario: 'kill-after-commit',
			verdict: 'fail',
			seconds: 180,
			operations: 900,
			counters,
			defenses: 'robust,-fencing',
		};
		answerFetch({
			[API_ROUTES.lab]: { status: 200, body: { status: 'idle' } },
			[API_ROUTES.rounds]: { status: 200, body: { rounds: [control] } },
		});

		renderRound();

		act(() => {
			stream().send('finished', { ...snapshot, verdict: 'pass', checks: allHeld });
		});

		expect(await screen.findByRole('link', { name: 'E sem a defesa?' })).toHaveAttribute('href', '/compare/r0007/r0004');
	});

	it('closes only the stop confirmation when Esc is pressed on it inside the arena', async () => {
		installFakeEventSource();
		answerFetch({ [API_ROUTES.lab]: { status: 200, body: running } });
		const user = userEvent.setup();

		renderRound('/rounds/r0007?arena=1');

		act(() => {
			stream().send('snapshot', snapshot);
		});

		const arena = screen.getByRole('dialog', { name: /O banco está sob ataque/ });

		await user.click(await within(arena).findByRole('button', { name: 'Parar rodada' }));

		const confirmation = screen.getByRole('dialog', { name: 'Parar esta rodada?' });

		fireEvent(confirmation, new Event('cancel', { cancelable: true }));

		expect(screen.getByRole('dialog', { name: /O banco está sob ataque/ })).toBeInTheDocument();
	});

	it('says the round does not exist when the lab refuses its stream and has no such round', async () => {
		installFakeEventSource();
		answerFetch({ [API_ROUTES.lab]: { status: 200, body: { status: 'idle' } } });

		renderRound();

		act(() => {
			stream().refuse();
		});

		expect(await screen.findByRole('heading', { level: 1, name: 'A rodada r0007 não existe.' })).toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'Ver todas as rodadas' })).toHaveAttribute('href', '/rounds');
	});

	it('offers to try again when the lab does not answer, and opens a new stream', async () => {
		installFakeEventSource();
		const failure = { status: 500, body: { error: { code: 'internal', message: 'boom' } } };
		answerFetch({
			[API_ROUTES.lab]: { status: 200, body: { status: 'idle' } },
			[replayPath('r0007' as RoundId)]: failure,
		});
		const user = userEvent.setup();

		renderRound();

		const refused = stream();

		act(() => {
			refused.refuse();
		});

		expect(await screen.findByRole('heading', { level: 1, name: 'O laboratório não respondeu.' })).toBeInTheDocument();

		await user.click(screen.getByRole('button', { name: 'Tentar de novo' }));

		expect(stream()).not.toBe(refused);
		expect(screen.getByRole('heading', { level: 1, name: 'Conectando à rodada…' })).toBeInTheDocument();
	});
});
