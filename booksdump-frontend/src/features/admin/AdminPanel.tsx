import React, { useEffect } from 'react';
import {
    Link as RouterLink,
    Navigate,
    Route,
    Routes,
    useLocation,
    useNavigate,
} from 'react-router';
import { useTranslation } from 'react-i18next';

import { cn } from '@/shared/lib/utils';

import UsersTable from '@/features/admin/UsersTable';
import InvitesTable from '@/features/admin/InvitesTable';
import Duplicates from '@/features/admin/Duplicates';
import BookScanning from '@/features/admin/BookScanning';
import GenreManagement from '@/features/admin/GenreManagement';
import CuratedCollectionsList from '@/features/admin/CuratedCollections/CuratedCollectionsList';
import CuratedCollectionDetail from '@/features/admin/CuratedCollections/CuratedCollectionDetail';

/**
 * The admin sections, in the order they are shown.
 *
 * `label` is resolved at render time rather than here: a module-level t() would
 * be evaluated once, before the language is known, and never again when it
 * changes.
 */
const SECTIONS: { path: string; labelKey: string; fallback?: string }[] = [
    { path: '/admin/users', labelKey: 'users' },
    { path: '/admin/invites', labelKey: 'invites' },
    { path: '/admin/book-scanning', labelKey: 'bookScanning' },
    { path: '/admin/duplicates', labelKey: 'duplicates' },
    { path: '/admin/genres', labelKey: 'genreManagement' },
    { path: '/admin/collections', labelKey: 'curatedCollections.tab', fallback: 'Collections' },
];

const AdminSpace: React.FC = () => {
    const location = useLocation();
    const navigate = useNavigate();
    const { t } = useTranslation();

    useEffect(() => {
        if (location.pathname === '/admin') {
            navigate('/admin/users', { replace: true });
        }
    }, [location.pathname, navigate]);

    return (
        /*
          The admin is a page, not a card. It used to be one, with every screen
          putting its own cards inside it — a frame inside a frame that cost
          50px of width on every page, and a sixth of a phone's, for nothing.
          The screens below bring their own framing where the content needs it.
        */
        <div className="mx-auto flex w-full max-w-[1400px] flex-col gap-4 p-4">
            {/*
              Tabs to look at and links to use: the panel below is rendered by
              the router and each section has a URL of its own, so these stay
              anchors — middle-click and open-in-new-tab keep working, and
              nothing claims to own a tabpanel it does not.

              They wrap rather than scroll. There are six of them and they never
              change; a row that scrolls hides half of them behind a gesture
              nobody makes, while a second line costs one row of height once.
            */}
            <nav
                aria-label={t('adminSections', 'Admin sections')}
                className="flex flex-wrap items-center gap-x-1 gap-y-1"
            >
                {SECTIONS.map((section) => {
                    const active = location.pathname.startsWith(section.path);
                    return (
                        <RouterLink
                            key={section.path}
                            to={section.path}
                            aria-current={active ? 'page' : undefined}
                            className={cn(
                                // no-underline and an explicit colour because
                                // Tailwind runs without preflight, so a bare
                                // <a> would arrive browser-blue and underlined.
                                'inline-flex h-9 items-center border-b-2 px-2 text-sm font-medium whitespace-nowrap no-underline transition-colors sm:px-3 sm:text-[13px]',
                                'outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50',
                                active
                                    ? 'border-foreground text-foreground'
                                    : 'border-transparent text-muted-foreground hover:text-foreground',
                            )}
                        >
                            {t(section.labelKey, section.fallback ?? section.labelKey)}
                        </RouterLink>
                    );
                })}
            </nav>

            <Routes>
                <Route path="users" element={<UsersTable />} />
                <Route path="users/:page" element={<UsersTable />} />
                <Route path="invites" element={<InvitesTable />} />
                <Route path="book-scanning" element={<BookScanning />} />
                <Route path="duplicates" element={<Duplicates />} />
                <Route path="genres" element={<GenreManagement />} />
                <Route path="collections" element={<CuratedCollectionsList />} />
                <Route path="collections/page/:page" element={<CuratedCollectionsList />} />
                <Route path="collections/:id" element={<CuratedCollectionDetail />} />
                <Route path="*" element={<Navigate to="/admin/users" />} />
            </Routes>
        </div>
    );
};

export default AdminSpace;
