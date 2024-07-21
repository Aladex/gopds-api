// src/components/BooksList.tsx
import React, {useState, useEffect} from 'react';
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
import {API_URL} from '../../api/config';
import {useAuth} from '../../context/AuthContext';
import {useTranslation} from 'react-i18next';
import StarIcon from '@mui/icons-material/Star';
import StarOutlineIcon from '@mui/icons-material/StarOutline';
import CheckCircleIcon from '@mui/icons-material/CheckCircle';
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutline';
import BookPagination from "../common/BookPagination";
import SkeletonCard from "../common/SkeletonCard";

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
}

const BooksList: React.FC = () => {
    const {token} = useAuth();
    const {page} = useParams<{ page: string }>();
    const [books, setBooks] = useState<Book[]>([]);
    const [totalPages, setTotalPages] = useState(0);
    const [loading, setLoading] = useState(true);
    const [opened, setOpened] = useState<number[]>([]);
    const [user] = useState<{ is_superuser: boolean, books_lang?: string } | null>(null);
    const {t} = useTranslation();
    // Destructure books_lang from user outside of useEffect
    const books_lang = user?.books_lang;

    useEffect(() => {
        const fetchUser = async () => {
            // Implementation remains the same
        };

        const fetchBooks = async () => {
            setLoading(true);
            const limit = 10; // Количество книг на страницу
            const currentPage = parseInt(page || '1', 10); // Получение текущей страницы из URL
            const offset = (currentPage - 1) * limit; // Вычисление смещения

            try {
                const response = await axios.get(`${API_URL}/books/list`, {
                    headers: {Authorization: `${token}`},
                    params: {
                        limit: limit,
                        offset: offset,
                        fav: false,
                        lang: books_lang || '', // Use the destructured variable directly
                    },
                });
                setBooks(response.data.books);
                setTotalPages(response.data.length);
            } catch (error) {
                console.error('Error fetching books', error);
            } finally {
                setLoading(false);
            }
        };

        fetchUser();
        fetchBooks();
    }, [token, page, books_lang]); // Include books_lang in the dependency array

    const handleOpenAnnotation = (id: number) => {
        setOpened((prev) => (prev.includes(id) ? prev.filter((bookId) => bookId !== id) : [...prev, id]));
    };

    const handleFavBook = async (book: Book) => {
        // Логика добавления в избранное
    };

    const handleUpdateBook = async (book: Book) => {
        // Логика обновления книги
    };

    const cover = (book: Book) => {
        return book.cover
            ? `${API_URL}/books-posters/${book.cover}`
            : `${API_URL}/books-posters/no-cover.png`;
    };

    return (
        <Box p={2}>
            <Typography variant="h4" gutterBottom>
                Books List - Page {page}
            </Typography>
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
                    <Grid container spacing={3} justifyContent="center">
                        {books.map((book) => (
                            <Grid item xs={12} key={book.id}>
                                <Box maxWidth={1200} mx="auto">
                                    <Card sx={{ boxShadow: 2 }}>
                                        <Grid container spacing={2}>
                                            <Grid item xs={12} md={9}>
                                                <Grid container spacing={2}>
                                                    <Grid item xs={12} md={4}>
                                                        <CardMedia
                                                            component="img"
                                                            height="300"
                                                            image={cover(book)}
                                                            alt={book.title}
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
                                                                <Typography variant="subtitle1">{t('authors')}:</Typography>
                                                                {book.authors.map((author) => (
                                                                    <Typography key={author.id} variant="body2">
                                                                        &#8226; {author.full_name}
                                                                    </Typography>
                                                                ))}
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
                                                <Box display="flex" flexWrap="wrap" justifyContent="center" gap={1} sx={{ mt: 2 }}>
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
                        <BookPagination totalPages={totalPages} currentPage={parseInt(page || '1', 10)} />
                    </Grid>
                </>
            )}
        </Box>
    );
};

export default BooksList;
