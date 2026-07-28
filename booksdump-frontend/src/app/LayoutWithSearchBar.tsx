import React, { memo } from 'react';
import { Outlet } from 'react-router';

import SearchBar from '@/features/catalogue/SearchBar';

/** The frame every list route shares: the search panel above, the list below. */
const LayoutWithSearchBar: React.FC = memo(() => (
    <div className="p-4">
        <SearchBar />
        <Outlet />
    </div>
));

LayoutWithSearchBar.displayName = 'LayoutWithSearchBar';

export default LayoutWithSearchBar;
