import React, { useEffect, useState } from 'react';
import { Alert, Box, Card, CardActionArea, CardContent, Stack, Typography } from '@mui/material';
import { Link as RouterLink } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { listPublicCollections, PublicCollectionRow } from './api';

const CollectionsList: React.FC = () => {
    const { t } = useTranslation();
    const [rows, setRows] = useState<PublicCollectionRow[]>([]);
    const [loadError, setLoadError] = useState<string | null>(null);
    const [loaded, setLoaded] = useState(false);

    useEffect(() => {
        let cancelled = false;
        listPublicCollections()
            .then((data) => {
                if (!cancelled) {
                    setRows(data);
                    setLoaded(true);
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
    }, []);

    if (loadError) {
        return <Alert severity="error">{loadError}</Alert>;
    }

    return (
        <Box p={2}>
            <Typography variant="h5" gutterBottom>
                {t('publicCollections.title', 'Collections')}
            </Typography>

            {loaded && rows.length === 0 && (
                <Typography color="text.secondary">
                    {t('publicCollections.empty', 'No collections yet')}
                </Typography>
            )}

            <Stack spacing={2} mt={2}>
                {rows.map((c) => (
                    <Card key={c.id}>
                        <CardActionArea component={RouterLink} to={`/collections/${c.id}`}>
                            <CardContent>
                                <Typography variant="h6">{c.name}</Typography>
                            </CardContent>
                        </CardActionArea>
                    </Card>
                ))}
            </Stack>
        </Box>
    );
};

export default CollectionsList;
