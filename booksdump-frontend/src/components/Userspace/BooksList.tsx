import '../styles/BooksList.css';
import React, { useReducer, useEffect, useRef, useState, useCallback } from 'react';
import ConversionBackdrop from '../hooks/convertingBooks';
import {
    Typography,
    Box,
    Stack,
    Card,
    CardContent,
    CardMedia,
    Button,
    CardActions, IconButton, Snackbar, Modal, Backdrop
} from '@mui/material';
import { useParams, useLocation, useNavigate } from 'react-router-dom';
import { API_URL } from '../../api/config';
import * as booksApi from '@/api/books';
import type { Book } from '@/api/books';
import * as adminApi from '@/api/admin';
import * as authApi from '@/api/auth';
import { useAuth } from '../../context/AuthContext';
import { useTranslation } from 'react-i18next';
import StarIcon from '@mui/icons-material/Star';
import StarOutlineIcon from '@mui/icons-material/StarOutline';
import CheckCircleIcon from '@mui/icons-material/CheckCircle';
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutline';
import EditIcon from '@mui/icons-material/Edit';
import RefreshIcon from '@mui/icons-material/Refresh';
import BookPagination from "../common/BookPagination";
import EditBookDialog from "../common/EditBookDialog";
import RescanPreviewDialog from "../common/RescanPreviewDialog";
import SkeletonCard from "../common/SkeletonCard";
import AuthorsList from "../common/AuthorsList";
import { useAuthor } from "../../context/AuthorContext";
import CategotiesList from "../common/CategotiesList";
import GenresList from "../common/GenresList";
import { useFav } from "../../context/FavContext";
import BookAnnotation from "../common/BookAnnotation";
import CoverLoader from "../common/CoverLoader";
import { format } from 'date-fns';
import { downloadViaIframe } from '../helpers/downloadViaIframe';
import { getLanguageInfo, isLanguageSupported } from '../../utils/languageUtils';
import { useBooksQuery } from './hooks/useBooksQuery';
import { useFavouriteToggle } from './hooks/useFavouriteToggle';
import { useBookDownloads } from './hooks/useBookDownloads';
import BookCard from './BookCard';

