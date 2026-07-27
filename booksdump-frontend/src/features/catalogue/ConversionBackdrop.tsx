import React, { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2 } from 'lucide-react';
import { toast } from 'sonner';

import { useBookConversion } from '@/context/BookConversionContext';

/**
 * ConversionBackdrop covers the catalogue while a book is being converted.
 *
 * The conversion rewrites the very file the reader just asked for, and a second
 * request for the same book while the first is in flight is wasted work on the
 * server, so the page is blocked rather than merely marked busy.
 */
function ConversionBackdrop() {
    const { state, dispatch } = useBookConversion();
    const { t } = useTranslation();

    /*
     * Failures are raised and drained from the queue in the same pass: left in
     * state they would be re-raised on every render. The toast id — the book and
     * the format that failed — collapses a repeat of the same failure instead of
     * stacking a second copy of it.
     */
    useEffect(() => {
        state.conversionErrors.forEach((err) => {
            toast.error(err.message, {
                id: `conversion-${err.bookID}-${err.format}`,
                duration: 4000,
            });
            dispatch({
                type: 'REMOVE_CONVERSION_ERROR',
                payload: { bookID: err.bookID, format: err.format },
            });
        });
    }, [state.conversionErrors, dispatch]);

    if (state.convertingBooks.length === 0) {
        return null;
    }

    return (
        // Always white on the dim: the backdrop is dark in either theme.
        <div
            role="status"
            aria-live="polite"
            className="fixed inset-0 z-modal flex flex-col items-center justify-center gap-2 bg-black/50 px-4 text-center text-white"
        >
            <Loader2 aria-hidden="true" className="size-10 animate-spin" />
            <p id="conversion-modal-title" className="text-lg font-medium">
                {t('conversionInProgress')}
            </p>
            <p id="conversion-modal-description" className="text-sm">
                {t('pleaseWait')}
            </p>
        </div>
    );
}

export default ConversionBackdrop;
