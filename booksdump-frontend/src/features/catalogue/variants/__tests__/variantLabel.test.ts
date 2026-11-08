import i18next from 'i18next';

import type { BookVariantRow } from '@/api/books';
import { differingFields, variantLabel } from '@/features/catalogue/variants/variantLabel';
import ruTranslation from '@/locales/ru/translation.json';

/*
 * A variant's label says only what differs inside its card, in a fixed order:
 * translator, publisher and year, the stripped tag, revision and file date,
 * added date. Dates are made from now, never written down.
 */

const DAY = 24 * 60 * 60 * 1000;
const isoDay = (offsetDays: number) =>
    new Date(Date.now() - offsetDays * DAY).toISOString().slice(0, 10);
const shown = (iso: string) => iso.split('-').reverse().join('.');

const i18n = i18next.createInstance();
await i18n.init({
    lng: 'ru',
    resources: { ru: { translation: ruTranslation } },
    keySeparator: false,
});
const t = i18n.t.bind(i18n);

const row = (over: Partial<BookVariantRow>): BookVariantRow => ({
    id: 1,
    title: 'Хоббит, или Туда и обратно',
    authors: [],
    series: [],
    genres: [],
    annotation: '',
    filename: 'f',
    cover: false,
    registerdate: `${isoDay(30)}T10:00:00Z`,
    docdate: isoDay(400),
    lang: 'ru',
    fav: false,
    approved: true,
    path: 'p',
    format: 'fb2',
    favorite_count: 0,
    publisher: null,
    isbn: [],
    lineage: 'doc-1',
    latest: true,
    rank: 1,
    tag: '',
    doc_version: '1.0',
    default: false,
    ...over,
});

describe('differingFields', () => {
    it('names translator and publisher for the Hobbit', () => {
        const rows = [
            row({
                id: 1,
                translators: ['Кирилл Королёв'],
                publisher: 'АСТ',
                docdate: '2025',
                lineage: 'a',
            }),
            row({
                id: 2,
                translators: ['Наталья Прохорова'],
                publisher: 'Миликон-пресс',
                docdate: '2005',
                lineage: 'b',
            }),
        ];
        expect(differingFields(rows)).toEqual(['translator', 'publisherYear']);
        expect(variantLabel(rows[1], ['translator', 'publisherYear'], rows[1].title, t)).toBe(
            'пер. Н. Прохорова · Миликон-пресс, 2005',
        );
    });

    it('names the revision for revisions of one document', () => {
        const day = isoDay(5);
        const rows = [
            row({ id: 1, doc_version: '2.1', docdate: day }),
            row({ id: 2, doc_version: '2.0', docdate: day, latest: false, rank: 2 }),
        ];
        expect(differingFields(rows)).toEqual(['revision']);
        expect(variantLabel(rows[0], ['revision'], rows[0].title, t)).toBe(
            `ред. 2.1 · файл ${shown(day)}`,
        );
    });

    it('names nothing for identical rows, and the label is the added date', () => {
        const rows = [row({ id: 1 }), row({ id: 2, lineage: 'doc-2' })];
        expect(differingFields(rows)).toEqual([]);
        expect(variantLabel(rows[0], [], rows[0].title, t)).toBe(`добавлено ${shown(isoDay(30))}`);
    });

    it('counts a revision only inside one document', () => {
        const rows = [
            row({ id: 1, lineage: 'a', doc_version: '1.0', docdate: '2001' }),
            row({ id: 2, lineage: 'b', doc_version: '2.3', docdate: '2015' }),
        ];
        expect(differingFields(rows)).not.toContain('revision');
        expect(
            differingFields([
                ...rows,
                row({ id: 3, lineage: 'b', doc_version: '2.2', latest: false }),
            ]),
        ).toContain('revision');
    });

    it('keeps the order translator, publisher and year, tag, revision, added', () => {
        const rows = [
            row({
                id: 1,
                translators: ['А Б'],
                publisher: 'X',
                tag: 'litres',
                doc_version: '1',
                registerdate: `${isoDay(2)}T00:00:00Z`,
            }),
            row({
                id: 2,
                translators: ['В Г'],
                publisher: 'Y',
                tag: 'СИ',
                doc_version: '2',
                registerdate: `${isoDay(3)}T00:00:00Z`,
            }),
        ];
        expect(differingFields(rows)).toEqual([
            'translator',
            'publisherYear',
            'tag',
            'revision',
            'added',
        ]);
    });
});

describe('variantLabel', () => {
    it('shortens a team of thirty to two names and «и др.»', () => {
        const team = row({
            translators: ['Евгений Михайлович Солонович', 'Илья Голенищев-Кутузов'],
            translators_more: true,
        });
        expect(variantLabel(team, ['translator'], team.title, t)).toBe(
            'пер. Е. М. Солонович, И. Голенищев-Кутузов и др.',
        );
    });

    it('keeps initials that are already initials', () => {
        const r = row({ translators: ['К. Королёв'] });
        expect(variantLabel(r, ['translator'], r.title, t)).toBe('пер. К. Королёв');
    });

    it('says «как …» when the file has a title of its own', () => {
        const r = row({ title: 'Хоббит', translators: ['Нора Галь'] });
        expect(variantLabel(r, ['translator'], 'Хоббит, или Туда и обратно', t)).toBe(
            'пер. Н. Галь · как «Хоббит»',
        );
    });

    it('says nothing of a title that differs only in case, ё or punctuation', () => {
        const r = row({ title: 'Хоббит или туда и обратно', translators: ['Нора Галь'] });
        expect(variantLabel(r, ['translator'], 'Хоббит, или Туда и Обратно', t)).toBe(
            'пер. Н. Галь',
        );
        const yo = row({ title: 'Ёжик в тумане', translators: ['Нора Галь'] });
        expect(variantLabel(yo, ['translator'], 'Ежик в тумане', t)).toBe('пер. Н. Галь');
    });

    it('shows the stripped tag in brackets', () => {
        const r = row({ tag: 'litres' });
        expect(variantLabel(r, ['tag'], r.title, t)).toBe('[litres]');
    });

    it('leaves out a field this row has nothing for', () => {
        const r = row({ translators: [], publisher: 'АСТ', docdate: '' });
        expect(variantLabel(r, ['translator', 'publisherYear'], r.title, t)).toBe('АСТ');
    });
});
