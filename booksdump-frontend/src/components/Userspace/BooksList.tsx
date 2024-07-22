// src/components/BooksList.tsx
import React, {useState, useEffect, useCallback} from 'react';
import {useParams} from 'react-router-dom';
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
import axios from 'axios';
import {API_URL, APP_URL} from '../../api/config';
import {useAuth} from '../../context/AuthContext';
import {useTranslation} from 'react-i18next';
import StarIcon from '@mui/icons-material/Star';
import StarOutlineIcon from '@mui/icons-material/StarOutline';
import CheckCircleIcon from '@mui/icons-material/CheckCircle';
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutline';
import BookPagination from "../common/BookPagination";
import SkeletonCard from "../common/SkeletonCard";
import AuthorsList from "../common/AuthorsList";
import SearchBar from "../common/SearchBar";

interface Book {
    id: number;
    title: string;
    authors: { id: number; full_name: string }[];
    series: { id: number; ser: string; ser_no: number }[];
    annotation: string;
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
    const { user, token } = useAuth();
    const {page} = useParams<{ page: string }>();
    const [books, setBooks] = useState<Book[]>([]);
    const [totalPages, setTotalPages] = useState(0);
    const [loading, setLoading] = useState(true);
    const [opened, setOpened] = useState<number[]>([]);
    const {t} = useTranslation();
    const baseUrl = window.location.pathname.replace(/\/\d+$/, '');

    type Params = {
        limit: number;
        offset: number;
        fav?: boolean;
        lang: string;
        author?: string;
        series?: string;
        title?: string;
    };

    const fetchBooks = useCallback(async () => {
        setLoading(true);
        const limit = 10;
        const currentPage = parseInt(page || '1', 10);
        const offset = (currentPage - 1) * limit;

        let params: Params = {limit, offset, lang: user?.books_lang || ''};
        // Checks the `baseUrl` to determine the type of search being performed and sets the appropriate parameter.
        // This block supports three types of searches: by author, by category, and by title.
        // Each condition checks for a specific pattern in the `baseUrl` and extracts the relevant parameter from the URL.

        // Checks if the search is for an author. If so, extracts the author's name from the URL.
        if (baseUrl.includes('/find/author/')) {
            params.author = baseUrl.split('/').pop() || '';
        }
        // Checks if the search is for a category. If so, extracts the category name from the URL.
        else if (baseUrl.includes('/find/category/')) {
            params.series = baseUrl.split('/').pop() || '';
        }
        // Checks if the search is for a title. If so, extracts the title from the URL.
        else if (baseUrl.includes('/books/find/title/')) {
            const urlSegments = baseUrl.split('/');
            const titleIndex = urlSegments.findIndex(segment => segment === 'title');
            // Ensures that the title segment exists and is not the last segment of the URL.
            if (titleIndex !== -1 && titleIndex < urlSegments.length - 1) {
                const title = decodeURIComponent(urlSegments[titleIndex + 1]);
                params.title = title;
            }
        }
        const isFavoritePath = baseUrl.includes('/books/favorite');
        if (isFavoritePath) {
            params.fav = true;
        }
        try {
            const response = await axios.get(`${API_URL}/books/list`, {
                headers: { Authorization: `${token}` },
                params,
            });
            setBooks(response.data.books);
            setTotalPages(response.data.length);
        } catch (error) {
            console.error('Error fetching books', error);
        } finally {
            setLoading(false);
        }
    }, [page, baseUrl, user?.books_lang, token]);

    useEffect(() => {
        if (token && user) {
            fetchBooks().then(r => r);
        }
    }, [token, page, user, fetchBooks, baseUrl]);

    const handleOpenAnnotation = (id: number) => {
        setOpened((prev) => (prev.includes(id) ? prev.filter((bookId) => bookId !== id) : [...prev, id]));
    };

    const handleFavBook = async (book: Book) => {
        // Логика добавления в избранное
    };

    const handleUpdateBook = async (book: Book) => {
        // Логика обновления книги
    };

    const cover = (book: Book) => `${APP_URL}books-posters/${book.path.replace(/[^a-zA-Z0-9]/g, '-')}/${book.id}-${book.format}.jpg`;

