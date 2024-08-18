import React, { useState, useEffect } from 'react';
import { Grid, Box, Typography, List, ListItemText, ListItemButton, Card, IconButton, Switch, FormControlLabel, Button, Drawer, Divider, Snackbar, Alert } from '@mui/material';
import { useParams, useLocation, useNavigate } from 'react-router-dom';
import { fetchWithAuth } from '../../api/config';
import SkeletonCard from "../common/SkeletonCard";
import { useSearchBar } from '../../context/SearchBarContext';
import { useTranslation } from "react-i18next";
import { DndContext, closestCenter } from '@dnd-kit/core';
import { arrayMove, SortableContext, verticalListSortingStrategy, useSortable } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { ArrowBack, DragHandle, Delete, Settings } from '@mui/icons-material';
import { StyledTextField } from "../StyledDataItems";

interface Book {
    id: number;
    title: string;
}

const SortableItem = ({ id, title, onClick, onDelete, onSettingsClick }: { id: number; title: string; onClick: () => void; onDelete: () => void; onSettingsClick: () => void }) => {
    const { attributes, listeners, setNodeRef, transform, transition } = useSortable({ id });

    const style = {
        transform: CSS.Transform.toString(transform),
        transition,
        cursor: 'grab',
    };

    return (
        <ListItemButton
            ref={setNodeRef}
            style={style}
            {...attributes}
            onClick={onClick}
        >
            <Box {...listeners} sx={{ cursor: 'grab', display: 'flex', alignItems: 'center', paddingRight: 1 }}>
                <DragHandle />
            </Box>
            <ListItemText primary={title} />
            <IconButton edge="end" onClick={(e) => { e.stopPropagation(); onDelete(); }}>
                <Delete />
            </IconButton>
        </ListItemButton>
    );
};

