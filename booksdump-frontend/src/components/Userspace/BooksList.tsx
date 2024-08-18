import '../styles/BooksList.css';
import React, { useReducer, useEffect, useRef } from 'react';
import {
    LinearProgress,
    Typography,
    Box,
    Grid,
    Card,
    CardContent,
    CardMedia,
    Button,
    Menu,
    MenuItem,
    Checkbox,
    ListItemText,
    CardActions, IconButton
} from '@mui/material';
import { useParams, useLocation, useNavigate } from 'react-router-dom';
import { API_URL, fetchWithAuth } from '../../api/config';
import { useAuth } from '../../context/AuthContext';
import { useTranslation } from 'react-i18next';
import StarIcon from '@mui/icons-material/Star';
import StarOutlineIcon from '@mui/icons-material/StarOutline';
import CheckCircleIcon from '@mui/icons-material/CheckCircle';
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutline';
import BookPagination from "../common/BookPagination";
import SkeletonCard from "../common/SkeletonCard";
import AuthorsList from "../common/AuthorsList";
import { useAuthor } from "../../context/AuthorContext";
import CategotiesList from "../common/CategotiesList";
import { useFav } from "../../context/FavContext";
import BookAnnotation from "../common/BookAnnotation";
import CoverLoader from "../common/CoverLoader";
import { format } from 'date-fns';
import { useState } from 'react';
import CollectionCard from '../common/CollectionCard';

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
    favorite_count: number;
}

interface Collection {
    id: number;
    name: string;
    is_public: boolean;
    created_at: string;
    updated_at: string;
    rating: number;
    book_ids: number[];
    book_is_in_collection: boolean;
}

interface State {
    books: Book[];
    loading: boolean;
    totalPages: number;
    bookCollections: Collection[];
    menuLoading: boolean;
    selectedBook: number | null;
}

type Action =
    | { type: 'FETCH_SUCCESS'; payload: { books: Book[]; totalPages: number } }
    | { type: 'FETCH_ERROR' }
    | { type: 'SET_LOADING' }
    | { type: 'SET_MENU_LOADING'; payload: boolean }
    | { type: 'SET_SELECTED_BOOK'; payload: number | null }
    | { type: 'UPDATE_BOOK'; payload: Book }
    | { type: 'TOGGLE_FAV'; payload: number }
    | { type: 'FETCH_COLLECTIONS_SUCCESS'; payload: Collection[] };

const initialState: State = {
    books: [],
    loading: true,
    totalPages: 0,
    bookCollections: [],
    menuLoading: false,
    selectedBook: null,
};

function reducer(state: State, action: Action): State {
    switch (action.type) {
        case 'FETCH_SUCCESS':
            return {
                ...state,
                books: action.payload.books,
                totalPages: action.payload.totalPages,
                loading: false,
            };
        case 'FETCH_ERROR':
            return { ...state, loading: false };
        case 'SET_LOADING':
            return { ...state, loading: true };
        case 'SET_MENU_LOADING':
            return { ...state, menuLoading: action.payload };
        case 'SET_SELECTED_BOOK':
            return { ...state, selectedBook: action.payload };
        case 'UPDATE_BOOK':
            return {
                ...state,
                books: state.books.map((b) =>
                    b.id === action.payload.id ? action.payload : b
                ),
            };
        case 'TOGGLE_FAV':
            return {
                ...state,
                books: state.books.map((b) =>
                    b.id === action.payload ? { ...b, fav: !b.fav } : b
                ),
            };
        case 'FETCH_COLLECTIONS_SUCCESS':
            return {
                ...state,
                bookCollections: action.payload,
            };
        default:
            return state;
    }
}

