import React, { memo } from 'react';
import { Outlet } from 'react-router';

import ListLine from '@/features/catalogue/ListLine';

/** The frame every list route shares: the line that says what the list is, then the list. */
const ListLayout: React.FC = memo(() => (
    <>
        <ListLine />
        <div className="px-4 pb-4">
            <Outlet />
        </div>
    </>
));

ListLayout.displayName = 'ListLayout';

export default ListLayout;