    return (
        <Box p={2}>
            <SearchBar />
            {loading ? (
                Array.from({length: 10}).map((_, index) => (
                    <Grid item xs={12} key={index}>
                        <Box maxWidth={1200} mx="auto">
                            <SkeletonCard />
                        </Box>
                    </Grid>
                ))
            ) : books.length === 0 ? (
                <Typography variant="h6">No books found</Typography>
            ) : (
                <>

                    <Grid container justifyContent="center">
                        {books.map((book) => (
                            <Grid item xs={12} key={book.id}>
                                <Box maxWidth={1200} mx="auto">
                                    <Card sx={{ boxShadow: 2, p: 2, my: 2 }}>
                                        <Grid container spacing={2}>
                                            <Grid item xs={12} md={9}>
                                                <Grid container spacing={2}>
                                                    <Grid item xs={12} md={4}>
                                                        <CardMedia
                                                            component="img"
                                                            height="300"
                                                            image={cover(book)}
                                                            alt={book.title}
                                                            sx={{ objectFit: 'scale-down' }}
                                                        />
                                                    </Grid>
                                                    <Grid item xs={12} md={8}>
                                                        <CardContent>
                                                            <Typography variant="h5">{book.title}</Typography>
                                                            <Typography variant="body2" color="textSecondary">
                                                                {t('bookAdded')}: {new Date(book.registerdate).toLocaleString()}
                                                            </Typography>
                                                            <Typography variant="body2" color="textSecondary">
                                                                {t('bookPublished')}: {new Date(book.docdate).toLocaleDateString()}
                                                            </Typography>
                                                            <Typography variant="body2" color="textSecondary" sx={{ display: 'flex', alignItems: 'center' }}>
                                                                {t('language')}:
                                                                <Box sx={{ width: 24, height: 24, bgcolor: 'secondary.main', color: 'white', display: 'flex', justifyContent: 'center', alignItems: 'center', ml: 1 }}>
                                                                    {book.lang}
                                                                </Box>
                                                            </Typography>
                                                            <Box mt={2}>
                                                                <AuthorsList authors={book.authors} />
                                                            </Box>
                                                            {book.series && (
                                                                <Box mt={2}>
                                                                    <Typography variant="subtitle1">{t('series')}:</Typography>
                                                                    {book.series.map((serie) => (
                                                                        <Typography key={serie.id} variant="body2">
                                                                            &#8226; {serie.ser} {serie.ser_no !== 0 && `#${serie.ser_no}`}
                                                                        </Typography>
                                                                    ))}
                                                                </Box>
                                                            )}
                                                        </CardContent>
                                                    </Grid>
                                                </Grid>
                                                <CardContent>
                                                    <Box mt={2}>
                                                        <Typography variant="subtitle1">{t('annotation')}:</Typography>
                                                        {opened.includes(book.id) ? (
                                                            <Typography variant="body2">{book.annotation}</Typography>
                                                        ) : (
                                                            <Typography variant="body2">
                                                                {book.annotation.slice(0, 200)}
                                                                {book.annotation.length > 200 && (
                                                                    <>
                                                                        <br/><span onClick={() => handleOpenAnnotation(book.id)}><i>{t('readMore')}</i></span>
                                                                    </>
                                                                )}
                                                            </Typography>
                                                        )}
                                                    </Box>
                                                </CardContent>
                                            </Grid>
                                            <Grid item xs={12} md={3}>
                                                <Box display="flex" flexWrap="wrap" justifyContent={{ xs: 'center', md: 'end' }} gap={1} sx={{ mt: 2 }}>
                                                    <Button
                                                        variant="contained"
                                                        color="secondary"
                                                        sx={{ mb: 1, color: 'white', minWidth: 150 }}
                                                        href={`${API_URL}/files/books/get/zip/${book.id}?token=${token}`}
                                                    >
                                                        FB2+ZIP
                                                    </Button>
                                                    <Button
                                                        variant="contained"
                                                        color="secondary"
                                                        sx={{ mb: 1, color: 'white', minWidth: 150 }}
                                                        href={`${API_URL}/files/books/get/fb2/${book.id}?token=${token}`}
                                                    >
                                                        FB2
                                                    </Button>
                                                    <Button
                                                        variant="contained"
                                                        color="secondary"
                                                        sx={{ mb: 1, color: 'white', minWidth: 150 }}
                                                        href={`${API_URL}/files/books/get/epub/${book.id}?token=${token}`}
                                                    >
                                                        EPUB
                                                    </Button>
                                                    <Button
                                                        variant="contained"
                                                        color="secondary"
                                                        sx={{ mb: 1, color: 'white', minWidth: 150 }}
                                                        href={`${API_URL}/files/books/get/mobi/${book.id}?token=${token}`}
                                                    >
                                                        MOBI
                                                    </Button>
                                                </Box>
                                            </Grid>
                                        </Grid>
                                        <CardActions sx={{ justifyContent: 'flex-end' }}>
                                            {user?.is_superuser && (
                                                <IconButton onClick={() => handleUpdateBook(book)}>
                                                    {book.approved ? <CheckCircleIcon /> : <CheckCircleOutlineIcon />}
                                                </IconButton>
                                            )}
                                            <IconButton onClick={() => handleFavBook(book)}>
                                                {book.fav ? <StarIcon /> : <StarOutlineIcon />}
                                            </IconButton>
                                        </CardActions>
                                    </Card>
                                </Box>
                            </Grid>
                        ))}
                    </Grid>
                    <Grid container spacing={3} justifyContent="center" sx={{ marginTop: 2 }}>
                        <BookPagination totalPages={totalPages} currentPage={parseInt(page || '1', 10)} baseUrl={baseUrl} />
                    </Grid>
                </>
            )}
        </Box>
    );
};

export default BooksList;
