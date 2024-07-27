// src/components/BooksList.tsx
import '../styles/BooksList.css';
import React, { useState, useEffect, useRef } from 'react';
import {useParams, useLocation} from 'react-router-dom';
import {
    Typography,
    Box,
    Grid,
    Card,
    CardContent,
    CardMedia,
    Button,
    CardActions, IconButton
} from '@mui/material';
import {API_URL, fetchWithAuth} from '../../api/config';
import {useAuth} from '../../context/AuthContext';
import {useTranslation} from 'react-i18next';
import StarIcon from '@mui/icons-material/Star';
import StarOutlineIcon from '@mui/icons-material/StarOutline';
import CheckCircleIcon from '@mui/icons-material/CheckCircle';
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutline';
import BookPagination from "../common/BookPagination";
import SkeletonCard from "../common/SkeletonCard";
import AuthorsList from "../common/AuthorsList";
import {useAuthor} from "../../context/AuthorContext";
import CategotiesList from "../common/CategotiesList";
import {useFav} from "../../context/FavContext";
import BookAnnotation from "../common/BookAnnotation";
import CoverLoader from "../common/CoverLoader";
import { format } from 'date-fns';
import {useNavigate} from 'react-router-dom';

interface Book {
    id: number;
    title: string;
    authors: { id: number; full_name: string }[];
    series: { id: number; ser: string; ser_no: number }[];
    annotation: string;
    filename: string;
    cover: string;
    registerdate: string;
    docdate: string;
    lang: string;
    fav: boolean;
    approved: boolean;
    path: string;
    format: string;
}

