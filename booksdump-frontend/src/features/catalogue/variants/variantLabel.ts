import type { TFunction } from 'i18next';

import type { Book, BookVariantRow } from '@/api/books';
import { formatCalendarDay } from '@/shared/lib/formatDate';

/**
 * A variant's label says only what differs inside its card, so that six
 * «Хоббит»s read as six translations and twenty-two samizdat files as twenty-
 * two revisions. The fields, in the order a label lists them: the translator,
 * the publisher and year, the stripped tag, the revision with its file date,
 * the date the file was added.
 */
export type LabelField = 'translator' | 'publisherYear' | 'tag' | 'revision' | 'added';

export const LABEL_FIELDS: readonly LabelField[] = [
    'translator',
    'publisherYear',
    'tag',
    'revision',
    'added',
];

/** The year a document date names: its first four-digit run, or none. */
const yearOf = (docdate: string) => /\b(\d{4})\b/.exec(docdate)?.[1] ?? '';

/** The day a date names, as dd.MM.yyyy; '' for none. */
const dayOf = (value: string) => formatCalendarDay(value) ?? '';

/**
 * A name as a label shortens it: initials and the surname, the last word
 * taken as the surname, as the file's credit lists it. A name of one word,
 * and initials already written as initials, stay as they are.
 */
export const shortName = (name: string): string => {
    const words = name.trim().split(/\s+/).filter(Boolean);
    if (words.length < 2) return words.join('');
    const surname = words[words.length - 1];
    const initials = words
        .slice(0, -1)
        .map((word) => (word.endsWith('.') ? word : `${word.charAt(0)}.`));
    return [...initials, surname].join(' ');
};

/** What a row is compared by for a field: equal values make the same label. */
const fieldKey = (row: Book & Partial<BookVariantRow>, field: LabelField): string => {
    switch (field) {
        case 'translator':
            return `${(row.translators ?? []).join('\u001f')}|${row.translators_more ? 1 : 0}`;
        case 'publisherYear':
            return `${row.publisher?.trim() ?? ''}|${yearOf(row.docdate)}`;
        case 'tag':
            return row.tag ?? '';
        case 'revision':
            return `${row.doc_version ?? ''}|${dayOf(row.docdate)}`;
        case 'added':
            return dayOf(row.registerdate);
    }
};

/**
 * The fields whose values differ among the rows, in the label's order. The
 * revision tells files apart only inside one document: across documents (two
 * translations) their version numbers and file dates differ as a matter of
 * course and say nothing a reader chooses by.
 */
export function differingFields(rows: BookVariantRow[]): LabelField[] {
    return LABEL_FIELDS.filter((field) => {
        if (field !== 'revision') return new Set(rows.map((row) => fieldKey(row, field))).size > 1;
        const byLineage = new Map<string, Set<string>>();
        for (const row of rows) {
            const keys = byLineage.get(row.lineage) ?? new Set<string>();
            keys.add(fieldKey(row, field));
            byLineage.set(row.lineage, keys);
        }
        return [...byLineage.values()].some((keys) => keys.size > 1);
    });
}

/** One field of a row as the label says it; '' when the row has nothing for it. */
export function fieldText(
    row: Book & Partial<BookVariantRow>,
    field: LabelField,
    t: TFunction,
): string {
    switch (field) {
        case 'translator': {
            const names = (row.translators ?? []).map(shortName).filter(Boolean);
            if (names.length === 0) return '';
            const team =
                names.join(', ') + (row.translators_more ? ` ${t('bookTranslatorsMore')}` : '');
            return t('variantTranslator', { names: team });
        }
        case 'publisherYear':
            return [row.publisher?.trim(), yearOf(row.docdate)].filter(Boolean).join(', ');
        case 'tag':
            return row.tag ? `[${row.tag}]` : '';
        case 'revision': {
            const day = dayOf(row.docdate);
            return [
                row.doc_version ? t('revisionShort', { v: row.doc_version }) : '',
                day ? t('fileDate', { date: day }) : '',
            ]
                .filter(Boolean)
                .join(' · ');
        }
        case 'added': {
            const day = dayOf(row.registerdate);
            return day ? t('variantAdded', { date: day }) : '';
        }
    }
}

/**
 * A title as the card compares it: case, ё, punctuation and spacing aside, so
 * «Хоббит или туда и обратно» is the card's «Хоббит, или Туда и Обратно».
 */
const titleKey = (title: string) =>
    title
        .toLowerCase()
        .replaceAll('ё', 'е')
        .replace(/[^\p{L}\p{N}]+/gu, ' ')
        .trim();

/**
 * A row's label: the fields that differ in its card, or its added date when
 * nothing does, and «как «…»» when the file carries a title of its own.
 */
export function variantLabel(
    row: Book & Partial<BookVariantRow>,
    fields: readonly LabelField[],
    cardTitle: string,
    t: TFunction,
): string {
    const parts = fields.map((field) => fieldText(row, field, t)).filter(Boolean);
    if (parts.length === 0) {
        const added = fieldText(row, 'added', t);
        if (added) parts.push(added);
    }
    if (titleKey(row.title) !== titleKey(cardTitle)) {
        parts.push(t('variantAs', { title: row.title }));
    }
    return parts.join(' · ');
}

/** A label as two lines: what tells the file apart, and the rest of it. */
export function labelLines(label: string): [string, string] {
    const cut = label.indexOf(' · ');
    return cut < 0 ? [label, ''] : [label.slice(0, cut), label.slice(cut + 3)];
}
