import React, { useState, useEffect } from 'react';
import { Grid, Box, Typography, List, ListItemText, ListItemButton, Card, CardContent } from '@mui/material';
import { useParams, useLocation, useNavigate } from 'react-router-dom';
import { fetchWithAuth } from '../../api/config';
import SkeletonCard from "../common/SkeletonCard";
import BookPagination from "../common/BookPagination";
import { useSearchBar } from '../../context/SearchBarContext';
import { useTranslation } from "react-i18next";

interface Book {
    id: number;
    title: string;
}

const CollectionEditor: React.FC = () => {
    const { t } = useTranslation();
    const { id } = useParams<{ id: string }>();
    const [books, setBooks] = useState<Book[]>([]);
    const [loading, setLoading] = useState(true);
    const [totalPages, setTotalPages] = useState(0);
    const navigate = useNavigate();
    const { setSearchItem } = useSearchBar();
    const location = useLocation();
    const baseUrl = window.location.pathname.replace(/\/\d+$/, '');

    useEffect(() => {
        const fetchBooks = async () => {
            setLoading(true);
            try {
                window.scrollTo(0, 0);

                const response = await fetchWithAuth.get('/books/list', {
                    params: {
                        collection: id,
                        limit: 100,
                        offset: 0,
                    },
                });

                const responseData = response.data;
                if (responseData.books && Array.isArray(responseData.books)) {
                    setBooks(responseData.books);
                    setTotalPages(responseData.length);
                }
            } catch (error) {
                console.error('Error fetching books:', error);
                setBooks([]);
            } finally {
                setLoading(false);
            }
        };

        fetchBooks();
    }, [id, location.search]);

    const handleBookClick = (bookId: number) => {
        setSearchItem('');
        navigate(`/books/details/${bookId}`);
    };

    return (
        <Box p={2}>
            {loading ? (
                <Grid container justifyContent="center">
                    <Grid item xs={12}>
                        <Box maxWidth={1200} mx="auto">
                            <Card sx={{ boxShadow: 2, p: 1, my: 1 }}>
                                {Array.from({ length: 10 }).map((_, index) => (
                                    <SkeletonCard key={index} />
                                ))}
                            </Card>
                        </Box>
                    </Grid>
                </Grid>
            ) : books.length === 0 ? (
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
                    <Grid container justifyContent="center" spacing={4}>
                        <Grid item xs={12}>
                            <Box maxWidth={1200} mx="auto">
                                <Card sx={{ boxShadow: 2, p: 1, my: 1 }}>
                                    <Typography variant="h4" align="center">{t('booksInCollection')}</Typography>
                                    <List>
                                        {books.map((book) => (
                                            <ListItemButton key={book.id} onClick={() => handleBookClick(book.id)}>
                                                <ListItemText primary={book.title} />
                                            </ListItemButton>
                                        ))}
                                    </List>
                                </Card>
                            </Box>
                        </Grid>
                    </Grid>
                </>
            )}
        </Box>
    );
};

export default CollectionEditor;