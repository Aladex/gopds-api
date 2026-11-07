import React from 'react';
import { Navigate, Route, useLocation, useParams } from 'react-router';

import { legacyAuthorAddress, legacySearchAddress } from '@/features/catalogue/addresses';

// The redirects stand outside the private layout, so a stale address costs no
// search field: it only ever sees the address it can read. They replace
// the old entry, which is what lets the back button pass them.

const LegacyAuthor: React.FC = () => {
    const { id = '', page } = useParams();
    const { search, hash } = useLocation();
    return <Navigate to={{ ...legacyAuthorAddress(id, page, search), hash }} replace />;
};

const LegacyTitleSearch: React.FC = () => {
    const { title = '', page } = useParams();
    const { search, hash } = useLocation();
    return <Navigate to={{ ...legacySearchAddress(title, 'books', page, search), hash }} replace />;
};

const LegacyAuthorSearch: React.FC = () => {
    const { author = '', page } = useParams();
    const { search, hash } = useLocation();
    return (
        <Navigate to={{ ...legacySearchAddress(author, 'authors', page, search), hash }} replace />
    );
};

const legacyRoutes = (
    <>
        <Route path="/books/find/author/:id/:page" element={<LegacyAuthor />} />
        <Route path="/books/find/title/:title/:page" element={<LegacyTitleSearch />} />
        <Route path="/authors/:author/:page" element={<LegacyAuthorSearch />} />
    </>
);

export default legacyRoutes;
