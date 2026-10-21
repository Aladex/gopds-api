import React from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';

import { Button } from '@/shared/ui/button';
import CenteredBox from '@/features/auth/CenteredBox';

/**
 * ErrorPage is where a route that went nowhere lands the reader.
 *
 * It is a page of its own, with no header above it and no footer below — the
 * route that renders it sits outside the application's frame — so it takes the
 * frame the sign-in screens have: the reading room, one card. The words are
 * the blog's old joke that the Leprosorium does not exist, and the room says
 * the same thing without words: the open book on the table has its right-hand
 * page torn out, a ragged stub left at the gutter. The page is lost, like the
 * route.
 *
 * The card is set like the sign-in forms — a title, a line under it, one
 * full-width button — so that moving between them reads as one place.
 */
const ErrorPage: React.FC = () => {
    const navigate = useNavigate();
    const { t } = useTranslation();

    return (
        <CenteredBox torn>
            <div className="flex flex-col gap-4">
                <div>
                    <h1 className="text-xl font-semibold tracking-tight">{t('errorPage.title')}</h1>
                    <p className="mt-1 text-sm text-muted-foreground">{t('errorPage.message')}</p>
                </div>
                <Button className="w-full" onClick={() => navigate('/')}>
                    {t('errorPage.button')}
                </Button>
            </div>
        </CenteredBox>
    );
};

export default ErrorPage;
