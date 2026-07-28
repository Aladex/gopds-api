import React from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';

import { Button } from '@/shared/ui/button';

/** ErrorPage is where a route that went nowhere lands the reader. */
const ErrorPage: React.FC = () => {
    const navigate = useNavigate();
    const { t } = useTranslation();

    return (
        <div className="mx-auto flex max-w-sm flex-col items-center gap-4 px-4 py-16 text-center">
            <h1 className="text-2xl font-semibold tracking-tight">{t('errorPage.title')}</h1>
            <p className="text-sm text-muted-foreground">{t('errorPage.message')}</p>
            <Button onClick={() => navigate('/')}>{t('errorPage.button')}</Button>
        </div>
    );
};

export default ErrorPage;
