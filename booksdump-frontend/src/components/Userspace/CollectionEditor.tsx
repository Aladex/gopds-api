import React, { useState, useEffect } from 'react';
import { Grid, Box, Typography, List, ListItemText, ListItemButton, Card, CardContent, IconButton, Switch, FormControlLabel, Button } from '@mui/material';
import { useParams, useLocation, useNavigate } from 'react-router-dom';
import { fetchWithAuth } from '../../api/config';
import SkeletonCard from "../common/SkeletonCard";
import { useSearchBar } from '../../context/SearchBarContext';
import { useTranslation } from "react-i18next";
import { DndContext, closestCenter } from '@dnd-kit/core';
import { arrayMove, SortableContext, verticalListSortingStrategy, useSortable } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { DragHandle, Delete } from '@mui/icons-material';
import { StyledTextField } from "../StyledDataItems";

interface Book {
    id: number;
    title: string;
}

const SortableItem = ({ id, title, onClick, onDelete }: { id: number; title: string; onClick: () => void; onDelete: () => void }) => {
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
    const navigate = useNavigate();
    const { setSearchItem } = useSearchBar();
    const location = useLocation();

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
                setIsPublic(collectionData.is_public);
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
            setBooks(books.filter(book => book.id !== bookId));
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

    const handleSaveChanges = async () => {
        try {
            await fetchWithAuth.post(`/books/update-collection/${id}`, {
                name: collectionName,
                is_public: isPublic,
            });
            alert('Collection updated successfully');
        } catch (error) {
            console.error('Error updating collection:', error);
        }
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
                <Grid container justifyContent="center" spacing={4}>
                    <Grid item xs={12}>
                        <Box maxWidth={1200} mx="auto">
                            <Card sx={{ boxShadow: 2, p: 1, my: 1 }}>
                                <Typography variant="h4" align="center">{t('booksInCollection')}</Typography>
                                <Box display="flex" alignItems="center" justifyContent="space-between" mb={2}>
                                    <Grid container spacing={2}>
                                        <Grid item xs={6} />
                                        <Grid item xs={6} display="flex" alignItems="center">
                                            <StyledTextField
                                                label={t('collectionName')}
                                                value={collectionName}
                                                onChange={(e) => setCollectionName(e.target.value)}
                                                margin="normal"
                                                sx={{ flexGrow: 1, marginRight: 2 }}
                                            />
                                            <FormControlLabel
                                                control={
                                                    <Switch
                                                        checked={isPublic}
                                                        onChange={(e) => setIsPublic(e.target.checked)}
                                                        color="secondary"
                                                    />
                                                }
                                                label={t('isPublic')}
                                                sx={{ marginRight: 2 }}
                                            />
                                            <Button variant="contained" color="primary" onClick={handleSaveChanges}>
                                                {t('saveChanges')}
                                            </Button>
                                        </Grid>
                                    </Grid>
                                </Box>
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
                                                />
                                            ))}
                                        </List>
                                    </SortableContext>
                                </DndContext>
                            </Card>
                        </Box>
                    </Grid>
                </Grid>
            )}
        </Box>
    );
};

export default CollectionEditor;
