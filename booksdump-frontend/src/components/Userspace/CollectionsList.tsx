import React, { useState, useEffect } from 'react';
import { Grid, Box, Typography, List, ListItemText, Card, ListItemButton, CardContent, Tabs, Tab } from '@mui/material';
import { useParams, useLocation } from 'react-router-dom';
import { fetchWithAuth } from '../../api/config';
import BookPagination from "../common/BookPagination";
import SkeletonCard from "../common/SkeletonCard";
import { useNavigate } from 'react-router-dom';
import { useSearchBar } from '../../context/SearchBarContext';
import { useTranslation } from "react-i18next";

interface Collection {
    id: number;
    name: string;
}

const CollectionsList: React.FC = () => {
    const { t } = useTranslation();
    const { page } = useParams<{ page: string }>();
    const [collections, setCollections] = useState<Collection[]>([]);
    const [loading, setLoading] = useState(true);
    const location = useLocation();
    const [totalPages, setTotalPages] = useState(0);
    const [tab, setTab] = useState('public');
    const baseUrl = window.location.pathname.replace(/\/\d+$/, '');
    const navigate = useNavigate();
    const { setSearchItem } = useSearchBar();

    useEffect(() => {
        const fetchCollections = async () => {
            setLoading(true);
            try {
                window.scrollTo(0, 0);
                const limit = 10;
                const currentPage = parseInt(page || '1', 10);
                const offset = (currentPage - 1) * limit;

                const response = await fetchWithAuth.get(`/books/collections`, {
                    params: {
                        limit,
                        offset,
                        type: tab,
                    },
                });

                const responseData = await response.data;
                if (responseData.collections && Array.isArray(responseData.collections)) {
                    setCollections(responseData.collections);
                    setTotalPages(responseData.length);
                }
            } catch (error) {
                console.error('Error fetching collections:', error);
                setCollections([]);
            } finally {
                setLoading(false);
            }
        };

        fetchCollections().then(r => r);
    }, [tab, location.search, page]);

    const handleCollectionClick = (collectionId: number) => {
        setSearchItem('');
        navigate(`/books/find/collection/${collectionId}/1`);
    };

    const handleTabChange = (event: React.ChangeEvent<{}>, newValue: string) => {
        setTab(newValue);
    };

    return (
        <>
            <Box p={2}>
                <Grid container justifyContent="center">
                    <Grid item xs={12}>
                        <Box maxWidth={1200} mx="auto">
                            <Card sx={{ boxShadow: 2, p: 1, my: 1 }}>
                                <CardContent>
                                    <Tabs
                                        value={tab}
                                        onChange={handleTabChange}
                                        aria-label="collections tabs"
                                        textColor="inherit"
                                        indicatorColor="primary"
                                        TabIndicatorProps={{
                                            style: {
                                                backgroundColor: 'black',
                                            },
                                        }}
                                    >
                                        <Tab
                                            label={t('public')}
                                            value="public"
                                            sx={{
                                                color: tab === 'public' ? 'black' : '#818181',
                                                '&.Mui-selected': {
                                                    color: 'black',
                                                },
                                            }}
                                        />
                                        <Tab
                                            label={t('private')}
                                            value="private"
                                            sx={{
                                                color: tab === 'private' ? 'black' : '#818181',
                                                '&.Mui-selected': {
                                                    color: 'black',
                                                },
                                            }}
                                        />
                                    </Tabs>
                                    {loading ? (
                                        Array.from({ length: 10 }).map((_, index) => (
                                            <SkeletonCard key={index} />
                                        ))
                                    ) : collections.length === 0 ? (
                                        <Typography variant="h6" align="center">{t('noCollectionsFound')}</Typography>
                                    ) : (
                                        <>
                                            <Typography variant="h4" align="center">{t('collections')}</Typography>
                                            <List>
                                                {collections.map((collection) => (
                                                    <ListItemButton key={collection.id} onClick={() => handleCollectionClick(collection.id)}>
                                                        <ListItemText primary={collection.name} />
                                                    </ListItemButton>
                                                ))}
                                            </List>
                                        </>
                                    )}
                                </CardContent>
                            </Card>
                        </Box>
                    </Grid>
                </Grid>
            </Box>
            <Grid container spacing={3} justifyContent="center" sx={{ marginTop: 2 }}>
                <BookPagination totalPages={totalPages} currentPage={parseInt(page || '1', 10)} baseUrl={baseUrl} />
            </Grid>
        </>
    );
};

export default CollectionsList;