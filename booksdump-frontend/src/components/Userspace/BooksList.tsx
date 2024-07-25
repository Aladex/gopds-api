// src/components/BooksList.tsx
import React, {useState, useEffect, useCallback} from 'react';
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
import {useNavigate} from "react-router-dom";
import BookAnnotation from "../common/BookAnnotation";

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
    const {user, updateUser} = useAuth();
    const {page, id, title} = useParams<{ page: string, id?: string, title?: string }>();
    const [books, setBooks] = useState<Book[]>([]);
    const [totalPages, setTotalPages] = useState(0);
    const [loading, setLoading] = useState(true);
    const {t} = useTranslation();
    const {authorId, authorBook, setAuthorId, clearAuthorBook} = useAuthor();
    const location = useLocation();
    const fav = useFav();
    const navigate = useNavigate();

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
            const response = await fetchWithAuth.get('/books/list', {params});
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
    }, [page, user?.books_lang, id, title, location.pathname, setAuthorId, authorBook, clearAuthorBook, authorId]);

    useEffect(() => {
        fetchBooks().then(r => r);
    }, [page, fetchBooks]);

    const handleFavBook = async (book: Book) => {
        try {
            // Optimistically update the local state
            setBooks(prev => prev.map(b => b.id === book.id ? {...b, fav: !b.fav} : b));

            const response = await fetchWithAuth.post('/books/fav', {book_id: book.id, fav: !book.fav});
            if (response.status === 200) {
                // Fetch updated user data
                const userResponse = await fetchWithAuth.get('/books/self-user');
                if (userResponse.status === 200) {
                    updateUser(userResponse.data);
                    // If user.?have_favs not true, redirect to /books/page/1 - setFav will be false
                    if (!userResponse.data.have_favs) {
                        fav.setFav(false);
                        // Redirect to /books/page/1
                        navigate('/books/page/1');
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

    const downloadBook = async (book: Book, format?: string) => {
        try {
            let url = `/books/getsigned/${format}/${book.id}`;
            const response = await fetchWithAuth.get(url);
            if (response.status === 200) {
                const signedUrl = response.data.result;
                if (signedUrl) {
                    const a = document.createElement('a');
                    a.href = signedUrl;
                    a.download = ''; // Пустой атрибут download позволяет использовать имя файла из URL
                    document.body.appendChild(a);
                    a.click();
                    document.body.removeChild(a);
                }
            }
        } catch (error) {
            console.error('Error getting signed url or downloading book', error);
        }
    };



    const handleUpdateBook = async (book: Book) => {

    }


    const cover = (book: Book) => `${API_URL}/books-posters/${book.path.replace(/[^a-zA-Z0-9]/g, '-')}/${book.id}-${book.format}.jpg`;

    return (
        <Box p={2}>
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
                                                            component="img"
                                                            height="300"
                                                            image={cover(book)}
                                                            alt={book.title}
                                                            sx={{objectFit: 'scale-down'}}
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
                                                <Box display="flex" flexWrap="wrap"
                                                     justifyContent={{xs: 'center', md: 'end'}} gap={1} sx={{mt: 2}}>
                                                    <Button
                                                        variant="contained"
                                                        color="secondary"
                                                        sx={{mb: 1, color: 'white', minWidth: 150}}
                                                        onClick={() => downloadBook(book, 'zip')}
                                                    >
                                                        FB2+ZIP
                                                    </Button>
                                                    <Button
                                                        variant="contained"
                                                        color="secondary"
                                                        sx={{mb: 1, color: 'white', minWidth: 150}}
                                                        onClick={() => downloadBook(book, 'fb2')}
                                                    >
                                                        FB2
                                                    </Button>
                                                    <Button
                                                        variant="contained"
                                                        color="secondary"
                                                        sx={{mb: 1, color: 'white', minWidth: 150}}
                                                        onClick={() => downloadBook(book, 'epub')}
                                                    >
                                                        EPUB
                                                    </Button>
                                                    <Button
                                                        variant="contained"
                                                        color="secondary"
                                                        sx={{mb: 1, color: 'white', minWidth: 150}}
                                                        onClick={() => downloadBook(book, 'mobi')}
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
