import React, { ReactNode, useEffect } from 'react';
import { useTranslation } from 'react-i18next';

interface WithTitleProps {
    titleKey: string;
    children: ReactNode;
}

const WithTitle: React.FC<WithTitleProps> = ({ titleKey, children }) => {
    const { t } = useTranslation();

    useEffect(() => {
        document.title = t(titleKey);
    }, [titleKey, t]);

    return <>{children}</>;
};

export default WithTitle;