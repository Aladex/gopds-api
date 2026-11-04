import React from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { ChevronRight } from 'lucide-react';

import type { AuthorDisplay, AuthorEntityRef } from '@/api/books';
import { Button } from '@/shared/ui/button';
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuGroup,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuTrigger,
} from '@/shared/ui/dropdown-menu';
import ListSheet, { LIST_SHEET_ROW_CLASS } from '@/shared/components/ListSheet';
import { authorTitle } from '@/shared/lib/authorTitle';
import { cn } from '@/shared/lib/utils';

/**
 * AuthorByline is the line under a book's title that says who wrote it:
 * «Наталья Волочаевская, Дмитрий Костюкевич и ещё 12».
 *
 * Prose, with no «Авторы:» label: it is how a book names its author under its
 * title. A wide card shows two names before the counter and a phone one; a
 * book with two authors or fewer shows them all. The counter is the one
 * control for the rest, so a book of forty-six authors does not turn the card
 * into a wall of names: it opens a dropdown menu on a wide card and a bottom
 * sheet on a phone, each listing every author as a link.
 *
 * A name of several people (several author entities) opens a short menu of
 * them where it stands in the line, and in the full list it is a group: the
 * name as the book writes it, with each person nested under it.
 */

type AuthorBylineProps = {
    authors: AuthorDisplay[];
    /** Two names before the counter, and a menu rather than a sheet. */
    isWide: boolean;
    /** Named under the sheet's title, so the list says whose authors these are. */
    bookTitle: string;
    /** Called as a name is followed, with the name the author's page goes by. */
    onGo: (scopeName: string) => void;
};

/** Where one name links, and the name its page goes by; none when unlinked. */
const single = (
    author: AuthorDisplay,
    t: Parameters<typeof authorTitle>[1],
): { to: string; scopeName: string } | null => {
    // Under the entity identity a name links to its entity, and the scope is
    // named as the entity's page names it, so a reload says the same.
    const entity = author.entities?.length === 1 ? author.entities[0] : undefined;
    const id = entity ? entity.id : author.legacy_author_id;
    if (id === undefined) return null;
    return {
        to: `/books/find/author/${id}/1`,
        scopeName: entity ? authorTitle(entity, t) : author.name,
    };
};

const people = (author: AuthorDisplay): AuthorEntityRef[] =>
    (author.entities?.length ?? 0) > 1 ? author.entities! : [];

const personLink = (person: AuthorEntityRef) => `/books/find/author/${person.id}/1`;

/**
 * AuthorPeople is a name of several people where it stands in the byline: the
 * name as the book writes it, opening a short menu of each of them. Its
 * trigger is an inline span rather than a button box, so the name wraps with
 * the rest of the line.
 */
const AuthorPeople: React.FC<{
    name: string;
    people: AuthorEntityRef[];
    onGo: (scopeName: string) => void;
}> = ({ name, people: list, onGo }) => {
    const { t } = useTranslation();
    return (
        <DropdownMenu modal={false}>
            <DropdownMenuTrigger asChild>
                <span
                    role="button"
                    tabIndex={0}
                    className="cursor-pointer border-b border-dashed border-border hover:border-current"
                >
                    {name}
                </span>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start">
                {list.map((person) => (
                    <DropdownMenuItem key={person.id} asChild>
                        <Link to={personLink(person)} onClick={() => onGo(authorTitle(person, t))}>
                            {authorTitle(person, t)}
                        </Link>
                    </DropdownMenuItem>
                ))}
            </DropdownMenuContent>
        </DropdownMenu>
    );
};

/** One name in the byline: a link, a menu of people, or plain text. */
const BylineName: React.FC<{ author: AuthorDisplay; onGo: (scopeName: string) => void }> = ({
    author,
    onGo,
}) => {
    const { t } = useTranslation();
    const several = people(author);
    if (several.length > 0) {
        return <AuthorPeople name={author.name} people={several} onGo={onGo} />;
    }
    const target = single(author, t);
    if (!target) return <span>{author.name}</span>;
    return (
        <Link
            to={target.to}
            onClick={() => onGo(target.scopeName)}
            className="border-b border-border hover:border-current"
        >
            {author.name}
        </Link>
    );
};

/** The heading of the full list: «Авторы · 46». */
const listTitle = (t: (key: string) => string, count: number) => `${t('authors')} · ${count}`;

