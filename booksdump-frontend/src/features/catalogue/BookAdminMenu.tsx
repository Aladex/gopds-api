import React from 'react';
import { useTranslation } from 'react-i18next';
import { MoreHorizontal, Pencil, RefreshCw } from 'lucide-react';

import type { Book } from '@/api/books';
import { Button } from '@/shared/ui/button';
import {
    DropdownMenu,
    DropdownMenuCheckboxItem,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} from '@/shared/ui/dropdown-menu';

type BookAdminMenuProps = {
    book: Book;
    onRescan: (bookID: number) => void;
    onEdit: (book: Book) => void;
    onToggleApproved: (book: Book) => void;
};

/**
 * BookAdminMenu holds what only an administrator does to a book: rescan it,
 * edit it, approve it or take the approval back.
 *
 * They were three icon buttons of their own in the card's action row, and
 * with the two send buttons the row grew to seven — wider than a card on a
 * phone. One button opens them all now, just before the star.
 *
 * The approval used to show as the colour of its button, green when given.
 * It is still seen without opening anything: a book not yet approved puts a
 * dot on the menu button, and the button's name says so for anyone not
 * looking at it.
 */
const BookAdminMenu: React.FC<BookAdminMenuProps> = ({
    book,
    onRescan,
    onEdit,
    onToggleApproved,
}) => {
    const { t } = useTranslation();
    const label = book.approved
        ? t('bookAdminActions')
        : `${t('bookAdminActions')}: ${t('bookNotApproved')}`;

    return (
        <DropdownMenu modal={false}>
            <DropdownMenuTrigger asChild>
                <Button
                    variant="ghost"
                    size="icon"
                    title={label}
                    aria-label={label}
                    data-approved={book.approved}
                    className="relative"
                >
                    <MoreHorizontal className="size-4" />
                    {!book.approved && (
                        <span
                            aria-hidden="true"
                            data-testid="not-approved-dot"
                            className="absolute top-1.5 right-1.5 size-2 rounded-full bg-amber-500"
                        />
                    )}
                </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
                <DropdownMenuItem onSelect={() => onRescan(book.id)}>
                    <RefreshCw />
                    {t('bookMenuRescan')}
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => onEdit(book)}>
                    <Pencil />
                    {t('bookMenuEdit')}
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuCheckboxItem
                    checked={book.approved}
                    onCheckedChange={() => onToggleApproved(book)}
                >
                    {book.approved ? t('bookMenuUnapprove') : t('bookMenuApprove')}
                </DropdownMenuCheckboxItem>
            </DropdownMenuContent>
        </DropdownMenu>
    );
};

export default BookAdminMenu;
