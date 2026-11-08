import React from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { Check, ChevronDown, ChevronRight, Layers } from 'lucide-react';

import type { Book, BookVariantRow } from '@/api/books';
import { Button } from '@/shared/ui/button';
import { BouncingDots } from '@/shared/ui/bouncing-dots';
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuTrigger,
} from '@/shared/ui/dropdown-menu';
import ListSheet, { LIST_SHEET_ROW_CLASS } from '@/shared/components/ListSheet';
import { thumbURL } from '@/shared/lib/coverUrl';
import { cn } from '@/shared/lib/utils';
import {
    differingFields,
    labelLines,
    variantLabel,
    type LabelField,
} from '@/features/catalogue/variants/variantLabel';

/**
 * VariantSelector is the one control a card with variants gets (direction B).
 *
 * Wide, a block at the top of the rail above «Читать онлайн»: «Вариант» and
 * where the shown file stands («1 из 6», «последняя из 22»), and a trigger
 * naming the shown file that opens the app's dropdown menu. On a phone, a chip
 * at the end of the dates line («6 вариантов», after a pick «вариант 2 из 6 ·
 * пер. Н. Прохорова») that opens the app's bottom sheet, as «Скачать» and
 * «Отправить» do. Both list the same rows: what differs among the files, one
 * row per document with the default marked and the shown one checked, and a
 * document's earlier revisions folded under it.
 */

export interface VariantSelectorProps {
    /** The card: its title, its key, its kind and count. */
    book: Book;
    /** The file the card shows. */
    shown: Book;
    /** Every visible file of the card, by rank; null until read. */
    rows: BookVariantRow[] | null;
    loading: boolean;
    onOpen: () => void;
    onSelect: (bookID: number) => void;
    /** The phone's chip, or the rail's block. */
    layout: 'rail' | 'chip';
}

/** What a label says of a file before the card's files have been read. */
const fieldsBeforeRows = (kind: string): LabelField[] =>
    kind === 'revisions' ? ['revision'] : ['translator', 'publisherYear'];

/** A document's head and its earlier revisions, in rank order. */
interface Lineage {
    head: BookVariantRow;
    older: BookVariantRow[];
}

const lineagesOf = (rows: BookVariantRow[]): Lineage[] => {
    const byLineage = new Map<string, Lineage>();
    for (const row of rows) {
        const found = byLineage.get(row.lineage);
        if (!found) byLineage.set(row.lineage, { head: row, older: [] });
        else found.older.push(row);
    }
    return [...byLineage.values()];
};

/** Where the shown file stands in its card, from 1. */
const positionOf = (shown: Book, rows: BookVariantRow[] | null, kind: string): number => {
    if (!rows) return 1;
    if (kind === 'revisions') {
        const at = rows.findIndex((row) => row.id === shown.id);
        return at < 0 ? 1 : at + 1;
    }
    const lineages = lineagesOf(rows);
    const at = lineages.findIndex(
        (lineage) =>
            lineage.head.id === shown.id || lineage.older.some((row) => row.id === shown.id),
    );
    return at < 0 ? 1 : at + 1;
};

/** The shown file is the newest revision of its document. */
const isLatest = (shown: Book, rows: BookVariantRow[] | null) =>
    !rows || rows.some((row) => row.id === shown.id && row.latest);

const FIELD_NAME: Record<LabelField, string> = {
    translator: 'variantField.translator',
    publisherYear: 'variantField.publisherYear',
    tag: 'variantField.tag',
    revision: 'variantField.revision',
    added: 'variantField.added',
};

const differLine = (fields: LabelField[], t: TFunction) =>
    t('variantsDiffer', {
        fields: (fields.length > 0 ? fields : (['added'] as LabelField[]))
            .map((f) => t(FIELD_NAME[f]))
            .join(', '),
    });

