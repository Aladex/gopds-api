// src/components/BookAnnotation.tsx
import React from 'react';
import { Box, Typography } from "@mui/material";
import { useTranslation } from "react-i18next";

interface BookAnnotationProps {
    annotation: string;
}

const BookAnnotation: React.FC<BookAnnotationProps> = ({ annotation }) => {
    const { t } = useTranslation();
    const [opened, setOpened] = React.useState(false);
    return (
        <>
            {annotation && (
                <Box mt={2}>
                    <Typography variant="subtitle1">{t('annotation')}:</Typography>
                    {opened ? (
                        <Typography variant="body2">{annotation}</Typography>
                    ) : (
                        <Typography variant="body2">
                            {annotation.slice(0, 200)}
                            {annotation.length > 200 && (
                                <>
                                    <br/><span onClick={() => setOpened(true)}><i>{t('readMore')}</i></span>
                                </>
                            )}
                        </Typography>
                    )}
                </Box>
            )}
        </>
    );
};

export default BookAnnotation;