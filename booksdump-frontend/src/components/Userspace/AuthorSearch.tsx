import React, { useState, useEffect } from 'react';
import {Grid, Box, Typography, List, ListItemText, Card, ListItemButton} from '@mui/material';
import { useParams, useLocation } from 'react-router-dom';
import axios from 'axios';
import SearchBar from "../common/SearchBar";
import {API_URL} from '../../api/config';
import {useAuth} from "../../context/AuthContext";
import BookPagination from "../common/BookPagination";
import SkeletonCard from "../common/SkeletonCard";
import {useNavigate} from 'react-router-dom';

interface Author {
    id: number;
    full_name: string;
}

const AuthorSearch: React.FC = () => {
    const {token} = useAuth();
    const {page} = useParams<{ page: string }>();
    const [authors, setAuthors] = useState<Author[]>([]);
    const { author } = useParams<{ author: string }>();
    const [loading, setLoading] = useState(true);
    const location = useLocation();
    const [totalPages, setTotalPages] = useState(0);
    const baseUrl = window.location.pathname.replace(/\/\d+$/, '');
    const navigate = useNavigate();

    useEffect(() => {
        const fetchAuthors = async () => {
            setLoading(true); // Start loading
            try {
                const limit = 10;
                const currentPage = parseInt(page || '1', 10);
                const offset = (currentPage - 1) * limit;

                const response = await axios.get(`${API_URL}/books/authors`, {
                    headers: { Authorization: `${token}` },
                    params: {
                        limit,
                        offset,
                        author: decodeURIComponent(author || ''),
                    }
                });

                // Ensure the response contains the authors field and it's an array
                if (response.data.authors && Array.isArray(response.data.authors)) {
                    setAuthors(response.data.authors);
                    setTotalPages(response.data.length);
                } else {
                    // Handle the case where authors is not in the expected format or undefined
                    console.error('Unexpected response format:', response.data);
                    setAuthors([]); // Set to an empty array to avoid runtime errors
                }
            } catch (error) {
                console.error('Error fetching authors:', error);
                setAuthors([]); // Set to an empty array in case of error
            } finally {
                setLoading(false); // End loading
            }
        };

        if (author) {
            fetchAuthors();
        }
    }, [author, location.search, page, token]);

    const handleAuthorClick = (authorId: number) => {
        navigate(`/books/find/author/${authorId}/1`);
    };

    return (
        <>
            <Box p={2}>
                <SearchBar />
                {loading ? (
                    <Grid item xs={12}>
                        <Box maxWidth={1200} mx="auto">
                            <Card sx={{ boxShadow: 2, p: 1, my: 1 }}>
                                {Array.from({ length: 10 }).map((_, index) => (
                                    <SkeletonCard key={index} />
                                ))}
                            </Card>
                        </Box>
                    </Grid>
                ) : authors.length === 0 ? (
                    <Typography variant="h6" align="center">No authors found</Typography>
                ) : (
                    <Grid container justifyContent="center" spacing={4}>
                        <Grid item xs={12}>
                            <Box maxWidth={1200} mx="auto">
                                <Card sx={{ boxShadow: 2, p: 1, my: 1 }}>
                                    <Typography variant="h4" align="center">Authors</Typography>
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