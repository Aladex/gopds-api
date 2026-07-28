import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import { toast } from 'sonner';

import ConversionBackdrop from '@/features/catalogue/ConversionBackdrop';

// The backdrop covers the whole page, so appearing when nothing is converting
// would lock the catalogue. And a conversion failure has to be drained from the
// queue after it is shown, or the same message is raised on every render.
//
// "Covers" has to mean it: a fixed div over the top hides the page without
// stopping it scrolling, and leaves every button on it in the tab order — so a
// second conversion of the same book could be started from the keyboard, which
// is the one thing the backdrop exists to prevent.

vi.mock('sonner', () => ({ toast: { error: vi.fn() } }));

const translate = (key: string) => key;
const translation = { t: translate };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

const state: {
    convertingBooks: { bookID: number; format: string }[];
    conversionErrors: { bookID: number; format: string; message: string }[];
} = { convertingBooks: [], conversionErrors: [] };
const dispatch = vi.fn();
vi.mock('@/context/BookConversionContext', () => ({
    useBookConversion: () => ({ state, dispatch }),
}));

beforeEach(() => {
    state.convertingBooks = [];
    state.conversionErrors = [];
    dispatch.mockReset();
    vi.mocked(toast.error).mockReset();
});

describe('ConversionBackdrop', () => {
    it('stays out of the way when nothing is converting', () => {
        render(<ConversionBackdrop />);

        expect(screen.queryByText('conversionInProgress')).not.toBeInTheDocument();
    });

    it('covers the page while a conversion runs', () => {
        state.convertingBooks = [{ bookID: 1, format: 'epub' }];
        render(<ConversionBackdrop />);

        expect(screen.getByText('conversionInProgress')).toBeInTheDocument();
        expect(screen.getByText('pleaseWait')).toBeInTheDocument();
    });

    it('stops the page behind it from scrolling', () => {
        const { rerender } = render(<ConversionBackdrop />);
        const before = document.body.style.pointerEvents;

        state.convertingBooks = [{ bookID: 1, format: 'mobi' }];
        rerender(<ConversionBackdrop />);

        // Radix marks the body while a modal owns the screen; this is the same
        // lock the dialogs elsewhere in the application get.
        expect(document.body).toHaveAttribute('data-scroll-locked');

        state.convertingBooks = [];
        rerender(<ConversionBackdrop />);

        expect(document.body).not.toHaveAttribute('data-scroll-locked');
        expect(document.body.style.pointerEvents).toBe(before);
    });

    it('takes the page behind it out of reach', () => {
        state.convertingBooks = [{ bookID: 1, format: 'mobi' }];
        render(
            <>
                <button type="button">MOBI</button>
                <ConversionBackdrop />
            </>,
        );

        // The button that started the conversion must not be pressable again
        // while it runs — not by mouse, and not by tabbing to it either. It is
        // still in the document, but no longer in the accessibility tree, which
        // is what takes it out of the tab order and away from a screen reader.
        expect(screen.queryByRole('button', { name: 'MOBI' })).toBeNull();
        expect(document.querySelector('button')).toHaveTextContent('MOBI');
        expect(document.querySelector('button')!.closest('[aria-hidden="true"]')).not.toBeNull();
    });

    it('offers nothing to dismiss it with', () => {
        state.convertingBooks = [{ bookID: 1, format: 'mobi' }];
        render(<ConversionBackdrop />);

        // It ends when the conversion does. A close button would promise a
        // cancellation that neither the page nor the server can honour.
        expect(screen.queryByRole('button')).not.toBeInTheDocument();
    });

    it('raises each failure once and clears it from the queue', async () => {
        state.conversionErrors = [
            { bookID: 1, format: 'epub', message: 'Конвертация не удалась' },
            { bookID: 2, format: 'mobi', message: 'Формат не поддерживается' },
        ];
        render(<ConversionBackdrop />);

        await waitFor(() => expect(toast.error).toHaveBeenCalledTimes(2));
        expect(toast.error).toHaveBeenCalledWith('Конвертация не удалась', expect.anything());
        expect(dispatch).toHaveBeenCalledWith({
            type: 'REMOVE_CONVERSION_ERROR',
            payload: { bookID: 1, format: 'epub' },
        });
        expect(dispatch).toHaveBeenCalledWith({
            type: 'REMOVE_CONVERSION_ERROR',
            payload: { bookID: 2, format: 'mobi' },
        });
    });
});
