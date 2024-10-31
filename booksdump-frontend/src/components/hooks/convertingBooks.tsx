import React, { useEffect, useState } from 'react';
import { Backdrop, CircularProgress, Typography, Box } from '@mui/material';
import { useBookConversion } from '../../context/BookConversionContext';
import { useTranslation } from "react-i18next";

function ConversionBackdrop() {
    const { state } = useBookConversion();
    const [open, setOpen] = useState(false);
    const { t } = useTranslation();

    useEffect(() => {
        if (state.convertingBooks.length > 0) {
            setOpen(true);
        } else {
            setOpen(false);
        }
    }, [state.convertingBooks]);

    return (
        <Backdrop open={open} sx={{ color: '#fff', zIndex: (theme) => theme.zIndex.drawer + 1 }}>
            <Box textAlign="center">
                <CircularProgress color="inherit" />
                <Typography variant="h6" sx={{ mt: 2 }}>
                    {t('conversionInProgress')}
                </Typography>
                <Typography variant="body2">
                    {t('pleaseWait')}
                </Typography>
            </Box>
        </Backdrop>
    );
}

export default ConversionBackdrop;
