import React from 'react';
import { useTranslation } from 'react-i18next';
import { Grid, Box, Card, CardContent } from '@mui/material';
import '../styles/OpdsInfo.css';

const OpdsInfo: React.FC = () => {
    const { t } = useTranslation();
    const domain = window.location.hostname;

    return (
        <Grid container justifyContent="center" spacing={4}>
            <Grid item xs={12}>
                <Box maxWidth={1200} mx="auto">
                    <Card sx={{ boxShadow: 2, p: 1, my: 1 }}>
                        <CardContent>
                            <div className="opds-info">
                                <h2>{t('opdsInfo.title')}</h2>
                                <p>{t('opdsInfo.addCatalog', { domain })}</p>
                                <p>{t('opdsInfo.testMode')}</p>
                                <p>{t('opdsInfo.authorization')}</p>
                                <p>{t('opdsInfo.alReaderAuth')}</p>
                                <p>{t('opdsInfo.tlsSupport')}</p>
                                <p>{t('opdsInfo.pocketBook')}</p>
                                <p>{t('opdsInfo.onyxBoox')}</p>
                            </div>
                        </CardContent>
                    </Card>
                </Box>
            </Grid>
        </Grid>
    );
};

export default OpdsInfo;