const BooksList: React.FC = () => {
    const { user } = useAuth();
    const { page } = useParams<{ page: string }>();
    const { t } = useTranslation();
    const location = useLocation();
    const fav = useFav();
    const navigate = useNavigate();
    const { state, dispatch, loadBooks } = useBooksQuery();
    const [messageQueue, setMessageQueue] = useState<string[]>([]);
    const [currentMessage, setCurrentMessage] = useState<string | null>(null);
    const [editDialogOpen, setEditDialogOpen] = useState(false);
    const [bookToEdit, setBookToEdit] = useState<Book | null>(null);
    const [rescanDialogOpen, setRescanDialogOpen] = useState(false);
    const [bookToRescan, setBookToRescan] = useState<number | null>(null);
    const [downloadErrorOpen, setDownloadErrorOpen] = useState(false);
    const [downloadErrorMessage, setDownloadErrorMessage] = useState('');
    const [downloadErrorTitle, setDownloadErrorTitle] = useState('');

    const showDownloadError = (status: number, fallbackMessage?: string) => {
        const title = t('downloadErrorTitle');
        let message = fallbackMessage || t('downloadErrorUnknown');
        if (status === 0) {
            message = t('downloadErrorNetwork');
        } else if (status === 401) {
            message = t('downloadErrorUnauthorized');
        } else if (status === 403) {
            message = t('downloadErrorForbidden');
        } else if (status === 404) {
            message = t('downloadErrorNotFound');
        }
        setDownloadErrorTitle(title);
        setDownloadErrorMessage(message);
        setDownloadErrorOpen(true);
    };

    const { handleDownload, handleEpubDownloadClick, handleMobiDownloadClick, isBookConverting } =
        useBookDownloads(showDownloadError);

    const formatDate = (dateString: string) => {
        if (dateString === "") {
            return t('unknownAddDate'); // Or t('unknownPublicationDate'), depending on context
        }
        const timestamp = Date.parse(dateString);
        if (isNaN(timestamp)) {
            return dateString;
        }
        const date = new Date(timestamp);
        return format(date, "dd.MM.yyyy, HH:mm");
    };

    const enqueueSnackbar = (message: string) => {
        setMessageQueue((prevQueue) => {
            const newQueue = [...prevQueue, message];
            if (!currentMessage) {
                processQueue(newQueue);
            }
            return newQueue;
        });
    };

    const processQueue = useCallback((queue = messageQueue) => {
        if (queue.length > 0) {
            const nextMessage = queue[0];
            setCurrentMessage(nextMessage);
            setMessageQueue((prevQueue) => prevQueue.slice(1));
        }
    }, [messageQueue]);

    const handleFavBook = useFavouriteToggle(dispatch, enqueueSnackbar);

    const handleCloseSnackbar = (message: string) => {
        setMessageQueue((prevQueue) => prevQueue.filter((msg) => msg !== message));
    };

    const handleUpdateBook = async (book: Book) => {
        const newBook = { ...book, approved: !book.approved };
        try {
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
        enqueueSnackbar(t('bookUpdatedSuccessfully'));
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
        enqueueSnackbar(t('rescanCompleted'));
        loadBooks();
    };

    const coverPath = (value: string) => value.replaceAll('.', '-').replace(/^\/+/, '');
    const cover = (book: Book) => `${API_URL}/books-posters/${coverPath(book.path)}/${coverPath(book.filename)}.jpg`;


    return (
        <Box sx={{ display: 'flex', flexDirection: 'column', minHeight: 'calc(100vh - 200px)' }}>
            {state.loading ? (
                <Stack spacing={0}>
                    {Array.from({ length: 10 }).map((_, index) => (
                        <Box maxWidth={1200} mx="auto" key={index} sx={{ width: '100%' }}>
                            <SkeletonCard />
                        </Box>
                    ))}
                </Stack>
            ) : state.books.length === 0 ? (
                <Box maxWidth={1200} mx="auto">
                    <Card sx={{ boxShadow: 2, p: 2, my: 2 }}>
                        <CardContent>
                            <Typography variant="h6" align="center">{t('noBooksFound')}</Typography>
                        </CardContent>
                    </Card>
                </Box>
            ) : (
                <Box sx={{ display: 'flex', flexDirection: 'column', flex: 1 }}>
                    <Stack spacing={0}>
                        {state.books.map((book) => (
                            <div key={book.id} className="mx-auto w-full max-w-[1200px] py-1.5">
                                <BookCard
                                    book={book}
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
                    </Stack>
                    <Box sx={{ display: 'flex', justifyContent: 'center', mt: 'auto', pt: 2 }}>
                        <BookPagination totalPages={state.totalPages} currentPage={parseInt(page as string)}
                                        baseUrl={location.pathname} />
                    </Box>
                </Box>
            )}
            {messageQueue.map((msg, index) => (
                <Snackbar
                    key={index}
                    anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
                    open={true}
                    autoHideDuration={2000}
                    onClose={() => handleCloseSnackbar(msg)}
                    message={msg}
                    style={{ bottom: `${index * 50}px` }}
                />
            ))}
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
            <Modal
                open={downloadErrorOpen}
                onClose={() => setDownloadErrorOpen(false)}
                closeAfterTransition
                slots={{ backdrop: Backdrop }}
                slotProps={{
                    backdrop: {
                        sx: {
                            backgroundColor: 'rgba(0, 0, 0, 0.5)',
                            zIndex: (theme) => theme.zIndex.drawer + 1,
                        },
                    },
                }}
            >
                <Box
                    sx={{
                        position: 'absolute',
                        top: '50%',
                        left: '50%',
                        transform: 'translate(-50%, -50%)',
                        textAlign: 'center',
                        color: (theme) => theme.palette.common.white,
                        zIndex: (theme) => theme.zIndex.modal + 1,
                        backgroundColor: 'rgba(0,0,0,0.8)',
                        padding: 3,
                        borderRadius: 2,
                        minWidth: 280,
                    }}
                >
                    <Typography variant="h6" sx={{ mb: 1 }}>
                        {downloadErrorTitle || t('downloadErrorTitle')}
                    </Typography>
                    <Typography variant="body2" sx={{ mb: 2 }}>
                        {downloadErrorMessage}
                    </Typography>
                    <Button variant="contained" onClick={() => setDownloadErrorOpen(false)}>
                        OK
                    </Button>
                </Box>
            </Modal>
            <ConversionBackdrop />
        </Box>
    );
};

export default BooksList;
