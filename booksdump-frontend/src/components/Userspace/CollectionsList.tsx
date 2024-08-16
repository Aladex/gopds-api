import React, { useState, useEffect } from 'react';
import {
    Grid, Box, Typography, List, ListItemText, Card, ListItemButton, CardContent, Tabs, Tab, IconButton, Dialog, DialogTitle, DialogContent, DialogActions, Button
} from '@mui/material';
import { Add, Edit, Delete, Add as AddIcon } from '@mui/icons-material';
import { useParams, useLocation, useNavigate } from 'react-router-dom';
import { fetchWithAuth } from '../../api/config';
import BookPagination from "../common/BookPagination";
import SkeletonCard from "../common/SkeletonCard";
import { useSearchBar } from '../../context/SearchBarContext';
import { useTranslation } from "react-i18next";
import { StyledTextField } from "../StyledDataItems";
import { useAuth } from '../../context/AuthContext';

interface Collection {
    id: number;
    name: string;
    is_public: boolean;
    created_at: string;
    updated_at: string;
    vote_count: number;
}

const CollectionsList: React.FC = () => {
    const { t } = useTranslation();
    const { page } = useParams<{ page: string }>();
    const [collections, setCollections] = useState<Collection[]>([]);
    const [loading, setLoading] = useState(true);
    const location = useLocation();
    const [totalPages, setTotalPages] = useState(0);
    const [tab, setTab] = useState(location.pathname.includes('/private-collections') ? 'private' : 'public');
    const [open, setOpen] = useState(false);
    const [newCollectionName, setNewCollectionName] = useState('');
    const navigate = useNavigate();
    const { setSearchItem } = useSearchBar();
    const [collectionToDelete, setCollectionToDelete] = useState<number | null>(null);
    const [openDeleteDialog, setOpenDeleteDialog] = useState(false);
    const { login } = useAuth();

    const handleOpenDeleteDialog = (collectionId: number) => {
        setCollectionToDelete(collectionId);
        setOpenDeleteDialog(true);
    };

    const handleCloseDeleteDialog = () => {
        setOpenDeleteDialog(false);
        setCollectionToDelete(null);
    };

    const handleDeleteCollection = async () => {
        if (collectionToDelete !== null) {
            try {
                await fetchWithAuth.delete(`/books/collection/${collectionToDelete}`);
                setCollections(prevCollections => prevCollections.filter(c => c.id !== collectionToDelete));
                handleCloseDeleteDialog();
                login(); // Refresh user data
            } catch (error) {
                console.error('Error deleting collection:', error);
            }
        }
    };

    useEffect(() => {
        const fetchCollections = async () => {
            setLoading(true);
            try {
                window.scrollTo(0, 0);
                const limit = 10;
                const currentPage = parseInt(page || '1', 10);
                const offset = (currentPage - 1) * limit;

                const response = await fetchWithAuth.get('/books/collections', {
                    params: { limit, offset, private: tab === 'private' },
                });

                const responseData = await response.data;
                setCollections(responseData || []);
                setTotalPages(Math.ceil((responseData?.length || 0) / limit));
            } catch (error) {
                console.error('Error fetching collections:', error);
                setCollections([]);
            } finally {
                setLoading(false);
            }
        };

        fetchCollections();
    }, [tab, page]);

    const handleEditCollectionClick = (collectionId: number) => {
        setSearchItem('');
        navigate(`/collection/${collectionId}/edit`);
    };

    const handleCollectionClick = (collectionId: number) => {
        setSearchItem('');
        navigate(`/books/find/collection/${collectionId}/1`);
    };

    const handleTabChange = (event: React.ChangeEvent<{}>, newValue: string) => {
        setTab(newValue);
        navigate(newValue === 'private' ? '/private-collections/1' : '/public-collections/1');
    };

    const handleOpen = () => setOpen(true);
    const handleClose = () => setOpen(false);

    const handleCreateCollection = async () => {
        try {
            await fetchWithAuth.post('/books/create-collection', { name: newCollectionName });
            setOpen(false);
            setNewCollectionName('');
            const response = await fetchWithAuth.get(`/books/collections`, { params: { limit: 10, offset: 0, private: tab === 'private' } });
            const responseData = await response.data;
            setCollections(responseData.collections || []);
            setTotalPages(responseData.length || 0);
            login(); // Refresh user data
        } catch (error) {
            console.error('Error creating collection:', error);
        }
    };

    const handleVote = async (collectionId: number, vote: boolean) => {
        try {
            const response = await fetchWithAuth.post(`/books/vote-collection/${collectionId}`, { vote });
            const updatedCollection = response.data;
            setCollections(prevCollections => prevCollections.map(collection => collection.id === collectionId ? updatedCollection : collection));
        } catch (error) {
            console.error('Error voting on collection:', error);
        }
    };

    return (
        <>
            <Box>
                <Grid container justifyContent="center">
                    <Grid item xs={12}>
                        <Box maxWidth={1200} mx="auto">
                            <Card sx={{ boxShadow: 2, p: 1, my: 1 }}>
                                <CardContent>
                                    <Box display="flex" justifyContent="space-between" alignItems="center">
                                        <Tabs
                                            value={tab}
                                            onChange={handleTabChange}
                                            aria-label="collections tabs"
                                            textColor="inherit"
                                            indicatorColor="primary"
                                            TabIndicatorProps={{ style: { backgroundColor: 'black' } }}
                                        >
                                            <Tab label={t('public')} value="public" sx={{ color: tab === 'public' ? 'black' : '#818181', '&.Mui-selected': { color: 'black' } }} />
                                            <Tab label={t('private')} value="private" sx={{ color: tab === 'private' ? 'black' : '#818181', '&.Mui-selected': { color: 'black' } }} />
                                        </Tabs>
                                        {tab === 'private' && (
                                            <IconButton color="secondary" onClick={handleOpen}>
                                                <Add />
                                            </IconButton>
                                        )}

                                    </Box>
                                    {loading ? (
                                        Array.from({ length: 10 }).map((_, index) => <SkeletonCard key={index} />)
                                    ) : collections.length === 0 ? (
                                        <Typography variant="h6" align="center">{t('noCollectionsFound')}</Typography>
                                    ) : (
                                        <>
                                            <Typography variant="h4" align="center">{t('collections')}</Typography>
                                            <List>
                                                {collections.map((collection) => (
                                                    <ListItemButton key={collection.id} onClick={() => handleCollectionClick(collection.id)}>
                                                        <ListItemText primary={collection.name} secondary={new Date(collection.updated_at).toLocaleDateString()} />
                                                        {tab !== 'public' ? (
                                                            <>
                                                                <IconButton color="secondary" onClick={(e) => { e.stopPropagation(); handleEditCollectionClick(collection.id); }}>
                                                                    <Edit />
                                                                </IconButton>
                                                                <IconButton color="secondary" onClick={(e) => { e.stopPropagation(); handleOpenDeleteDialog(collection.id); }}>
                                                                    <Delete />
                                                                </IconButton>
                                                            </>
                                                        ) : (
                                                            <Box display="flex" alignItems="center">
                                                                <IconButton color="secondary" onClick={(e) => { e.stopPropagation(); handleVote(collection.id, false); }}>
                                                                    <Delete />
                                                                </IconButton>
                                                                <Typography variant="body2" sx={{ margin: '0 8px' }}>{collection.vote_count}</Typography>
                                                                <IconButton color="secondary" onClick={(e) => { e.stopPropagation(); handleVote(collection.id, true); }}>
                                                                    <AddIcon />
                                                                </IconButton>
                                                            </Box>
                                                        )}
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
                <BookPagination totalPages={totalPages} currentPage={parseInt(page || '1', 10)} baseUrl={window.location.pathname.replace(/\/\d+$/, '')} />
            </Grid>
            <Dialog open={open} onClose={handleClose}>
                <DialogTitle>{t('createCollection')}</DialogTitle>
                <DialogContent>
                    <StyledTextField autoFocus margin="dense" label={t('collectionName')} type="text" fullWidth value={newCollectionName} onChange={(e) => setNewCollectionName(e.target.value)} />
                </DialogContent>
                <DialogActions>
                    <Button onClick={handleClose} color="secondary">{t('cancel')}</Button>
                    <Button onClick={handleCreateCollection} color="secondary">{t('create')}</Button>
                </DialogActions>
            </Dialog>
            <Dialog open={openDeleteDialog} onClose={handleCloseDeleteDialog}>
                <DialogTitle>{t('confirmDeleteCollection')}</DialogTitle>
                <DialogContent>
                    <Typography>{t('areYouSureYouWantToDeleteThisCollection')}</Typography>
                </DialogContent>
                <DialogActions>
                    <Button onClick={handleCloseDeleteDialog} color="secondary">{t('cancel')}</Button>
                    <Button onClick={handleDeleteCollection} color="secondary">{t('delete')}</Button>
                </DialogActions>
            </Dialog>
        </>
    );
};

export default CollectionsList;