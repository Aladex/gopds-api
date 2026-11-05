import { API_URL } from '@/api/config';

/** A book as far as its cover's address is concerned. */
export interface CoverSource {
    path: string;
    filename: string;
}

/** The sanitising the cover routes expect, mirroring internal/posters. */
const segment = (value: string) => value.replaceAll('.', '-').replace(/^\/+/, '');

/**
 * The address of a book's thumbnail: a small JPEG for lists, made on demand.
 * Unlike /books-posters/ it answers 404 for a missing or broken cover, so an
 * <img> using it needs an onError that draws something else.
 */
export const thumbURL = (book: CoverSource): string =>
    `${API_URL}/books-thumbs/${segment(book.path)}/${segment(book.filename)}.jpg`;
