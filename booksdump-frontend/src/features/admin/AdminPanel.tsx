import React, { useEffect } from 'react';
import { Link as RouterLink, Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';

import { Card, CardContent } from '@/shared/ui/card';
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
        <div className="p-4">
            <div className="mx-auto max-w-[1400px]">
                <Card>
                    <CardContent className="flex flex-col gap-4">
                        {/*
                          A row of links, not a tab widget: the panel below is
                          rendered by the router, and each section has a URL of
                          its own. Giving these Radix Tabs' roles would promise
                          assistive technology a tabpanel this component does
                          not own, and would break the middle-click and
                          open-in-new-tab an <a> gives for free.

                          The row scrolls rather than wraps — six labels do not
                          fit a phone, and a second line would push the content
                          below the fold.
                        */}
                        <nav
                            aria-label={t('adminSections', 'Admin sections')}
                            className="scrollbar-thin flex w-full min-w-0 items-center justify-start gap-1 overflow-x-auto rounded-md bg-muted p-1"
                        >
                            {SECTIONS.map((section) => {
                                const active = location.pathname.startsWith(section.path);
                                return (
                                    <RouterLink
                                        key={section.path}
                                        to={section.path}
                                        aria-current={active ? 'page' : undefined}
                                        className={cn(
                                            // no-underline and an explicit
                                            // colour because Tailwind runs
                                            // without preflight, so a bare <a>
                                            // would arrive browser-blue and
                                            // underlined.
                                            'inline-flex h-9 flex-none items-center justify-center rounded-sm px-3 text-sm font-medium whitespace-nowrap no-underline transition-colors sm:h-7 sm:text-[13px]',
                                            'outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50',
                                            active
                                                ? 'bg-background text-foreground shadow-xs'
                                                : 'text-muted-foreground hover:text-foreground',
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
                    </CardContent>
                </Card>
            </div>
        </div>
    );
};

export default AdminSpace;