/** A file's small cover, or the tile a file without one gets. */
const Thumb: React.FC<{ book: Book; className?: string }> = ({ book, className }) => {
    const [failed, setFailed] = React.useState(false);
    return (
        <span
            aria-hidden="true"
            data-variant-thumb=""
            className={cn(
                'relative block flex-none overflow-hidden rounded-[3px] bg-muted shadow-[0_1px_3px_rgba(0,0,0,0.14)]',
                className,
            )}
        >
            {book.cover && !failed ? (
                <img
                    src={thumbURL(book)}
                    alt=""
                    // Eager: the list is opened on purpose and is short, and a lazy
                    // image inside the menu's popover was never asked for.
                    loading="eager"
                    onError={() => setFailed(true)}
                    className="size-full object-cover"
                />
            ) : (
                <span className="block size-full border-t-2 border-primary bg-secondary" />
            )}
        </span>
    );
};

/** A row's two lines and its marks, the same in the menu and in the sheet. */
const RowText: React.FC<{ label: string; isDefault: boolean; compact?: boolean }> = ({
    label,
    isDefault,
    compact,
}) => {
    const { t } = useTranslation();
    const [primary, secondary] = labelLines(label);
    return (
        <span className="flex min-w-0 flex-1 flex-col gap-px">
            <span
                className={cn('font-semibold leading-tight', compact ? 'text-sm' : 'text-[15px]')}
            >
                {primary}
                {isDefault && (
                    <span className="ml-1.5 align-[1px] text-[11px] font-bold text-primary">
                        {t('variantDefault')}
                    </span>
                )}
            </span>
            {secondary && (
                <span className="text-[13px] leading-snug text-muted-foreground">{secondary}</span>
            )}
        </span>
    );
};

/** The rows of the list, built once for the menu and the sheet. */
interface ListModel {
    fields: LabelField[];
    lineages: Lineage[];
    labelOf: (row: BookVariantRow, fields?: LabelField[]) => string;
}

const useListModel = (
    book: Book,
    rows: BookVariantRow[] | null,
    kind: string,
): ListModel | null => {
    const { t } = useTranslation();
    return React.useMemo(() => {
        if (!rows) return null;
        // A card of variants tells its documents apart by what differs among
        // them; a revision number tells apart only the revisions folded under
        // a document, which are labelled by it on their own.
        const differing = differingFields(rows);
        // Revisions of one document are told apart by their number alone.
        const fields: LabelField[] =
            kind === 'revisions' ? ['revision'] : differing.filter((f) => f !== 'revision');
        return {
            fields,
            lineages: lineagesOf(rows),
            labelOf: (row, own = fields) => variantLabel(row, own, book.title, t),
        };
    }, [book.title, kind, rows, t]);
};

