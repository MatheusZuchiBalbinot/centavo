import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';

import { Glossed } from './Glossed';
import TooltipLayer from './TooltipLayer';

function renderWithLayer(content: React.ReactNode): void {
  render(
    <>
      {content}
      <TooltipLayer />
    </>,
  );
}

describe('the tooltip layer', () => {
  it('opens on focus, points the element at it and closes on Escape', async () => {
    const user = userEvent.setup();

    renderWithLayer(
      <button type="button" data-tooltip="Soma de todos os saldos.">
        ?
      </button>,
    );

    await user.tab();

    const tooltip = await screen.findByRole('tooltip');

    expect(tooltip).toHaveTextContent('Soma de todos os saldos.');
    expect(screen.getByRole('button', { name: '?' })).toHaveAttribute('aria-describedby', tooltip.id);

    await user.keyboard('{Escape}');

    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument();
  });

  it('explains a glossary term with the guarantees it protects', async () => {
    const user = userEvent.setup();

    renderWithLayer(
      <p>
        <Glossed text="Sem fencing, o zumbi grava." />
      </p>,
    );

    await user.hover(screen.getByRole('button', { name: 'fencing' }));
    await act(() => new Promise((resolve) => setTimeout(resolve, 200)));

    const tooltip = screen.getByRole('tooltip');

    expect(tooltip).toHaveTextContent('Fencing');
    expect(tooltip).toHaveTextContent('Protege I1 · I2');
  });
});
