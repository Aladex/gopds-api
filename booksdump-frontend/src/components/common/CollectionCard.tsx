import React from 'react';
import { Card, CardContent, Typography, Box, Button, Grid } from '@mui/material';
import { API_URL } from '../../api/config';
import { useTranslation } from 'react-i18next';

interface CollectionCardProps {
    collection: {
        id: number;
        name: string;
        created_at: string;
        updated_at: string;
    };
}

const CollectionCard: React.FC<CollectionCardProps> = ({ collection }) => {
    const { t } = useTranslation();

    return (
        <Grid item xs={12}>
            <Box maxWidth={1200} mx="auto" mt={1}>
                <Card sx={{ boxShadow: 2, p: 2 }}>
                    <CardContent>
                        <Box sx={{ textAlign: 'center' }}>
                            <Typography variant="h5">
                                {t('collection')}: {collection.name}
                            </Typography>
                        </Box>
                        <Typography sx={{ textAlign: 'center', mb: 1 }} variant="body2" color="textSecondary" textAlign="left">
                            {t('collectionUpdated')}: {new Date(collection.updated_at).toLocaleDateString()} {new Date(collection.updated_at).toLocaleTimeString()}
                        </Typography>
                        <Box sx={{ textAlign: 'center', mb: 1 }}>
                            <Typography variant="body2">
                                {t('downloadCollectionInFormat')}
                            </Typography>
                        </Box>
                        <Box sx={{ display: 'flex', justifyContent: 'space-between', gap: 2 }}>
                            <Button
                                component="a"
                                href={`${API_URL}/books/collection/${collection.id}/download/fb2`}
                                variant="contained"
                                color="secondary"
                                sx={{ color: 'white', flexGrow: 1 }}
                            >
                                FB2
                            </Button>
                            <Button
                                component="a"
                                href={`${API_URL}/books/collection/${collection.id}/download/epub`}
                                variant="contained"
                                color="secondary"
                                sx={{ color: 'white', flexGrow: 1 }}
                            >
                                EPUB
                            </Button>
                            <Button
                                component="a"
                                href={`${API_URL}/books/collection/${collection.id}/download/mobi`}
                                variant="contained"
                                color="secondary"
                                sx={{ color: 'white', flexGrow: 1 }}
                            >
                                MOBI
                            </Button>
                        </Box>
                    </CardContent>
                </Card>
            </Box>
        </Grid>
    );
};

export default CollectionCard;