const VariantSelector: React.FC<VariantSelectorProps> = ({
    book,
    shown,
    rows,
    loading,
    onOpen,
    onSelect,
    layout,
}) => {
    const { t } = useTranslation();
    const [open, setOpen] = React.useState(false);
    const [unfolded, setUnfolded] = React.useState<ReadonlySet<string>>(new Set());
    const info = book.variants!;
    const revisions = info.kind === 'revisions';
    const model = useListModel(book, rows, info.kind);
    const fields = model?.fields ?? fieldsBeforeRows(info.kind);
    // The shown file as its card lists it, once the card's files are read: the
    // list's own book carries no document version.
    const shownRow = rows?.find((row) => row.id === shown.id) ?? shown;
    const shownLabel = variantLabel(shownRow, fields, book.title, t);
    const [shownPrimary, shownSecondary] = labelLines(shownLabel);
    const position = positionOf(shown, rows, info.kind);
    const listed = shown.id === book.id && !info.reason;

    const openChange = (next: boolean) => {
        setOpen(next);
        if (next) onOpen();
    };
    const pick = (bookID: number) => {
        setOpen(false);
        onSelect(bookID);
    };
    const toggleFold = (lineage: string) =>
        setUnfolded((current) => {
            const next = new Set(current);
            if (next.has(lineage)) next.delete(lineage);
            else next.add(lineage);
            return next;
        });

    /** The rows of an open list, drawn by the place they open in. */
    const list = (Item: typeof MenuRow | typeof SheetRow) => {
        if (!model) {
            return (
                <p
                    className="flex items-center gap-2 px-4 py-3 text-sm text-muted-foreground"
                    role="status"
                >
                    <BouncingDots />
                    {loading ? t('variantsLoading') : null}
                </p>
            );
        }
        return model.lineages.map(({ head, older }) => (
            <React.Fragment key={head.lineage}>
                <Item
                    selected={head.id === shown.id}
                    onPick={() => pick(head.id)}
                    thumb={revisions ? null : <Thumb book={head} className="h-[46px] w-8" />}
                >
                    <RowText
                        label={model.labelOf(head)}
                        isDefault={head.default}
                        compact={Item === MenuRow}
                    />
                </Item>
                {older.length > 0 && (
                    <Item
                        fold
                        expanded={unfolded.has(head.lineage)}
                        onPick={() => toggleFold(head.lineage)}
                    >
                        {unfolded.has(head.lineage) ? (
                            <ChevronDown aria-hidden="true" className="size-3.5 flex-none" />
                        ) : (
                            <ChevronRight aria-hidden="true" className="size-3.5 flex-none" />
                        )}
                        <span>{t('earlierRevisions', { count: older.length })}</span>
                    </Item>
                )}
                {unfolded.has(head.lineage) &&
                    older.map((row) => (
                        <Item
                            key={row.id}
                            revision
                            selected={row.id === shown.id}
                            onPick={() => pick(row.id)}
                            thumb={null}
                        >
                            <RowText
                                label={model.labelOf(row, ['revision'])}
                                isDefault={false}
                                compact
                            />
                        </Item>
                    ))}
            </React.Fragment>
        ));
    };

    if (layout === 'chip') {
        const chipText = listed
            ? t(revisions ? 'revisionCount' : 'variantCount', { count: info.count })
            : `${t(revisions ? 'revisionOf' : 'variantOf', { n: position, count: info.count })} · ${shownPrimary}`;
        return (
            <>
                <button
                    type="button"
                    data-testid="variant-chip"
                    aria-haspopup="dialog"
                    onClick={() => openChange(true)}
                    className={cn(
                        'relative inline-flex h-6 max-w-full min-w-0 items-center gap-1 rounded-md border border-border bg-card pr-1.5 pl-[7px] text-[12.5px] font-semibold text-primary dark:border-input',
                        // A finger's 40px around a 24px chip, as the star's hit area does.
                        "before:absolute before:-inset-x-1 before:-inset-y-2 before:content-['']",
                        'outline-none focus-visible:ring-[3px] focus-visible:ring-focus',
                    )}
                >
                    <Layers aria-hidden="true" className="size-[13px] flex-none" />
                    <span className="truncate">{chipText}</span>
                    <ChevronDown aria-hidden="true" className="size-3.5 flex-none" />
                </button>
                <ListSheet
                    open={open}
                    onOpenChange={openChange}
                    title={t(revisions ? 'revisionsTitle' : 'variantsTitle')}
                    description={book.title}
                    data-testid="variant-sheet"
                >
                    {model && (
                        <p className="px-4 pb-2 text-[13px] text-muted-foreground">
                            {differLine(model.fields, t)}
                        </p>
                    )}
                    <div role="list">{list(SheetRow)}</div>
                </ListSheet>
            </>
        );
    }

    return (
        <div data-testid="variant-selector" className="border-b border-border pb-3">
            <p className="mb-1.5 flex justify-between gap-2 text-xs font-semibold text-muted-foreground">
                <span>{t(revisions ? 'revisionLabel' : 'variantLabel')}</span>
                <span>
                    {revisions && isLatest(shown, rows)
                        ? t('revisionLatestOf', { count: info.count })
                        : t('variantPosition', { n: position, count: info.count })}
                </span>
            </p>
            <DropdownMenu modal={false} open={open} onOpenChange={openChange}>
                <DropdownMenuTrigger asChild>
                    <Button
                        variant="outline"
                        aria-label={`${t('variantsChoose')}: ${shownLabel}`}
                        className="h-auto min-h-12 w-full justify-start gap-2 bg-card px-2 py-1.5 text-left font-normal dark:bg-card"
                    >
                        {!revisions && <Thumb book={shown} className="h-8 w-[22px]" />}
                        <span className="flex min-w-0 flex-1 flex-col">
                            <span className="truncate text-[13px] leading-tight font-semibold">
                                {shownPrimary}
                            </span>
                            {shownSecondary && (
                                <span className="truncate text-[11.5px] leading-tight text-muted-foreground">
                                    {shownSecondary}
                                </span>
                            )}
                        </span>
                        <ChevronDown
                            aria-hidden="true"
                            className="size-4 flex-none text-muted-foreground"
                        />
                    </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-[440px] max-w-[calc(100vw-2rem)]">
                    {model && (
                        <DropdownMenuLabel className="px-3 py-1.5 text-[13px] font-normal text-muted-foreground">
                            {differLine(model.fields, t)}
                        </DropdownMenuLabel>
                    )}
                    {list(MenuRow)}
                </DropdownMenuContent>
            </DropdownMenu>
        </div>
    );
};

