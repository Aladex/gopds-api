import React, { useEffect, useState } from 'react';
import { Modal, Backdrop, CircularProgress, Typography, Box } from '@mui/material';
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
            closeAfterTransition
            slots={{ backdrop: Backdrop }}
            slotProps={{
                backdrop: {
                    sx: {
                        backgroundColor: 'rgba(0, 0, 0, 0.5)',
                        zIndex: (theme) => theme.zIndex.drawer + 1,
                    },
                },
            }}
            disableAutoFocus
        >
            <Box
                sx={{
                    position: 'absolute',
                    top: '50%',
                    left: '50%',
                    transform: 'translate(-50%, -50%)',
                    textAlign: 'center',
                    color: 'white',
                    zIndex: (theme) => theme.zIndex.modal + 1,
                }}
            >
                <CircularProgress sx={{ color: 'white', zIndex: (theme) => theme.zIndex.modal + 1 }} />
                <Typography id="conversion-modal-title" variant="h6" sx={{ mt: 2, color: 'white' }}>
                    {t('conversionInProgress')}
                </Typography>
                <Typography id="conversion-modal-description" variant="body2" sx={{ color: 'white' }}>
                    {t('pleaseWait')}
                </Typography>
            </Box>
        </Modal>
    );
}

export default ConversionModal;
