// src/hooks/useSearchOptions.ts
import { useState, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { useLocation } from 'react-router-dom';

const useSearchOptions = (setSelectedSearch: (value: string) => void) => {
    const { t } = useTranslation();
    const location = useLocation();
    const [searchOptions, setSearchOptions] = useState<Array<{ value: string; label: string }>>([
        { value: 'title', label: t('byTitle') },
        { value: 'author', label: t('byAuthor') },
    ]);

    useEffect(() => {
        setSearchOptions([
            { value: 'title', label: t('byTitle') },
            { value: 'author', label: t('byAuthor') },
        ]);
    }, [t]);

    useEffect(() => {
        const pathStartsWith = '/books/find/author/';
        const optionValue = 'authorsBookSearch';
        const optionLabel = t('authorsBookSearch');
        const isAuthorsBooksSearchPage = location.pathname.startsWith(pathStartsWith);

        setSearchOptions(prevOptions => {
            const optionExists = prevOptions.some(option => option.value === optionValue);
            if (isAuthorsBooksSearchPage && !optionExists) {
                setSelectedSearch(optionValue);
                return [...prevOptions, { value: optionValue, label: optionLabel }];
            } else if (location.pathname.startsWith('/authors/') && !optionExists) {
                return prevOptions.filter(option => option.value !== optionValue);
            } else if (!isAuthorsBooksSearchPage && optionExists) {
                setSelectedSearch('title');
                return prevOptions.filter(option => option.value !== optionValue);
            }
            return prevOptions;
        });
    }, [location.pathname, t, setSelectedSearch]);

    return searchOptions;
};

export default useSearchOptions;