interface RowProps {
    selected?: boolean;
    /** A row that folds or unfolds a document's earlier revisions. */
    fold?: boolean;
    expanded?: boolean;
    /** An earlier revision, text only and indented under its document. */
    revision?: boolean;
    thumb?: React.ReactNode;
    onPick: () => void;
    children: React.ReactNode;
}

/** A row of the desktop menu: an item of the app's dropdown menu. */
const MenuRow: React.FC<RowProps> = ({
    selected,
    fold,
    expanded,
    revision,
    thumb,
    onPick,
    children,
}) => {
    if (fold) {
        return (
            <DropdownMenuItem
                aria-expanded={expanded}
                // Folding opens the list in place: the menu stays open.
                onSelect={(event) => {
                    event.preventDefault();
                    onPick();
                }}
                className="min-h-10 gap-1.5 pl-14 text-[13px] text-muted-foreground"
            >
                {children}
            </DropdownMenuItem>
        );
    }
    return (
        <DropdownMenuItem
            role="menuitemradio"
            aria-checked={Boolean(selected)}
            onSelect={onPick}
            className={cn(
                'gap-3 px-3',
                revision ? 'min-h-10 pl-14' : 'min-h-14',
                selected && 'bg-secondary',
            )}
        >
            {thumb}
            {children}
            <Check
                aria-hidden="true"
                className={cn('size-[18px] text-primary', !selected && 'invisible')}
            />
        </DropdownMenuItem>
    );
};

/** A row of the phone's sheet, as the sheet's other lists draw theirs. */
const SheetRow: React.FC<RowProps> = ({
    selected,
    fold,
    expanded,
    revision,
    thumb,
    onPick,
    children,
}) => (
    <div role="listitem">
        <button
            type="button"
            aria-expanded={fold ? expanded : undefined}
            aria-current={!fold && selected ? 'true' : undefined}
            onClick={onPick}
            className={cn(
                LIST_SHEET_ROW_CLASS,
                fold && 'gap-1.5 pl-[60px] text-[13px] text-muted-foreground',
                revision && 'pl-[60px]',
                !fold && !revision && 'min-h-16',
                selected && 'bg-secondary',
            )}
        >
            {thumb}
            {children}
            {!fold && (
                <Check
                    aria-hidden="true"
                    className={cn('size-[18px] flex-none text-primary', !selected && 'invisible')}
                />
            )}
        </button>
    </div>
);

export default VariantSelector;