const CollectionEditor: React.FC = () => {
    const { t } = useTranslation();
    const { id } = useParams<{ id: string }>();
    const [books, setBooks] = useState<Book[]>([]);
    const [loading, setLoading] = useState(true);
    const [collectionName, setCollectionName] = useState('');
    const [isPublic, setIsPublic] = useState(false);
    const [settingsOpen, setSettingsOpen] = useState(false);
    const [tempCollectionName, setTempCollectionName] = useState(collectionName);
    const [tempIsPublic, setTempIsPublic] = useState(isPublic);
    const [snackbarOpen, setSnackbarOpen] = useState(false);
    const navigate = useNavigate();
    const { setSearchItem } = useSearchBar();
    const location = useLocation();
    const disablePublicSwitch = books.length < 2;

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
                }

                const collectionResponse = await fetchWithAuth.get(`/books/collection/${id}`);
                const collectionData = collectionResponse.data;
                setCollectionName(collectionData.name);
                setTempCollectionName(collectionData.name);
                setIsPublic(collectionData.is_public);
                setTempIsPublic(collectionData.is_public);
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

    const handleDeleteBook = async (bookId: number) => {
        try {
            await fetchWithAuth.post('/books/remove-from-collection', { collection_id: id ? parseInt(id, 10) : 1, book_id: bookId });
            const updatedBooks = books.filter(book => book.id !== bookId);
            setBooks(updatedBooks);

            if (updatedBooks.length < 2 && tempIsPublic) {
                setTempIsPublic(false);
            }
        } catch (error) {
            console.error('Error deleting book from collection:', error);
        }
    };


    const handleDragEnd = async (event: any) => {
        const { active, over } = event;

        if (active.id !== over.id) {
            const oldIndex = books.findIndex((item) => item.id === active.id);
            const newIndex = books.findIndex((item) => item.id === over.id);

            setBooks((items) => {
                return arrayMove(items, oldIndex, newIndex);
            });

            try {
                await fetchWithAuth.post('/books/update-book-position', {
                    collection_id: id ? parseInt(id, 10) : 1,
                    new_position: newIndex + 1,
                    book_id: active.id,
                });
            } catch (error) {
                console.error('Error updating book position:', error);
            }
        }
    };

    const toggleSettingsDrawer = () => {
        setSettingsOpen(!settingsOpen);
    };

    const handleSaveChanges = async () => {
        try {
            await fetchWithAuth.post(`/books/update-collection/${id}`, {
                name: tempCollectionName,
                is_public: tempIsPublic,
            });
            setCollectionName(tempCollectionName);
            setIsPublic(tempIsPublic);
            toggleSettingsDrawer();
            setSnackbarOpen(true);
        } catch (error) {
            console.error('Error updating collection:', error);
        }
    };

    const handleSnackbarClose = () => {
        setSnackbarOpen(false);
    };

    return (
        <Box>
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
            ) : (
                <Grid container justifyContent="center" spacing={4}>
                    <Grid item xs={12}>
                        <Box maxWidth={1200} mx="auto">
                            <Card sx={{ boxShadow: 2, p: 1, my: 1 }}>
                                <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                                    <IconButton edge="start" onClick={() => navigate('/private-collections/1')} sx={{ ml: 1 }}>
                                        <ArrowBack />
                                    </IconButton>
                                    <Typography variant="h4" align="center" sx={{ flexGrow: 1 }}>{t('booksInCollection')}</Typography>
                                    <IconButton edge="end" onClick={toggleSettingsDrawer} sx={{ mr: 1 }}>
                                        <Settings />
                                    </IconButton>
                                </Box>
                                {books.length > 0 ? (
                                    <DndContext collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
                                        <SortableContext items={books} strategy={verticalListSortingStrategy}>
                                            <List>
                                                {books.map((book) => (
                                                    <SortableItem
                                                        key={book.id}
                                                        id={book.id}
                                                        title={book.title}
                                                        onClick={() => handleBookClick(book.id)}
                                                        onDelete={() => handleDeleteBook(book.id)}
                                                        onSettingsClick={toggleSettingsDrawer}
                                                    />
                                                ))}
                                            </List>
                                        </SortableContext>
                                    </DndContext>
                                ) : (
                                    <Box sx={{ textAlign: 'center', padding: 2 }}>
                                        <Typography variant="body1" sx={{ marginBottom: 2 }}>
                                            {t('noBooksInstruction')}
                                        </Typography>
                                    </Box>
                                )}
                            </Card>
                        </Box>
                    </Grid>
                </Grid>
            )}
            <Drawer
                anchor="right"
                open={settingsOpen}
                onClose={toggleSettingsDrawer}
            >
                <Box sx={{ width: 300, padding: 2 }}>
                    <Typography variant="h6">{t('settings')}</Typography>
                    <Divider sx={{ marginBottom: 2 }} />

                    <StyledTextField
                        label={t('collectionName')}
                        value={tempCollectionName}
                        onChange={(e) => setTempCollectionName(e.target.value)}
                        margin="normal"
                        fullWidth
                    />
                    <FormControlLabel
                        control={
                            <Switch
                                checked={tempIsPublic}
                                onChange={(e) => setTempIsPublic(e.target.checked)}
                                color="secondary"
                                disabled={disablePublicSwitch}
                            />
                        }
                        label={t('isPublic')}
                        sx={{ marginTop: 2 }}
                    />
                    {disablePublicSwitch && (
                        <Typography variant="body2" color="textSecondary" sx={{ fontSize: '0.75rem', marginTop: 1 }}>
                            {t('publicSwitchInstruction')}
                        </Typography>
                    )}

                    <Button
                        variant="contained"
                        color="primary"
                        onClick={handleSaveChanges}
                        fullWidth
                        sx={{ marginTop: 2 }}
                    >
                        {t('saveChanges')}
                    </Button>
                </Box>
            </Drawer>

            <Snackbar
                open={snackbarOpen}
                autoHideDuration={6000}
                onClose={handleSnackbarClose}
            >
                <Alert onClose={handleSnackbarClose} severity="success" sx={{ width: '100%' }}>
                    {t('collectionUpdatedSuccessfully')}
                </Alert>
            </Snackbar>
        </Box>
    );
};

export default CollectionEditor;