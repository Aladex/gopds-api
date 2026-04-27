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

export const listPublicCollections = async (): Promise<PublicCollectionRow[]> => {
    const resp = await fetchWithAuth.get('/collections');
    return resp.data ?? [];
};

export const getPublicCollection = async (id: number): Promise<PublicCollectionDetail> => {
    const resp = await fetchWithAuth.get(`/collections/${id}`);
    return resp.data;
};
