import { fetchWithAuth } from '../../../api/config';

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
    const resp = await fetchWithAuth.get('/collections', { params: { page, page_size: pageSize } });
    if (Array.isArray(resp.data)) {
        return { rows: resp.data, total: resp.data.length, page, page_size: pageSize };
    }
    return resp.data ?? { rows: [], total: 0, page, page_size: pageSize };
};

export const getPublicCollection = async (id: number): Promise<PublicCollectionDetail> => {
    const resp = await fetchWithAuth.get(`/collections/${id}`);
    return resp.data;
};
