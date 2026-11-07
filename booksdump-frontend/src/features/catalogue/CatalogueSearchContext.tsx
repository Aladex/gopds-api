import React, { createContext, useContext } from 'react';

import {
    useCatalogueSearch,
    type CatalogueSearch,
} from '@/features/catalogue/hooks/useCatalogueSearch';

const CatalogueSearchContext = createContext<CatalogueSearch | undefined>(undefined);

/**
 * CatalogueSearchProvider holds the one running search of a page.
 *
 * The scope's release is state, kept per call of the hook behind it, and the
 * field, the chip and the picker all have to see the same one. Calling the hook
 * from each of them would give each its own scope: a release in one that the
 * others never heard of.
 */
export const CatalogueSearchProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
    const search = useCatalogueSearch();
    return (
        <CatalogueSearchContext.Provider value={search}>{children}</CatalogueSearchContext.Provider>
    );
};

export const useCatalogueSearchContext = (): CatalogueSearch => {
    const context = useContext(CatalogueSearchContext);
    if (!context) {
        throw new Error('useCatalogueSearchContext must be used within a CatalogueSearchProvider');
    }
    return context;
};
