import { http } from '@/api/http';
import type { ParsedItem } from '@/features/admin/CuratedCollections/csvParser';

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

export interface AIDecision {
    item_id: number;
    external_title: string;
    action: 'resolved' | 'skipped';
    book_id?: number;
    book_title?: string;
}

export interface AIResolveProgress {
    running: boolean;
    processed: number;
    total: number;
    resolved: number;
    recent: AIDecision[];
    started_at?: string;
    updated_at?: string;
}

export interface ImportStatusInfo {
    status: string;
    import_error?: string;
    stats: {
        matched?: number;
        ambiguous?: number;
        not_found?: number;
        processed?: number;
        total?: number;
        ai_progress?: AIResolveProgress;
    };
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
    return http.post<{ collection_id: number; status: string }>('/admin/collections', {
        name,
        source_url: sourceUrl || undefined,
        items,
    });
};

export interface CuratedCollectionsPage {
    rows: CuratedCollection[];
    total: number;
    page: number;
    page_size: number;
}

export const listCuratedCollections = async (page = 1, pageSize = 25): Promise<CuratedCollectionsPage> => {
    const body = await http.get<CuratedCollectionsPage | CuratedCollection[]>('/admin/collections', {
        query: { page, page_size: pageSize },
    });
    // tolerate the legacy {array} shape during a deploy window
    if (Array.isArray(body)) {
        return { rows: body, total: body.length, page, page_size: pageSize };
    }
    return body ?? { rows: [], total: 0, page, page_size: pageSize };
};

export const getCuratedCollection = async (id: number): Promise<CuratedCollection> => {
    return http.get<CuratedCollection>(`/admin/collections/${id}`);
};

export const getImportStatus = async (id: number): Promise<ImportStatusInfo> => {
    return http.get<ImportStatusInfo>(`/admin/collections/${id}/status`);
};

export const listCollectionItems = async (
    id: number,
    statusFilter: string,
    page = 1,
    pageSize = 500,
): Promise<ItemsPage> => {
    return http.get<ItemsPage>(`/admin/collections/${id}/items`, {
        query: { status: statusFilter, page, page_size: pageSize },
    });
};

export const resolveItem = async (
    collectionID: number,
    itemID: number,
    bookID: number,
): Promise<void> => {
    await http.post(`/admin/collections/${collectionID}/items/${itemID}/resolve`, {
        book_id: bookID,
    });
};

export const ignoreItem = async (collectionID: number, itemID: number): Promise<void> => {
    await http.post(`/admin/collections/${collectionID}/items/${itemID}/ignore`);
};

export const autoResolveCollection = async (collectionID: number): Promise<{ resolved: number }> => {
    return http.post<{ resolved: number }>(`/admin/collections/${collectionID}/auto-resolve`);
};

export const llmResolveCollection = async (collectionID: number): Promise<{ resolved: number }> => {
    return http.post<{ resolved: number }>(`/admin/collections/${collectionID}/llm-resolve`);
};

export const llmSearchNotFound = async (collectionID: number): Promise<{ status: string }> => {
    return http.post<{ status: string }>(`/admin/collections/${collectionID}/llm-search-not-found`);
};

export const patchCuratedCollection = async (
    id: number,
    patch: { name?: string; is_public?: boolean; source_url?: string },
): Promise<void> => {
    await http.patch(`/admin/collections/${id}`, patch);
};

export const deleteCuratedCollection = async (id: number): Promise<void> => {
    await http.delete(`/admin/collections/${id}`);
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
    const body = await http.get<LookupBook[]>('/admin/books/lookup', {
        query: { ids: ids.join(',') },
    });
    return body ?? [];
};