/** The wide card's list: the app's dropdown menu, scrolling when long. */
const AuthorsMenu: React.FC<{
    authors: AuthorDisplay[];
    trigger: React.ReactNode;
    onGo: (scopeName: string) => void;
}> = ({ authors, trigger, onGo }) => {
    const { t } = useTranslation();
    const groupID = React.useId();
    return (
        <DropdownMenu modal={false}>
            <DropdownMenuTrigger asChild>{trigger}</DropdownMenuTrigger>
            <DropdownMenuContent
                align="start"
                data-testid="book-authors-list"
                className="max-h-80 w-80"
            >
                <DropdownMenuLabel className="text-xs font-semibold text-muted-foreground">
                    {listTitle(t, authors.length)}
                </DropdownMenuLabel>
                {authors.map((author, index) => {
                    const several = people(author);
                    if (several.length > 0) {
                        const labelID = `${groupID}-${index}`;
                        return (
                            <DropdownMenuGroup key={index} aria-labelledby={labelID}>
                                <DropdownMenuLabel id={labelID} className="font-normal">
                                    {author.name}
                                </DropdownMenuLabel>
                                {several.map((person) => (
                                    <DropdownMenuItem key={person.id} asChild className="pl-5">
                                        <Link
                                            to={personLink(person)}
                                            onClick={() => onGo(authorTitle(person, t))}
                                        >
                                            {authorTitle(person, t)}
                                        </Link>
                                    </DropdownMenuItem>
                                ))}
                            </DropdownMenuGroup>
                        );
                    }
                    const target = single(author, t);
                    if (!target) {
                        return (
                            <DropdownMenuItem key={index} disabled>
                                {author.name}
                            </DropdownMenuItem>
                        );
                    }
                    return (
                        <DropdownMenuItem key={index} asChild>
                            <Link to={target.to} onClick={() => onGo(target.scopeName)}>
                                {author.name}
                            </Link>
                        </DropdownMenuItem>
                    );
                })}
            </DropdownMenuContent>
        </DropdownMenu>
    );
};

const SHEET_ROW = cn(LIST_SHEET_ROW_CLASS, 'justify-between');

/** The phone's list: the app's bottom sheet, one finger-height row per name. */
const AuthorsSheet: React.FC<{
    open: boolean;
    onOpenChange: (open: boolean) => void;
    authors: AuthorDisplay[];
    bookTitle: string;
    onGo: (scopeName: string) => void;
}> = ({ open, onOpenChange, authors, bookTitle, onGo }) => {
    const { t } = useTranslation();
    const groupID = React.useId();
    // Following a name closes the sheet: the list it opens is the new page.
    const go = (scopeName: string) => {
        onGo(scopeName);
        onOpenChange(false);
    };
    const chevron = <ChevronRight aria-hidden="true" className="size-4 text-muted-foreground" />;
    return (
        <ListSheet
            open={open}
            onOpenChange={onOpenChange}
            title={listTitle(t, authors.length)}
            description={bookTitle}
        >
            <ul data-testid="book-authors-list" className="divide-y divide-border">
                {authors.map((author, index) => {
                    const several = people(author);
                    if (several.length > 0) {
                        const labelID = `${groupID}-${index}`;
                        return (
                            <li key={index}>
                                <div role="group" aria-labelledby={labelID}>
                                    <p
                                        id={labelID}
                                        className="flex min-h-12 items-center px-4 text-base text-muted-foreground"
                                    >
                                        {author.name}
                                    </p>
                                    <ul>
                                        {several.map((person) => (
                                            <li key={person.id}>
                                                <Link
                                                    to={personLink(person)}
                                                    onClick={() => go(authorTitle(person, t))}
                                                    className={cn(SHEET_ROW, 'pl-8')}
                                                >
                                                    {authorTitle(person, t)}
                                                    {chevron}
                                                </Link>
                                            </li>
                                        ))}
                                    </ul>
                                </div>
                            </li>
                        );
                    }
                    const target = single(author, t);
                    return (
                        <li key={index}>
                            {target ? (
                                <Link
                                    to={target.to}
                                    onClick={() => go(target.scopeName)}
                                    className={SHEET_ROW}
                                >
                                    {author.name}
                                    {chevron}
                                </Link>
                            ) : (
                                <span className="flex min-h-12 items-center px-4 text-base">
                                    {author.name}
                                </span>
                            )}
                        </li>
                    );
                })}
            </ul>
        </ListSheet>
    );
};

const AuthorByline: React.FC<AuthorBylineProps> = ({ authors, isWide, bookTitle, onGo }) => {
    const { t } = useTranslation();
    const [sheetOpen, setSheetOpen] = React.useState(false);

    if (authors.length === 0) return null;

    const shownCount = authors.length <= 2 ? authors.length : isWide ? 2 : 1;
    const rest = authors.length - shownCount;

    const counter = (
        <Button
            variant="outline"
            size="xs"
            aria-haspopup={isWide ? 'menu' : 'dialog'}
            onClick={isWide ? undefined : () => setSheetOpen(true)}
            className={cn(
                'relative ml-1 h-[22px] rounded-full px-2 align-[1px] text-xs font-normal text-primary',
                // A finger's height around a 22px pill.
                'before:absolute before:-inset-x-1 before:-inset-y-2.5',
            )}
        >
            {t('bookAuthorsMore', { count: rest })}
        </Button>
    );

    return (
        <div data-testid="book-byline" className="text-sm leading-relaxed">
            {authors.slice(0, shownCount).map((author, index) => (
                // Two credits may share a name, and an unlinked one has no ID:
                // the place in the line is the only stable key.
                <React.Fragment key={index}>
                    {index > 0 && ', '}
                    <BylineName author={author} onGo={onGo} />
                </React.Fragment>
            ))}
            {rest > 0 &&
                (isWide ? (
                    <AuthorsMenu authors={authors} trigger={counter} onGo={onGo} />
                ) : (
                    <>
                        {counter}
                        <AuthorsSheet
                            open={sheetOpen}
                            onOpenChange={setSheetOpen}
                            authors={authors}
                            bookTitle={bookTitle}
                            onGo={onGo}
                        />
                    </>
                ))}
        </div>
    );
};

export default AuthorByline;
