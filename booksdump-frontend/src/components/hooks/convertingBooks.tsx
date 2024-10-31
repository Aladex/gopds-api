import React, { useEffect, useState } from 'react';
import Snackbar from '@mui/material/Snackbar';
import { useBookConversion } from '../../context/BookConversionContext';
import {useTranslation} from "react-i18next";

function ConversionSnackbar() {
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
        <Snackbar
            open={open}
            message={t('convertingBooks')}
            anchorOrigin={{ vertical: 'bottom', horizontal: 'center' }}
            onClose={() => setOpen(false)}
        />
    );
}

export default ConversionSnackbar;
