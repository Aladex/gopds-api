import React from 'react';
import { Navigate, useSearchParams } from 'react-router';

import TitleSetter from '@/app/TitleSetter';
import AuthorSearch from '@/features/catalogue/AuthorSearch';
import BooksList from '@/features/catalogue/BooksList';
import { CATALOGUE_PATH } from '@/features/catalogue/hooks/useSearchScope';

/**
 * SearchPage is the one address of a search: the type says which list answers.
 * With nothing to look for it is not a search, and the catalogue is the page
 * that shows everything.
 */
const SearchPage: React.FC = () => {
    const [params] = useSearchParams();

    if (!params.get('q')?.trim()) {
        return <Navigate to={CATALOGUE_PATH} replace />;
    }

    return params.get('type') === 'authors' ? (
        <TitleSetter titleKey="routeByAuthorName.books">
            <AuthorSearch />
        </TitleSetter>
    ) : (
        <TitleSetter titleKey="routeByBookName.books">
            <BooksList />
        </TitleSetter>
    );
};

export default SearchPage;
