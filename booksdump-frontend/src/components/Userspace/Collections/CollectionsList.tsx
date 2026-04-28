import React, { useEffect, useState } from 'react';
import { Alert, Box, Card, CardActionArea, Stack, Typography } from '@mui/material';
import { Link as RouterLink, useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { API_URL } from '../../../api/config';
import BookPagination from '../../common/BookPagination';
import {
    CollectionCoverBook,
    listPublicCollections,
    PublicCollectionRow,
} from './api';

// coverPath mirrors Userspace/BooksList: covers live under the
// books-posters/<sanitized-path>/<sanitized-filename>.jpg URL pattern.
const coverPath = (value: string) => value.replaceAll('.', '-').replace(/^\/+/, '');

const coverURL = (book: CollectionCoverBook) =>
    book.cover && book.path && book.filename
        ? `${API_URL}/books-posters/${coverPath(book.path)}/${coverPath(book.filename)}.jpg`
        : null;

// hashHue maps a collection name to a stable HSL hue so the empty-state
// fallback gradient is at least consistent between reloads.
const hashHue = (s: string) => {
    let h = 0;
    for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0;
    return h % 360;
};

const CoverMosaic: React.FC<{ name: string; books?: CollectionCoverBook[] }> = ({ name, books }) => {
    const real = (books ?? []).map((b) => ({ url: coverURL(b), title: b.title }));
    const withCover = real.filter((b) => b.url);
    // Pad to 4 tiles. Empty tiles get a generated initial-tile so the mosaic
    // never has half-rendered holes.
    const tiles = withCover.slice(0, 4);
    while (tiles.length < 4) tiles.push({ url: null, title: name });

    if (withCover.length === 0) {
        // No covers anywhere — full-card gradient with the collection initial.
        const hue = hashHue(name);
        const initial = name.trim().charAt(0).toUpperCase() || '·';
        return (
            <Box
                sx={{
                    height: 220,
                    background: `linear-gradient(135deg, hsl(${hue} 40% 50%), hsl(${(hue + 50) % 360} 40% 30%))`,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    color: '#fff',
                    fontSize: 84,
                    fontWeight: 300,
                    letterSpacing: -2,
                }}
            >
                {initial}
            </Box>
        );
    }

    return (
        <Box
            sx={{
                height: 220,
                display: 'grid',
                gridTemplateColumns: '1fr 1fr',
                gridTemplateRows: '1fr 1fr',
                gap: '2px',
                background: '#0001',
            }}
        >
            {tiles.map((t, i) => {
                if (t.url) {
                    return (
                        <Box
                            key={i}
                            sx={{
                                backgroundImage: `url("${t.url}")`,
                                backgroundSize: 'cover',
                                backgroundPosition: 'center',
                            }}
                        />
                    );
                }
                const hue = hashHue(name + i);
                return (
                    <Box
                        key={i}
                        sx={{
                            background: `linear-gradient(135deg, hsl(${hue} 30% 55%), hsl(${(hue + 60) % 360} 30% 35%))`,
                        }}
                    />
                );
            })}
        </Box>
    );
};

const PAGE_SIZE = 12;

const CollectionsList: React.FC = () => {
    const { t } = useTranslation();
    const { page: pageParam } = useParams<{ page?: string }>();
    const page = Math.max(1, parseInt(pageParam ?? '1', 10) || 1);

    const [rows, setRows] = useState<PublicCollectionRow[]>([]);
    const [total, setTotal] = useState(0);
    const [loadError, setLoadError] = useState<string | null>(null);
    const [loaded, setLoaded] = useState(false);

    useEffect(() => {
        let cancelled = false;
        listPublicCollections(page, PAGE_SIZE)
            .then((data) => {
                if (!cancelled) {
                    setRows(data.rows);
                    setTotal(data.total);
                    setLoaded(true);
                    if (typeof window !== 'undefined') window.scrollTo(0, 0);
                }
            })
            .catch((err) => {
                if (!cancelled) {
                    setLoadError(err?.message ?? 'failed');
                    setLoaded(true);
                }
            });
        return () => {
            cancelled = true;
        };
    }, [page]);

    const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

    if (loadError) {
        return <Alert severity="error">{loadError}</Alert>;
    }

    return (
        <Box p={2}>
            <Box maxWidth={1200} mx="auto">
                <Typography variant="h5" gutterBottom>
                    {t('publicCollections.title', 'Collections')}
                </Typography>

                {loaded && rows.length === 0 && (
                    <Typography color="text.secondary">
                        {t('publicCollections.empty', 'No collections yet')}
                    </Typography>
                )}

                <Box
                    sx={{
                        mt: 2,
                        display: 'grid',
                        gridTemplateColumns: { xs: '1fr', sm: 'repeat(2, 1fr)', md: 'repeat(3, 1fr)' },
                        gap: 2.5,
                    }}
                >
                    {rows.map((c) => (
                        <Card key={c.id} sx={{ borderRadius: 3, overflow: 'hidden' }}>
                            <CardActionArea component={RouterLink} to={`/collections/${c.id}/page/1`}>
                                <CoverMosaic name={c.name} books={c.cover_books} />
                                <Box p={2}>
                                    <Typography variant="h6" sx={{ lineHeight: 1.2 }}>
                                        {c.name}
                                    </Typography>
                                </Box>
                            </CardActionArea>
                        </Card>
                    ))}
                </Box>

                {totalPages > 1 && (
                    <Stack alignItems="center" mt={3}>
                        <BookPagination
                            totalPages={totalPages}
                            currentPage={page}
                            baseUrl={`/collections/page/${page}`}
                        />
                    </Stack>
                )}
            </Box>
        </Box>
    );
};

export default CollectionsList;
