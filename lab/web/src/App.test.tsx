import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { describe, expect, it } from 'vitest';

import { API_ROUTES } from './api/routes';
import { expectNoAxeViolations } from './test/axe';
import { answerFetch } from './test/fetch';
import { appRoutes } from './App';

function renderAt(path: string): ReturnType<typeof render> {
	const router = createMemoryRouter(appRoutes, { initialEntries: [path] });

	return render(<RouterProvider router={router} />);
}

describe('the panel shell', () => {
	it('opens with a skip link, one heading and the lab state', async () => {
		answerFetch({ [API_ROUTES.lab]: { status: 200, body: { status: 'idle' } } });

		const { container } = renderAt('/');

		expect(screen.getByRole('link', { name: 'Pular para o conteúdo' })).toHaveAttribute('href', '#main');
		expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1);
		expect(await screen.findByText('Laboratório livre')).toBeInTheDocument();
		expect(document.title).toContain('Centavo');
		await expectNoAxeViolations(container);
	});

	it('says the lab is offline when it does not answer', async () => {
		answerFetch({});

		renderAt('/');

		expect(await screen.findByText('Laboratório fora do ar')).toBeInTheDocument();
	});

	it('moves focus to the new page heading after a navigation', async () => {
		answerFetch({ [API_ROUTES.lab]: { status: 200, body: { status: 'idle' } } });
		const user = userEvent.setup();

		renderAt('/nowhere');
		await user.click(screen.getByRole('link', { name: 'Voltar para o início' }));

		expect(screen.getByRole('heading', { level: 1, name: 'Tente fazer o banco perder um centavo.' })).toHaveFocus();
	});
});
