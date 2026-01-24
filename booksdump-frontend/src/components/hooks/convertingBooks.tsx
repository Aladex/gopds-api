import React, { useEffect, useState } from 'react';
import { Modal, Backdrop, CircularProgress, Typography, Box, Snackbar } from '@mui/material';
import { useBookConversion } from '../../context/BookConversionContext';
import { useTranslation } from "react-i18next";

function ConversionModal() {
    const { state, dispatch } = useBookConversion();
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
                    color: (theme) => theme.palette.common.white,
                    zIndex: (theme) => theme.zIndex.modal + 1,
                }}
            >
                <CircularProgress sx={{ color: (theme) => theme.palette.common.white, zIndex: (theme) => theme.zIndex.modal + 1 }} />
                <Typography id="conversion-modal-title" variant="h6" sx={{ mt: 2, color: (theme) => theme.palette.common.white }}>
                    {t('conversionInProgress')}
                </Typography>
                <Typography id="conversion-modal-description" variant="body2" sx={{ color: (theme) => theme.palette.common.white }}>
                    {t('pleaseWait')}
                </Typography>
                {state.conversionErrors.map((err) => (
                    <Snackbar
                        key={`${err.bookID}-${err.format}`}
                        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
                        open={true}
                        autoHideDuration={4000}
                        message={err.message}
                        onClose={() => dispatch({ type: 'REMOVE_CONVERSION_ERROR', payload: { bookID: err.bookID, format: err.format } })}
                    />
                ))}
            </Box>
        </Modal>
    );
}

export default ConversionModal;