const BooksList: React.FC = () => {
    const {user} = useAuth();
    const {page, id, title} = useParams<{ page: string, id?: string, title?: string }>();
    const [books, setBooks] = useState<Book[]>([]);
    const [totalPages, setTotalPages] = useState(0);
    const [loading, setLoading] = useState(true);
    const {t} = useTranslation();
    const {authorId, authorBook, setAuthorId, clearAuthorBook} = useAuthor();
    const location = useLocation();
    const fav = useFav();
    const navigate = useNavigate();
    const prevLangRef = useRef(user?.books_lang);

    type Params = {
        limit: number;
        offset: number;
        fav?: boolean;
        lang: string;
        author?: string;
        series?: string;
        title?: string;
    };

    const formatDate = (dateString: string) => {
        const date = new Date(dateString);
        return format(date, "dd.MM.yyyy, HH:mm");
    };

    useEffect(() => {
        console.log('Fetching books...');
        const fetchBooks = async () => {
            if (prevLangRef.current !== user?.books_lang && page !== '1') {
                navigate('/books/page/1');
                return;
            }
            setLoading(true);
            const limit = 10;
            const currentPage = parseInt(page || '1', 10);
            const offset = (currentPage - 1) * limit;
            let params: Params = { limit, offset, lang: user?.books_lang || '' };

            if (location.pathname.includes('/books/find/author/')) {
                if (id) {
                    params.author = id;
                    setAuthorId(id);
                    if (authorBook) params.title = authorBook;
                }
            } else if (location.pathname.includes('/books/find/category/')) {
                if (id) {
                    params.series = id;
                    clearAuthorBook();
                }
            } else if (location.pathname.includes('/books/find/title/')) {
                if (title) {
                    params.title = decodeURIComponent(title);
                    if (authorId) params.author = authorId;
                    clearAuthorBook();
                }
            }

            if (location.pathname.includes('/books/favorite')) {
                params.fav = true;
                clearAuthorBook();
            }

            try {
                const response = await fetchWithAuth.get('/books/list', { params });
                if (response.status === 200) {
                    const data = response.data;
                    setBooks(data.books);
                    setTotalPages(data.length);
                } else {
                    console.error('Failed to fetch books');
                }
            } catch (error) {
                console.error('Error fetching books', error);
            } finally {
                setLoading(false);
            }
        };

        fetchBooks();
    }, [page,
        user?.books_lang,
        id,
        title,
        location.pathname,
        setAuthorId,
        clearAuthorBook,
        authorId,
        authorBook,
        navigate
    ]);

    /**
     * Handles the favoriting of a book.
     *
     * This function optimistically updates the local state to reflect the new favorite status of the book.
     * It then sends a request to the server to update the favorite status. If the request is successful,
     * it fetches the updated user data and updates the favorite context. If the request fails, it reverts
     * the optimistic update.
     *
     * @param {Book} book - The book to be favorited or unfavorited.
     */
    const handleFavBook = async (book: Book) => {
        try {
            // Optimistically update the local state
            setBooks(prev => prev.map(b => b.id === book.id ? {...b, fav: !b.fav} : b));

            // Send request to update favorite status
            const response = await fetchWithAuth.post('/books/fav', {book_id: book.id, fav: !book.fav});
            if (response.status === 200) {
                // Fetch updated user data
                const userResponse = await fetchWithAuth.get('/books/self-user');
                if (userResponse.status === 200) {
                    // Update favorite context
                    fav.setFavEnabled(userResponse.data.have_favs);
                    // Navigate to books page if no favorites are left
                    if (location.pathname.includes('/books/favorite') && !userResponse.data.have_favs) {
                        navigate('/books/page/1');
                        fav.setFav(false);
                    }
                } else {
                    console.error('Failed to fetch updated user data');
                }
            } else {
                console.error('Failed to update favorite status');
                // Revert the optimistic update if the request fails
                setBooks(prev => prev.map(b => b.id === book.id ? {...b, fav: book.fav} : b));
            }
        } catch (error) {
            console.error('Error favoriting book', error);
            // Revert the optimistic update if an error occurs
            setBooks(prev => prev.map(b => b.id === book.id ? {...b, fav: book.fav} : b));
        }
    };

    const handleUpdateBook = async (book: Book) => {
        try {
            // Change only approved field
            let newBook = {...book, approved: !book.approved};
            // Optimistically update the local state
            setBooks(prev => prev.map(b => b.id === book.id ? newBook : b));
            // Send request to update book
            const response = await fetchWithAuth.post('/admin/update-book', newBook);
            // Update book in local state from response
            if (response.status === 200) {
                setBooks(prev => prev.map(b => b.id === book.id ? response.data : b));
            } else {
                console.error('Failed to update book');
                // Revert the optimistic update if the request fails
                setBooks(prev => prev.map(b => b.id === book.id ? book : b));
            }
        } catch (error) {
            console.error('Error updating book', error);
        }
    }


    const cover = (book: Book) => `${API_URL}/books-posters/${book.path.replace(/\W/g, '-')}/${book.filename.replace(/\W/g, '-')}.jpg`;

    return (
        <Box>
            {loading ? (
                Array.from({length: 10}).map((_, index) => (
                    <Grid item xs={12} key={index}>
                        <Box maxWidth={1200} mx="auto">
                            <SkeletonCard/>
                        </Box>
                    </Grid>
                ))
            ) : books.length === 0 ? (
                <Grid container justifyContent="center">
                    <Grid item xs={12}>
                        <Box maxWidth={1200} mx="auto">
                            <Card sx={{boxShadow: 2, p: 2, my: 2}}>
                                <CardContent>
                                    <Typography variant="h6" align="center">{t('noBooksFound')}</Typography>
                                </CardContent>
                            </Card>
                        </Box>
                    </Grid>
                </Grid>
            ) : (
                <>

                    <Grid container justifyContent="center">
                        {books.map((book) => (
                            <Grid item xs={12} key={book.id}>
                                <Box maxWidth={1200} mx="auto">
                                    <Card sx={{boxShadow: 2, p: 2, my: 2}}>
                                        <Grid container spacing={2}>
                                            <Grid item xs={12} md={9}>
                                                <Grid container spacing={2}>
                                                    <Grid item xs={12} md={4}>
                                                        <CardMedia
                                                            component={CoverLoader}
                                                            imageUrl={cover(book)}
                                                            alt={book.title}
                                                        />
                                                    </Grid>
                                                    <Grid item xs={12} md={8}>
                                                        <CardContent>
                                                            <Typography variant="h5">{book.title}</Typography>
                                                            <Typography sx={{ mb: 2 }} variant="body2" color="textSecondary">
                                                                {t('bookAdded')}: {formatDate(book.registerdate)}
                                                            </Typography>
                                                            <Typography sx={{ mb: 2 }} variant="body2" color="textSecondary">
                                                                {t('bookPublished')}: {book.docdate ? formatDate(book.docdate) : t('unknownPublicationDate')}
                                                            </Typography>
                                                            <Typography variant="body2" color="textSecondary"
                                                                        sx={{display: 'flex', alignItems: 'center'}}>
                                                                {t('language')}:
                                                                <Box sx={{
                                                                    width: 24,
                                                                    height: 24,
                                                                    bgcolor: 'secondary.main',
                                                                    color: 'white',
                                                                    display: 'flex',
                                                                    justifyContent: 'center',
                                                                    alignItems: 'center',
                                                                    ml: 1
                                                                }}>
                                                                    {book.lang}
                                                                </Box>
                                                            </Typography>
                                                            <Box mt={2}>
                                                                <AuthorsList authors={book.authors}/>
                                                            </Box>
                                                            <Box mt={2}>
                                                                <CategotiesList categories={book.series}/>
                                                            </Box>
                                                        </CardContent>
                                                    </Grid>
                                                </Grid>
                                                <CardContent>
                                                    {book.annotation ? (
                                                        <BookAnnotation annotation={book.annotation}/>
                                                    ) : (
                                                        <Box mt={2}>
                                                            <Typography variant="body2">{t('noAnnotation')}</Typography>
                                                        </Box>
                                                    )}
                                                </CardContent>
                                            </Grid>
                                            <Grid item xs={12} md={3}>
                                                <Box className="download-buttons">
                                                    <Button
                                                        component="a"
                                                        href={`${API_URL}/files/books/get/zip/${book.id}`}
                                                        variant="contained"
                                                        color="secondary"
                                                        sx={{ mb: 1, color: 'white', minWidth: 120 }}
                                                    >
                                                        FB2+ZIP
                                                    </Button>
                                                    <Button
                                                        component="a"
                                                        href={`${API_URL}/files/books/get/fb2/${book.id}`}
                                                        variant="contained"
                                                        color="secondary"
                                                        sx={{ mb: 1, color: 'white', minWidth: 120 }}
                                                    >
                                                        FB2
                                                    </Button>
                                                    <Button
                                                        component="a"
                                                        href={`${API_URL}/files/books/get/epub/${book.id}`}
                                                        variant="contained"
                                                        color="secondary"
                                                        sx={{ mb: 1, color: 'white', minWidth: 120 }}
                                                    >
                                                        EPUB
                                                    </Button>
                                                    <Button
                                                        component="a"
                                                        href={`${API_URL}/files/books/get/mobi/${book.id}`}
                                                        variant="contained"
                                                        color="secondary"
                                                        sx={{ mb: 1, color: 'white', minWidth: 120 }}
                                                    >
                                                        MOBI
                                                    </Button>
                                                </Box>

                                            </Grid>
                                        </Grid>
                                        <CardActions sx={{justifyContent: 'flex-end'}}>
                                            {user?.is_superuser && (
                                                <IconButton onClick={() => handleUpdateBook(book)}>
                                                    {book.approved ? <CheckCircleIcon/> : <CheckCircleOutlineIcon/>}
                                                </IconButton>
                                            )}
                                            <IconButton onClick={() => handleFavBook(book)}>
                                                {book.fav ? <StarIcon/> : <StarOutlineIcon/>}
                                            </IconButton>
                                        </CardActions>
                                    </Card>
                                </Box>
                            </Grid>
                        ))}
                    </Grid>
                    <Grid container spacing={3} justifyContent="center" sx={{marginTop: 2}}>
                        <BookPagination totalPages={totalPages} currentPage={parseInt(page as string)}
                                        baseUrl={location.pathname}/>
                    </Grid>
                </>
            )}
        </Box>
    );
};

export default BooksList;
