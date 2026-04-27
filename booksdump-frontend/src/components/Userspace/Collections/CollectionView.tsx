import React, { useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { useParams } from 'react-router-dom';
import BooksList from '../BooksList';
import { getPublicCollection, PublicCollectionDetail } from './api';

// CollectionView wraps the regular BooksList with a heading carrying the
// collection's display name. The list itself reads `?curated_collection=:id`
// from the URL via BooksList.getParams (path: /collections/:id/page/:page).
const CollectionView: React.FC = () => {
    const { id: idParam } = useParams<{ id: string; page?: string }>();
    const id = idParam ? parseInt(idParam, 10) : 0;
    const [coll, setColl] = useState<PublicCollectionDetail | null>(null);
    const [err, setErr] = useState<string | null>(null);

    useEffect(() => {
        if (!id) return;
        let cancelled = false;
        getPublicCollection(id)
            .then((d) => {
                if (!cancelled) setColl(d);
            })
            .catch((e) => {
                if (!cancelled) setErr(e?.message ?? 'failed');
            });
        return () => {
            cancelled = true;
        };
    }, [id]);

    return (
        <>
            {(coll || err) && (
                <Box maxWidth={1200} mx="auto" px={2} pt={2}>
                    <Typography variant="h5">{err ? 'Error' : coll?.name}</Typography>
                </Box>
            )}
            <BooksList />
        </>
    );
};

export default CollectionView;
