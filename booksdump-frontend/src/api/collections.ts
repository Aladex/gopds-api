import { http } from './http';

export interface CollectionCoverBook {
    id: number;
    path: string;
    filename: string;
    cover: boolean;
    title: string;
}

// PublicCollectionRow is the shape of one row from GET /api/collections.
// Backend strips all admin-only fields; the frontend never sees source_url,
// import_*, match_*, external_* or even is_curated/is_public.
export interface PublicCollectionRow {
    id: number;
    name: string;
    created_at?: string;
    cover_books?: CollectionCoverBook[];
}

// PublicCollectionDetail is the shape of one row from GET /api/collections/:id.
// books reuse the project-wide Book shape — title, authors, cover etc.
export interface PublicCollectionBook {
    id: number;
    title: string;
    authors?: { id: number; full_name: string }[];
    cover?: boolean;
    lang?: string;
    annotation?: string;
    path?: string;
    filename?: string;
}

export interface PublicCollectionDetail extends PublicCollectionRow {
    books: PublicCollectionBook[];
}

export interface PublicCollectionsPage {
    rows: PublicCollectionRow[];
    total: number;
    page: number;
    page_size: number;
}

export const listPublicCollections = async (page = 1, pageSize = 12): Promise<PublicCollectionsPage> => {
    const body = await http.get<PublicCollectionsPage | PublicCollectionRow[]>('/collections', {
        query: { page, page_size: pageSize },
    });
    // The endpoint has returned a bare array in the past; keep tolerating it.
    if (Array.isArray(body)) {
        return { rows: body, total: body.length, page, page_size: pageSize };
    }
    return body ?? { rows: [], total: 0, page, page_size: pageSize };
};

export const getPublicCollection = async (id: number): Promise<PublicCollectionDetail> =>
    http.get<PublicCollectionDetail>(`/collections/${id}`);
