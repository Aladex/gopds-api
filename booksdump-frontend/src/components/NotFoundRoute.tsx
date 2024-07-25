// src/components/NotFoundRoute.tsx
import React from 'react';
import ErrorPage from '../components/Service/ErrorPage';
import TitleSetter from '../components/common/TitleSetter';

const NotFoundRoute: React.FC = () => {
    return (
        <TitleSetter titleKey="routeTitles.error">
            <ErrorPage />
        </TitleSetter>
    );
};

export default NotFoundRoute;
