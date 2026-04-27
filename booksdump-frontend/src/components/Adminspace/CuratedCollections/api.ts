import { fetchWithAuth } from '../../../api/config';
import type { ParsedItem } from './csvParser';

export interface CuratedCollection {
    id: number;
    name: string;
    is_public: boolean;
    is_curated: boolean;
    source_url?: string;
    import_status?: string;
    import_error?: string;
    imported_at?: string | null;
    import_stats?: { matched?: number; ambiguous?: number; not_found?: number };
    created_at?: string;
}

export interface CollectionItem {
    id: number;
    collection_id: number;
    book_id?: number | null;
    external_title: string;
    external_author: string;
    external_extra?: any;
    match_status: string;
    match_score?: number;
    position: number;
}

export interface ImportStatusInfo {
    status: string;
    import_error?: string;
    stats: { matched?: number; ambiguous?: number; not_found?: number };
}

export interface ItemsPage {
    items: CollectionItem[];
    total: number;
    page: number;
    page_size: number;
}

export const importCuratedCollection = async (
    name: string,
    sourceUrl: string,
    items: ParsedItem[],
): Promise<{ collection_id: number; status: string }> => {
    const resp = await fetchWithAuth.post('/admin/collections', {
        name,
        source_url: sourceUrl || undefined,
        items,
    });
    return resp.data;
};

export const listCuratedCollections = async (): Promise<CuratedCollection[]> => {
    const resp = await fetchWithAuth.get('/admin/collections');
    return resp.data ?? [];
};

export const getCuratedCollection = async (id: number): Promise<CuratedCollection> => {
    const resp = await fetchWithAuth.get(`/admin/collections/${id}`);
    return resp.data;
};

export const getImportStatus = async (id: number): Promise<ImportStatusInfo> => {
    const resp = await fetchWithAuth.get(`/admin/collections/${id}/status`);
    return resp.data;
};

export const listCollectionItems = async (
    id: number,
    statusFilter: string,
    page = 1,
    pageSize = 50,
): Promise<ItemsPage> => {
    const resp = await fetchWithAuth.get(`/admin/collections/${id}/items`, {
        params: { status: statusFilter, page, page_size: pageSize },
    });
    return resp.data;
};

export const resolveItem = async (
    collectionID: number,
    itemID: number,
    bookID: number,
): Promise<void> => {
    await fetchWithAuth.post(
        `/admin/collections/${collectionID}/items/${itemID}/resolve`,
        { book_id: bookID },
    );
};

export const ignoreItem = async (collectionID: number, itemID: number): Promise<void> => {
    await fetchWithAuth.post(`/admin/collections/${collectionID}/items/${itemID}/ignore`);
};

export const patchCuratedCollection = async (
    id: number,
    patch: { name?: string; is_public?: boolean; source_url?: string },
): Promise<void> => {
    await fetchWithAuth.patch(`/admin/collections/${id}`, patch);
};

export const deleteCuratedCollection = async (id: number): Promise<void> => {
    await fetchWithAuth.delete(`/admin/collections/${id}`);
};

// LookupBook is the slim shape returned by GET /admin/books/lookup — enough to
// render a candidate chip with the real title and author of a local book.
export interface LookupBook {
    id: number;
    title: string;
    authors?: { id: number; full_name: string }[];
}

export const lookupBooksByIDs = async (ids: number[]): Promise<LookupBook[]> => {
    if (ids.length === 0) return [];
    const resp = await fetchWithAuth.get('/admin/books/lookup', {
        params: { ids: ids.join(',') },
    });
    return resp.data ?? [];
};
