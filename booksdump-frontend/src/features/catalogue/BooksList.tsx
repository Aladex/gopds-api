import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useLocation, useParams } from 'react-router';
import { format } from 'date-fns';
import { toast } from 'sonner';

import type { Book } from '@/api/books';
import * as adminApi from '@/api/admin';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/shared/ui/dialog';
import { Button } from '@/shared/ui/button';

import { useAuth } from '@/context/AuthContext';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import ConversionBackdrop from '@/features/catalogue/ConversionBackdrop';
import BookPagination from '@/features/catalogue/BookPagination';
import EditBookDialog from '@/features/catalogue/EditBookDialog';
import RescanPreviewDialog from '@/features/catalogue/RescanPreviewDialog';
import SkeletonCard from '@/features/catalogue/SkeletonCard';
import { useBooksQuery } from '@/features/catalogue/hooks/useBooksQuery';
import { useFavouriteToggle } from '@/features/catalogue/hooks/useFavouriteToggle';
import { useBookDownloads } from '@/features/catalogue/hooks/useBookDownloads';
import BookCard from '@/features/catalogue/BookCard';
import { ALL_LANGUAGES } from '@/shared/lib/languageUtils';

const SKELETON_COUNT = 10;

const BooksList: React.FC = () => {
    const { user } = useAuth();
    const { page } = useParams<{ page: string }>();
    const { t } = useTranslation();
    const location = useLocation();
    const { state, dispatch, loadBooks } = useBooksQuery();
    const [editDialogOpen, setEditDialogOpen] = useState(false);
    const [bookToEdit, setBookToEdit] = useState<Book | null>(null);
    const [rescanDialogOpen, setRescanDialogOpen] = useState(false);
    const [bookToRescan, setBookToRescan] = useState<number | null>(null);
    const [downloadError, setDownloadError] = useState<{ title: string; message: string } | null>(
        null,
    );

    // Asked once for the whole list rather than once per card: ten cards would
    // otherwise each hold their own media query listener for the same answer.
    const isMobile = useMediaQuery('(max-width: 600px)');

    // With a language filter on, every book in the list is in it and saying so
    // on each card is noise. Reading the whole library is when it starts to
    // matter which book is in which language.
    const mixedLanguages = !user?.books_lang || user.books_lang === ALL_LANGUAGES;

    const showDownloadError = (status: number, fallbackMessage?: string) => {
        const byStatus: Record<number, string> = {
            0: t('downloadErrorNetwork'),
            401: t('downloadErrorUnauthorized'),
            403: t('downloadErrorForbidden'),
            404: t('downloadErrorNotFound'),
        };
        setDownloadError({
            title: t('downloadErrorTitle'),
            message: byStatus[status] ?? fallbackMessage ?? t('downloadErrorUnknown'),
        });
    };

    const { handleDownload, handleEpubDownloadClick, handleMobiDownloadClick, isBookConverting } =
        useBookDownloads(showDownloadError);

    const formatDate = (dateString: string) => {
        if (dateString === '') {
            return t('unknownAddDate');
        }
        const timestamp = Date.parse(dateString);
        if (isNaN(timestamp)) {
            return dateString;
        }
        return format(new Date(timestamp), 'dd.MM.yyyy, HH:mm');
    };

    const notify = (message: string) => toast(message, { duration: 2000 });

    const handleFavBook = useFavouriteToggle(dispatch, notify);

    const handleUpdateBook = async (book: Book) => {
        const newBook = { ...book, approved: !book.approved };
        try {
            // Show the new state at once and put it back if the server disagrees:
            // approving is a frequent action and waiting on a round trip for each
            // one makes moderating a page of books feel broken.
            dispatch({ type: 'UPDATE_BOOK', payload: newBook });
            await adminApi.updateBook(newBook);
        } catch (error) {
            console.error('Error updating book', error);
            dispatch({ type: 'UPDATE_BOOK', payload: book });
        }
    };

    const handleEditBook = (book: Book) => {
        setBookToEdit(book);
        setEditDialogOpen(true);
    };

    const handleEditDialogClose = () => {
        setEditDialogOpen(false);
        setBookToEdit(null);
    };

    const handleBookUpdated = (updatedBook: Book) => {
        dispatch({ type: 'UPDATE_BOOK', payload: updatedBook });
        notify(t('bookUpdatedSuccessfully'));
        loadBooks();
    };

    const handleRescanBook = (bookId: number) => {
        setBookToRescan(bookId);
        setRescanDialogOpen(true);
    };

    const handleRescanDialogClose = () => {
        setRescanDialogOpen(false);
        setBookToRescan(null);
    };

    const handleRescanCompleted = () => {
        notify(t('rescanCompleted'));
        loadBooks();
    };

    return (
        <div className="flex min-h-[calc(100vh-200px)] flex-col">
            {state.loading ? (
                <div>
                    {Array.from({ length: SKELETON_COUNT }).map((_, index) => (
                        <div key={index} className="mx-auto w-full max-w-[1200px] py-1.5">
                            <SkeletonCard />
                        </div>
                    ))}
                </div>
            ) : state.books.length === 0 ? (
                <div className="mx-auto w-full max-w-[1200px] py-1.5">
                    <div className="rounded border border-border bg-card p-8">
                        <p className="text-center text-lg">{t('noBooksFound')}</p>
                    </div>
                </div>
            ) : (
                <div className="flex flex-1 flex-col">
                    {state.books.map((book) => (
                        <div key={book.id} className="mx-auto w-full max-w-[1200px] py-1.5">
                            <BookCard
                                book={book}
                                annotationPeekLines={isMobile ? 5 : 2}
                                isMobile={isMobile}
                                showLanguage={mixedLanguages}
                                isSuperuser={Boolean(user?.is_superuser)}
                                formatDate={formatDate}
                                isBookConverting={isBookConverting}
                                onDownload={handleDownload}
                                onEpubRequest={handleEpubDownloadClick}
                                onMobiRequest={handleMobiDownloadClick}
                                onToggleFavourite={handleFavBook}
                                onToggleApproved={handleUpdateBook}
                                onRescan={handleRescanBook}
                                onEdit={handleEditBook}
                            />
                        </div>
                    ))}
                    <div className="mt-auto flex justify-center pt-4">
                        <BookPagination
                            totalPages={state.totalPages}
                            currentPage={parseInt(page as string)}
                            baseUrl={location.pathname}
                        />
                    </div>
                </div>
            )}

            <EditBookDialog
                open={editDialogOpen}
                onClose={handleEditDialogClose}
                book={bookToEdit}
                onBookUpdated={handleBookUpdated}
            />
            <RescanPreviewDialog
                open={rescanDialogOpen}
                onClose={handleRescanDialogClose}
                bookId={bookToRescan}
                onRescanCompleted={handleRescanCompleted}
            />

            <Dialog
                open={downloadError !== null}
                onOpenChange={(open) => !open && setDownloadError(null)}
            >
                <DialogContent className="sm:max-w-sm" closeLabel={t('close')}>
                    <DialogHeader>
                        <DialogTitle>{downloadError?.title ?? t('downloadErrorTitle')}</DialogTitle>
                        <DialogDescription>{downloadError?.message}</DialogDescription>
                    </DialogHeader>
                    <DialogFooter>
                        <Button onClick={() => setDownloadError(null)}>OK</Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>

            <ConversionBackdrop />
        </div>
    );
};

export default BooksList;
