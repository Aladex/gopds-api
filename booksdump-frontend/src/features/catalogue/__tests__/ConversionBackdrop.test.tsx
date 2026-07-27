import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import { toast } from 'sonner';

import ConversionBackdrop from '@/features/catalogue/ConversionBackdrop';

// The backdrop covers the whole page, so appearing when nothing is converting
// would lock the catalogue. And a conversion failure has to be drained from the
// queue after it is shown, or the same message is raised on every render.

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
