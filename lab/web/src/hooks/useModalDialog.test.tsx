import { render } from '@testing-library/react';
import { useRef } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useModalDialog } from './useModalDialog';

function Modal(): React.JSX.Element {
  const dialog = useRef<HTMLDialogElement>(null);

  useModalDialog(dialog);

  return <dialog ref={dialog} aria-label="modal" />;
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('useModalDialog', () => {
  it('opens the dialog as a modal and closes it while it is still in the page', () => {
    const connectedAtClose: boolean[] = [];

    vi.spyOn(HTMLDialogElement.prototype, 'close').mockImplementation(function close(this: HTMLDialogElement) {
      connectedAtClose.push(this.isConnected);
      this.removeAttribute('open');
    });

    const { getByRole, unmount } = render(<Modal />);

    expect(getByRole('dialog', { name: 'modal' })).toHaveAttribute('open');

    unmount();

    // A dialog closed after it left the page cannot give the focus back to
    // the button that opened it.
    expect(connectedAtClose).toEqual([true]);
  });
});
