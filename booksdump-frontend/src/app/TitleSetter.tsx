// src/components/TitleSetter.tsx
import React, { useEffect } from 'react';
import { useTranslation } from 'react-i18next';

interface TitleSetterProps {
    titleKey: string;
    children: React.ReactNode;
}

const TitleSetter: React.FC<TitleSetterProps> = ({ titleKey, children }) => {
    const { t } = useTranslation();

    useEffect(() => {
        document.title = t(titleKey);
    }, [titleKey, t]);

    return <>{children}</>;
};

export default TitleSetter;
