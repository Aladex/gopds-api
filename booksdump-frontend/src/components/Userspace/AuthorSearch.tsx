import React, { useState, useEffect } from 'react';
import {Grid, Box, Typography, List, ListItemText, Card, ListItemButton, CardContent} from '@mui/material';
import { useParams, useLocation } from 'react-router-dom';
import { fetchWithAuth} from '../../api/config';
import BookPagination from "../common/BookPagination";
import SkeletonCard from "../common/SkeletonCard";
import {useNavigate} from 'react-router-dom';
import { useSearchBar } from '../../context/SearchBarContext';
import { useAuthor} from "../../context/AuthorContext";
import { useTranslation } from "react-i18next";

interface Author {
    id: number;
    full_name: string;
}

const AuthorSearch: React.FC = () => {
    const { t } = useTranslation();
    const {page} = useParams<{ page: string }>();
    const [authors, setAuthors] = useState<Author[]>([]);
    const { author } = useParams<{ author: string }>();
    const [loading, setLoading] = useState(true);
    const location = useLocation();
    const [totalPages, setTotalPages] = useState(0);
    const baseUrl = window.location.pathname.replace(/\/\d+$/, '');
    const navigate = useNavigate();
    const { setSearchItem } = useSearchBar();
    const { clearAuthorBook } = useAuthor();

    useEffect(() => {
        const fetchAuthors = async () => {
            setLoading(true); // Start loading
            try {
                window.scrollTo(0, 0);
                const limit = 10;
                const currentPage = parseInt(page || '1', 10);
                const offset = (currentPage - 1) * limit;

                const response = await fetchWithAuth.get(`/books/authors`, {
                    params: {
                        limit,
                        offset,
                        author: decodeURIComponent(author || ''),
                    },
                });

                const responseData = await response.data; // Convert response to JSON
                if (responseData.authors && Array.isArray(responseData.authors)) {
                    setAuthors(responseData.authors);
                    setTotalPages(responseData.length);
                }
            } catch (error) {
                console.error('Error fetching authors:', error);
                setAuthors([]); // Set to an empty array in case of error
            } finally {
                setLoading(false); // End loading
            }
        };

        if (author) {
            fetchAuthors().then(r => r);
        }
    }, [author, location.search, page]);

    const handleAuthorClick = (authorId: number) => {
        setSearchItem('');
        clearAuthorBook();
        navigate(`/books/find/author/${authorId}/1`);
    };

    return (
        <>
            <Box>
                {loading ? (
                    <Grid item xs={12}>
                        <Box maxWidth={1200} mx="auto">
                            <Card sx={{ boxShadow: 2, p: 2, my: 2 }}>
                                {Array.from({ length: 10 }).map((_, index) => (
                                    <SkeletonCard key={index} />
                                ))}
                            </Card>
                        </Box>
                    </Grid>
                ) : authors.length === 0 ? (
                    <Grid container justifyContent="center">
                        <Grid item xs={12}>
                            <Box maxWidth={1200} mx="auto">
                                <Card sx={{ boxShadow: 2, p: 2, my: 2 }}>
                                    <CardContent>
                                        <Typography variant="h6" align="center">{t('noAuthorsFound')}</Typography>
                                    </CardContent>
                                </Card>
                            </Box>
                        </Grid>
                    </Grid>
                ) : (
                    <Grid container justifyContent="center" spacing={4}>
                        <Grid item xs={12}>
                            <Box maxWidth={1200} mx="auto">
                                <Card sx={{ boxShadow: 2, p: 2, my: 2 }}>
                                    <Typography variant="h4" align="center">{t('authorsSearch')}</Typography>
                                    {authors && authors.length > 0 ? (
                                        <List>
                                            {authors.map((author) => (
                                                <ListItemButton key={author.id} onClick={() => handleAuthorClick(author.id)}>
                                                    <ListItemText primary={author.full_name} />
                                                </ListItemButton>
                                            ))}
                                        </List>
                                    ) : (
                                        <Typography variant="body1" align="center">No authors found</Typography>
                                    )}
                                </Card>
                            </Box>
                        </Grid>
                    </Grid>
                )}
            </Box>
            <Grid container spacing={3} justifyContent="center" sx={{ marginTop: 2 }}>
                <BookPagination totalPages={totalPages} currentPage={parseInt(page || '1', 10)} baseUrl={baseUrl} />
            </Grid>
        </>
    );
};

export default AuthorSearch;