import React, { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';

import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/shared/ui/dialog';
import { BouncingDots } from '@/shared/ui/bouncing-dots';

import { useBookConversion } from '@/context/BookConversionContext';

/**
 * ConversionBackdrop covers the catalogue while a book is being converted.
 *
 * The conversion rewrites the very file the reader just asked for, and a second
 * request for the same book while the first is in flight is wasted work on the
 * server, so the page is blocked rather than merely marked busy.
 *
 * Blocking it means a modal, not a fixed div over the top. A div only covers
 * the picture: the page behind still scrolls under the wheel, and every button
 * on it is still in the tab order — so the format that started the conversion
 * could be pressed again from the keyboard, which is the one thing this exists
 * to prevent.
 *
 * It cannot be dismissed. There is nothing for the reader to decide: it goes
 * when the conversion does, or when it fails, and both arrive over the socket.
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

    const converting = state.convertingBooks.length > 0;

    return (
        <Dialog open={converting}>
            <DialogContent
                showCloseButton={false}
                // Neither Escape nor a click outside ends a conversion, so
                // neither should look as though it might.
                onEscapeKeyDown={(event) => event.preventDefault()}
                onInteractOutside={(event) => event.preventDefault()}
                // The message sits on the dim itself rather than on a panel:
                // there is nothing to read but two lines and nothing to do.
                className="max-w-xs border-0 bg-transparent text-center text-white shadow-none"
            >
                <div className="flex flex-col items-center gap-2">
                    <BouncingDots size="lg" />
                    <DialogTitle className="text-lg font-medium">
                        {t('conversionInProgress')}
                    </DialogTitle>
                    <DialogDescription className="text-sm text-white/80">
                        {t('pleaseWait')}
                    </DialogDescription>
                </div>
            </DialogContent>
        </Dialog>
    );
}

export default ConversionBackdrop;