const BooksList: React.FC = () => {
    const { user } = useAuth();
    const { page, id, title } = useParams<{ page: string, id?: string, title?: string }>();
    const { t } = useTranslation();
    const { authorId, authorBook, setAuthorId, clearAuthorBook } = useAuthor();
    const location = useLocation();
    const fav = useFav();
    const navigate = useNavigate();
    const prevLangRef = useRef(user?.books_lang);
    const [state, dispatch] = useReducer(reducer, initialState);
    const [anchorEl, setAnchorEl] = React.useState<null | HTMLElement>(null);
    const [collection, setCollection] = useState<Collection | null>(null);

    const formatDate = (dateString: string) => {
        if (dateString === "") {
            return t('unknownAddDate'); // Или t('unknownPublicationDate'), в зависимости от контекста
        }
        const timestamp = Date.parse(dateString);
        if (isNaN(timestamp)) {
            return dateString;
        }
        const date = new Date(timestamp);
        return format(date, "dd.MM.yyyy, HH:mm");
    };

    useEffect(() => {
        const fetchCollection = async () => {
            if (id) {
                try {
                    const response = await fetchWithAuth.get(`/books/collection/${id}`);
                    if (response.status === 200) {
                        setCollection(response.data);
                    } else {
                        console.error('Failed to fetch collection');
                    }
                } catch (error) {
                    console.error('Error fetching collection', error);
                }
            }
        };

        fetchCollection();

    }, [page, user?.books_lang, id, title, location.pathname, setAuthorId, clearAuthorBook, authorId, authorBook, navigate]);


    useEffect(() => {
        // Update the `getParams` function in `src/components/Userspace/BooksList.tsx`
        const getParams = () => {
            const limit = 10;
            const currentPage = parseInt(page || '1', 10);
            const offset = (currentPage - 1) * limit;
            let params: Record<string, any> = { limit, offset, lang: user?.books_lang || '' };

            if (location.pathname.includes('/books/find/author/') && id) {
                params.author = id;
                setAuthorId(id);
                if (authorBook) params.title = authorBook;
            } else if (location.pathname.includes('/books/find/category/') && id) {
                params.series = id;
                clearAuthorBook();
            } else if (location.pathname.includes('/books/find/title/') && title) {
                params.title = decodeURIComponent(title);
                if (authorId) params.author = authorId;
                clearAuthorBook();
            } else if (location.pathname.includes('/books/find/collection/') && id) {
                params.collection = id;
                clearAuthorBook();
            }

            if (location.pathname.includes('/books/favorite')) {
                params.fav = true;
                clearAuthorBook();
            }

            if (location.pathname.includes('/books/users/favorites')) {
                params.users_favorites = true;
                clearAuthorBook();
            }

            return params;
        };

        const fetchBooks = async () => {
            if (prevLangRef.current !== user?.books_lang && page !== '1') {
                navigate('/books/page/1');
                return;
            }

            dispatch({ type: 'SET_LOADING' });

            try {
                const params = getParams();
                const response = await fetchWithAuth.get('/books/list', { params });
                if (response.status === 200) {
                    const data = response.data;
                    dispatch({ type: 'FETCH_SUCCESS', payload: { books: data.books, totalPages: data.length } });
                } else {
                    console.error('Failed to fetch books');
                    dispatch({ type: 'FETCH_ERROR' });
                }
            } catch (error) {
                console.error('Error fetching books', error);
                dispatch({ type: 'FETCH_ERROR' });
            }
        };

        fetchBooks();
    }, [page, user?.books_lang, id, title, location.pathname, setAuthorId, clearAuthorBook, authorId, authorBook, navigate]);

    const handleFavBook = async (book: Book) => {
        try {
            dispatch({ type: 'TOGGLE_FAV', payload: book.id });

            const response = await fetchWithAuth.post('/books/fav', { book_id: book.id, fav: !book.fav });
            if (response.status === 200) {
                const userResponse = await fetchWithAuth.get('/books/self-user');
                if (userResponse.status === 200) {
                    fav.setFavEnabled(userResponse.data.have_favs);
                    if (location.pathname.includes('/books/favorite') && !userResponse.data.have_favs) {
                        navigate('/books/page/1');
                        fav.setFav(false);
                    }
                } else {
                    console.error('Failed to fetch updated user data');
                }
            } else {
                console.error('Failed to update favorite status');
                dispatch({ type: 'TOGGLE_FAV', payload: book.id });
            }
        } catch (error) {
            console.error('Error favoriting book', error);
            dispatch({ type: 'TOGGLE_FAV', payload: book.id });
        }
    };

    const handleUpdateBook = async (book: Book) => {
        const newBook = { ...book, approved: !book.approved };
        try {
            dispatch({ type: 'UPDATE_BOOK', payload: newBook });
            const response = await fetchWithAuth.post('/admin/update-book', newBook);
            if (response.status !== 200) throw new Error('Failed to update book');
        } catch (error) {
            console.error('Error updating book', error);
            dispatch({ type: 'UPDATE_BOOK', payload: book });
        }
    };

    const handleMenuOpen = async (event: React.MouseEvent<HTMLElement>, bookId: number) => {
        setAnchorEl(event.currentTarget);
        dispatch({ type: 'SET_SELECTED_BOOK', payload: bookId });
        dispatch({ type: 'SET_MENU_LOADING', payload: true });

        try {
            const response = await fetchWithAuth.get(`/books/collections?book_id=${bookId}&private=true`);
            if (response.status === 200) {
                const collections = response.data;
                dispatch({ type: 'FETCH_COLLECTIONS_SUCCESS', payload: collections });
            } else {
                console.error('Failed to fetch collections for the book');
            }
        } catch (error) {
            console.error('Error fetching collections for the book:', error);
        } finally {
            dispatch({ type: 'SET_MENU_LOADING', payload: false });
        }
    };

    const handleMenuClose = () => {
        setAnchorEl(null);
        dispatch({ type: 'SET_SELECTED_BOOK', payload: null });
    };

    const cover = (book: Book) => `${API_URL}/books-posters/${book.path.replace(/\W/g, '-')}/${book.filename.replace(/\W/g, '-')}.jpg`;

    const handleToggleCollection = async (collectionId: number) => {
        if (state.selectedBook !== null) {
            const collectionIndex = state.bookCollections.findIndex(collection => collection.id === collectionId);
            if (collectionIndex !== -1) {
                const collection = state.bookCollections[collectionIndex];
                const updatedCollections = [...state.bookCollections];
                updatedCollections[collectionIndex] = {
                    ...collection,
                    book_is_in_collection: !collection.book_is_in_collection,
                };

                // Optimistically update the state
                dispatch({ type: 'FETCH_COLLECTIONS_SUCCESS', payload: updatedCollections });

                try {
                    if (collection.book_is_in_collection) {
                        await fetchWithAuth.post('/books/remove-from-collection', {
                            book_id: state.selectedBook,
                            collection_id: collectionId,
                        });
                    } else {
                        await fetchWithAuth.post('/books/add-to-collection', {
                            book_id: state.selectedBook,
                            collection_id: collectionId,
                        });
                    }
                } catch (error) {
                    console.error('Error toggling book in collection:', error);
                    // Revert the state change if the API call fails
                    updatedCollections[collectionIndex] = collection;
                    dispatch({ type: 'FETCH_COLLECTIONS_SUCCESS', payload: updatedCollections });
                }
            }
        }
    };

    const renderMenu = () => (
        <Menu
            id="simple-menu"
            anchorEl={anchorEl}
            keepMounted
            open={Boolean(anchorEl)}
            onClose={handleMenuClose}
        >
            {state.menuLoading ? (
                <MenuItem disabled>
                    <Box sx={{ width: '100%', padding: '0 100px' }}>
                        <LinearProgress color={'secondary'} />
                    </Box>
                </MenuItem>
            ) : (
                state.bookCollections.map((collection) => (
                    <MenuItem
                        key={collection.id}
                        onClick={() => handleToggleCollection(collection.id)}
                    >
                        <Checkbox
                            checked={collection.book_is_in_collection || false}
                            color="secondary"
                        />
                        <ListItemText primary={collection.name} />
                    </MenuItem>
                ))
            )}
        </Menu>
    );

    return (
        <Box>
            {state.loading ? (
                Array.from({ length: 10 }).map((_, index) => (
                    <Grid item xs={12} key={index}>
                        <Box maxWidth={1200} mx="auto">
                            <SkeletonCard />
                        </Box>
                    </Grid>
                ))
            ) : state.books.length === 0 ? (
                <Grid container justifyContent="center">
                    <Grid item xs={12}>
                        <Box maxWidth={1200} mx="auto">
                            <Card sx={{ boxShadow: 2, p: 2, my: 2 }}>
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
                        {location.pathname.includes('/books/find/collection/') && collection && (
                            <CollectionCard collection={collection} />
                        )}
                        {state.books.map((book) => (
                            <Grid item xs={12} key={book.id}>
                                <Box maxWidth={1200} mx="auto">
                                    <Card sx={{ boxShadow: 2, p: 2, my: 2 }}>
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
                                                                {t('bookPublished')}: {formatDate(book.docdate)}
                                                            </Typography>
                                                            <Typography variant="body2" color="textSecondary"
                                                                        sx={{ display: 'flex', alignItems: 'center' }}>
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
                                                                <AuthorsList authors={book.authors} />
                                                            </Box>
                                                            <Box mt={2}>
                                                                <CategotiesList categories={book.series} />
                                                            </Box>
                                                        </CardContent>
                                                    </Grid>
                                                </Grid>
                                                <CardContent>
                                                    {book.annotation ? (
                                                        <BookAnnotation annotation={book.annotation} />
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
                                        <CardActions sx={{ justifyContent: 'space-between' }}>
                                            <Box sx={{ display: 'flex', justifyContent: 'space-between', width: '100%' }}>
                                                <Box>
                                                    {user?.collections && Array.isArray(user.collections) && user.collections.length > 0 && (
                                                        <>
                                                            <Button
                                                                aria-controls="simple-menu"
                                                                aria-haspopup="true"
                                                                onClick={(event) => handleMenuOpen(event, book.id)}
                                                                color="secondary"
                                                            >
                                                                {t('addToCollection')}
                                                            </Button>
                                                            {renderMenu()}
                                                        </>
                                                    )}
                                                </Box>
                                                <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
                                                    {user?.is_superuser && (
                                                        <IconButton onClick={() => handleUpdateBook(book)}>
                                                            {book.approved ? <CheckCircleIcon /> : <CheckCircleOutlineIcon />}
                                                        </IconButton>
                                                    )}
                                                    <IconButton onClick={() => handleFavBook(book)}>
                                                        {book.fav ? <StarIcon /> : <StarOutlineIcon />}
                                                    </IconButton>
                                                </Box>
                                            </Box>
                                        </CardActions>
                                    </Card>
                                </Box>
                            </Grid>
                        ))}
                    </Grid>
                    <Grid container spacing={3} justifyContent="center" sx={{ marginTop: 2 }}>
                        <BookPagination totalPages={state.totalPages} currentPage={parseInt(page as string)}
                                        baseUrl={location.pathname} />
                    </Grid>
                </>
            )}
        </Box>
    );
};

export default BooksList;

