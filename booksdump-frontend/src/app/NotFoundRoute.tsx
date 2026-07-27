// src/components/NotFoundRoute.tsx
import React from 'react';
import ErrorPage from '@/shared/components/ErrorPage';
import TitleSetter from '@/app/TitleSetter';

const NotFoundRoute: React.FC = () => {
    return (
        <TitleSetter titleKey="routeTitles.error">
            <ErrorPage />
        </TitleSetter>
    );
};

export default NotFoundRoute;
