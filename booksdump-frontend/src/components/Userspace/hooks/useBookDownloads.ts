import { useCallback } from 'react';

import { API_URL } from '../../../api/config';
import { useBookConversion } from '../../../context/BookConversionContext';
import { downloadViaIframe } from '../../helpers/downloadViaIframe';

/**
 * useBookDownloads owns getting a file to the reader.
 *
 * FB2 and FB2+ZIP exist on disk and are fetched straight away. EPUB and MOBI
 * are produced on demand, so before queueing a conversion the source FB2 is
 * probed with a HEAD request — otherwise a missing source would be reported only
 * after the reader had waited through the conversion.
 *
 * Extracted from BooksList unchanged.
 */
export function useBookDownloads(showDownloadError: (status: number, fallbackMessage?: string) => void) {
    const { state: conversionState, dispatch: conversionDispatch } = useBookConversion();

    /** sourceAvailable reports whether the FB2 a conversion needs is there. */
    const sourceAvailable = useCallback(
        async (bookID: number) => {
            try {
                const sourceUrl = `${API_URL}/files/books/get/fb2/${bookID}`;
                const sourceCheck = await fetch(sourceUrl, { method: 'HEAD', credentials: 'include' });
                if (!sourceCheck.ok) {
                    showDownloadError(sourceCheck.status);
                    return false;
                }
                return true;
            } catch {
                showDownloadError(0);
                return false;
            }
        },
        [showDownloadError],
    );

    const requestConversion = useCallback(
        async (bookID: number, format: 'epub' | 'mobi') => {
            if (!(await sourceAvailable(bookID))) {
                return;
            }
            conversionDispatch({ type: 'ADD_CONVERTING_BOOK', payload: { bookID, format } });
        },
        [conversionDispatch, sourceAvailable],
    );

    const handleEpubDownloadClick = useCallback(
        (bookID: number) => requestConversion(bookID, 'epub'),
        [requestConversion],
    );

    const handleMobiDownloadClick = useCallback(
        (bookID: number) => requestConversion(bookID, 'mobi'),
        [requestConversion],
    );

    const isBookConverting = useCallback(
        (bookID: number, format: string) =>
            conversionState.convertingBooks.some(
                (book) => book.bookID === bookID && book.format === format,
            ),
        [conversionState.convertingBooks],
    );

    const handleDownload = useCallback(
        (format: string, bookID: number) => {
            const url = `${API_URL}/files/books/get/${format}/${bookID}`;
            downloadViaIframe(url, (status) => showDownloadError(status));
        },
        [showDownloadError],
    );

    return { handleDownload, handleEpubDownloadClick, handleMobiDownloadClick, isBookConverting };
}
