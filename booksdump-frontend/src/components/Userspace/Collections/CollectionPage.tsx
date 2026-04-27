import React, { useEffect, useState } from 'react';
import {
    Alert,
    Box,
    Card,
    CardContent,
    CardMedia,
    Stack,
    Typography,
} from '@mui/material';
import { Link as RouterLink, useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { API_URL } from '../../../api/config';
import { getPublicCollection, PublicCollectionBook, PublicCollectionDetail } from './api';

// coverPath strips dots and leading slashes the way the rest of the userspace
// builds book-posters URLs (see Userspace/BooksList.tsx).
const coverPath = (value: string) => value.replaceAll('.', '-').replace(/^\/+/, '');

const BookCard: React.FC<{ book: PublicCollectionBook }> = ({ book }) => {
    const cover =
        book.cover && book.path && book.filename
            ? `${API_URL}/books-posters/${coverPath(book.path)}/${coverPath(book.filename)}.jpg`
            : undefined;
    const authors = (book.authors ?? []).map((a) => a.full_name).join(', ');

    return (
        <Card sx={{ display: 'flex', height: 160 }}>
            {cover && (
                <CardMedia
                    component="img"
                    image={cover}
                    alt={book.title}
                    sx={{ width: 110, objectFit: 'cover' }}
                />
            )}
            <CardContent sx={{ flex: 1, overflow: 'hidden' }}>
                <Typography variant="subtitle1" noWrap>
                    {book.title}
                </Typography>
                {authors && (
                    <Typography variant="body2" color="text.secondary" noWrap>
                        {authors}
                    </Typography>
                )}
                {book.annotation && (
                    <Typography
                        variant="body2"
                        color="text.secondary"
                        sx={{
                            mt: 1,
                            display: '-webkit-box',
                            WebkitLineClamp: 3,
                            WebkitBoxOrient: 'vertical',
                            overflow: 'hidden',
                        }}
                    >
                        {book.annotation}
                    </Typography>
                )}
            </CardContent>
        </Card>
    );
};

const CollectionPage: React.FC = () => {
    const { t } = useTranslation();
    const { id: idParam } = useParams<{ id: string }>();
    const id = idParam ? parseInt(idParam, 10) : 0;

    const [data, setData] = useState<PublicCollectionDetail | null>(null);
    const [loadError, setLoadError] = useState<string | null>(null);

    useEffect(() => {
        if (!id) return;
        let cancelled = false;
        getPublicCollection(id)
            .then((d) => {
                if (!cancelled) setData(d);
            })
            .catch((err) => {
                if (!cancelled) setLoadError(err?.message ?? 'failed');
            });
        return () => {
            cancelled = true;
        };
    }, [id]);

    if (!id) return <Alert severity="error">invalid id</Alert>;
    if (loadError) return <Alert severity="error">{loadError}</Alert>;
    if (!data) return <Typography>{t('loading', 'Loading…')}</Typography>;

    return (
        <Box p={2}>
            <Box maxWidth={1200} mx="auto">
                <Box mb={2}>
                    <RouterLink to="/collections" style={{ textDecoration: 'none', color: 'inherit' }}>
                        <Typography variant="body2" color="text.secondary">
                            ← {t('publicCollections.backToList', 'All collections')}
                        </Typography>
                    </RouterLink>
                </Box>

                <Typography variant="h4" gutterBottom>
                    {data.name}
                </Typography>
                <Typography variant="body2" color="text.secondary" mb={3}>
                    {t('publicCollections.bookCount', { count: data.books.length, defaultValue: '{{count}} books' })}
                </Typography>

                <Stack spacing={2}>
                    {data.books.map((b) => (
                        <BookCard key={b.id} book={b} />
                    ))}
                    {data.books.length === 0 && (
                        <Typography color="text.secondary">
                            {t('publicCollections.emptyBooks', 'This collection has no books yet')}
                        </Typography>
                    )}
                </Stack>
            </Box>
        </Box>
    );
};

export default CollectionPage;
