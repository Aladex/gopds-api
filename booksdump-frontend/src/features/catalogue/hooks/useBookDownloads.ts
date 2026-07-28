import { API_URL } from '@/api/config';
import { useBookConversion } from '@/context/BookConversionContext';
import { downloadViaIframe } from '@/shared/lib/downloadViaIframe';

/**
 * useBookDownloads owns getting a file to the reader.
 *
 * FB2 and its zipped form exist on disk and are fetched straight away. EPUB and
 * MOBI are produced on demand, so before queueing a conversion the source FB2 is
 * probed with a HEAD request — otherwise a missing source would be reported only
 * after the reader had waited through the conversion.
 *
 * None of these are wrapped in useCallback: nothing reads their identity — they
 * are called from click handlers, not named in a dependency list — and the React
 * Compiler memoises what is worth memoising.
 */
export function useBookDownloads(
    showDownloadError: (status: number, fallbackMessage?: string) => void,
) {
    const { state: conversionState, dispatch: conversionDispatch } = useBookConversion();

    /** sourceAvailable reports whether the FB2 a conversion needs is there. */
    const sourceAvailable = async (bookID: number) => {
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
    };

    const requestConversion = async (bookID: number, format: 'epub' | 'mobi') => {
        if (!(await sourceAvailable(bookID))) {
            return;
        }
        conversionDispatch({ type: 'ADD_CONVERTING_BOOK', payload: { bookID, format } });
    };

    const handleEpubDownloadClick = (bookID: number) => requestConversion(bookID, 'epub');
    const handleMobiDownloadClick = (bookID: number) => requestConversion(bookID, 'mobi');

    const isBookConverting = (bookID: number, format: string) =>
        conversionState.convertingBooks.some(
            (book) => book.bookID === bookID && book.format === format,
        );

    const handleDownload = (format: string, bookID: number) => {
        const url = `${API_URL}/files/books/get/${format}/${bookID}`;
        downloadViaIframe(url, (status) => showDownloadError(status));
    };

    return { handleDownload, handleEpubDownloadClick, handleMobiDownloadClick, isBookConverting };
}
