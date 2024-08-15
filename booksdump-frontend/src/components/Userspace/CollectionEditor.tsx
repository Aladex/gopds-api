import React, { useState, useEffect } from 'react';
import { Grid, Box, Typography, List, ListItemText, ListItemButton, Card, CardContent } from '@mui/material';
import { useParams, useLocation, useNavigate } from 'react-router-dom';
import { fetchWithAuth } from '../../api/config';
import SkeletonCard from "../common/SkeletonCard";
import { useSearchBar } from '../../context/SearchBarContext';
import { useTranslation } from "react-i18next";
import { DndContext, closestCenter } from '@dnd-kit/core';
import { arrayMove, SortableContext, verticalListSortingStrategy, useSortable } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';

interface Book {
    id: number;
    title: string;
}

const SortableItem = ({ id, title, onClick }: { id: number; title: string; onClick: () => void }) => {
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
            {...listeners}
            onClick={onClick}
        >
            <ListItemText primary={title} />
        </ListItemButton>
    );
};

const CollectionEditor: React.FC = () => {
    const { t } = useTranslation();
    const { id } = useParams<{ id: string }>();
    const [books, setBooks] = useState<Book[]>([]);
    const [loading, setLoading] = useState(true);
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
                                <DndContext collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
                                    <SortableContext items={books} strategy={verticalListSortingStrategy}>
                                        <List>
                                            {books.map((book) => (
                                                <SortableItem
                                                    key={book.id}
                                                    id={book.id}
                                                    title={book.title}
                                                    onClick={() => handleBookClick(book.id)}
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