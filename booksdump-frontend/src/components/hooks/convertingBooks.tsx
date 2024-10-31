import React, { useEffect, useState } from 'react';
import { Modal, Box, CircularProgress, Typography } from '@mui/material';
import { useBookConversion } from '../../context/BookConversionContext';
import { useTranslation } from "react-i18next";

function ConversionModal() {
    const { state } = useBookConversion();
    const [open, setOpen] = useState(false);
    const { t } = useTranslation();

    useEffect(() => {
        setOpen(state.convertingBooks.length > 0);
    }, [state.convertingBooks]);

    return (
        <Modal
            open={open}
            aria-labelledby="conversion-modal-title"
            aria-describedby="conversion-modal-description"
            closeAfterTransition
        >
            <Box
                sx={{
                    position: 'absolute',
                    top: '50%',
                    left: '50%',
                    transform: 'translate(-50%, -50%)',
                    bgcolor: 'background.paper',
                    borderRadius: 1,
                    boxShadow: 24,
                    p: 4,
                    display: 'flex',
                    flexDirection: 'column',
                    alignItems: 'center',
                }}
            >
                <CircularProgress color="inherit" />
                <Typography id="conversion-modal-title" variant="h6" sx={{ mt: 2 }}>
                    {t('conversionInProgress')}
                </Typography>
                <Typography id="conversion-modal-description" variant="body2">
                    {t('pleaseWait')}
                </Typography>
            </Box>
        </Modal>
    );
}

export default ConversionModal;
