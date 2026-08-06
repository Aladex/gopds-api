import React from 'react';
import { Link, useLocation } from 'react-router';
import { useTranslation } from 'react-i18next';
import { BookOpen, Library, ShieldUser, User, type LucideIcon } from 'lucide-react';

import { cn } from '@/shared/lib/utils';

import { useAuth } from '@/context/AuthContext';
import { activeNavItem, onSurface, useNavItems, type NavItem } from '@/shared/layout/navItems';

type BottomNavigationProps = {
    isProfileOpen: boolean;
    onOpenProfile: () => void;
};

const ICONS: Record<NavItem['id'], LucideIcon> = {
    books: BookOpen,
    collections: Library,
    admin: ShieldUser,
};

/**
 * BottomNavigation carries the section navigation on a phone, where the header
 * has no room for it. The profile sits alongside the sections because that is
 * where a reader looks for it, though it opens a panel rather than a route.
 */
const BottomNavigation: React.FC<BottomNavigationProps> = ({ isProfileOpen, onOpenProfile }) => {
    const location = useLocation();
    const { t } = useTranslation();
    const { user } = useAuth();

    const allItems = useNavItems(Boolean(user?.is_superuser));
    const navItems = onSurface(allItems, 'bottom');

    /*
     * Matched against every section, drawn from this surface's own.
     *
     * The two differ, and the match has to be the wider of them: on a route
     * belonging to a section this bar does not carry, the answer is that none of
     * its own is current — not the first one. Matching only against what is
     * drawn would fall through to the fallback below and light up Books while
     * the reader is in the admin panel.
     *
     * The fallback itself stays for routes belonging to no section at all — a
     * search result, an author's page — where the reader did come from the
     * catalogue and the bar should say so.
     */
    const active = activeNavItem(allItems, location.pathname);
    // An open profile takes the highlight off whatever section is behind it.
    const current = isProfileOpen ? null : (active ?? navItems[0]);

    const itemClass = (active: boolean) =>
        cn(
            'flex min-w-0 flex-1 flex-col items-center gap-0.5 px-1 py-1.5 text-[11px]',
            active ? 'text-white' : 'text-neutral-400 hover:text-white',
        );

    return (
        <nav
            aria-label={t('user')}
            className="fixed inset-x-0 bottom-0 z-chrome flex bg-neutral-800 shadow-[0_-2px_8px_rgba(0,0,0,0.4)]"
        >
            {navItems.map((item) => {
                const Icon = ICONS[item.id];
                const active = current?.id === item.id;
                return (
                    <Link
                        key={item.id}
                        to={item.path}
                        aria-label={item.label}
                        aria-current={active ? 'page' : undefined}
                        className={itemClass(active)}
                    >
                        <Icon className="size-6" />
                        <span className="max-w-full truncate">{item.label}</span>
                    </Link>
                );
            })}
            <button
                type="button"
                onClick={onOpenProfile}
                aria-label={t('user')}
                aria-expanded={isProfileOpen}
                className={itemClass(isProfileOpen)}
            >
                <User className="size-6" />
                <span className="max-w-full truncate">{t('user')}</span>
            </button>
        </nav>
    );
};

export default BottomNavigation;
