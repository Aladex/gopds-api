import React from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';

import { Button } from '@/shared/ui/button';
import { Card } from '@/shared/ui/card';
import ClassicBackdrop from '@/features/auth/ClassicBackdrop';

/**
 * ErrorPage is where a route that went nowhere lands the reader.
 *
 * It is a page of its own, with no header above it and no footer below — the
 * route that renders it sits outside the application's frame. So it is built
 * like the sign-in screens rather than like a section of the catalogue: one
 * card in the middle of the viewport, the verse drifting down the left of it.
 *
 * It used to be a bare column of text pinned to the top of a box seven tenths
 * of the screen tall, which left the words floating somewhere near the middle
 * with nothing under them and the verse stopping short of the foot of the page.
 */
const ErrorPage: React.FC = () => {
    const navigate = useNavigate();
    const { t } = useTranslation();

    return (
        <div className="relative flex min-h-screen items-center justify-center overflow-hidden p-4">
            <ClassicBackdrop moment="lost" />

            <Card className="relative w-full max-w-[400px] items-center gap-4 p-6 text-center">
                <h1 className="text-2xl font-semibold tracking-tight">{t('errorPage.title')}</h1>
                <p className="text-sm text-muted-foreground">{t('errorPage.message')}</p>
                <Button onClick={() => navigate('/')}>{t('errorPage.button')}</Button>
            </Card>
        </div>
    );
};

export default ErrorPage;
