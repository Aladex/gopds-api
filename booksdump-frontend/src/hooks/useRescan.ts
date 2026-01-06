import { useState, useCallback } from 'react';
import { fetchWithAuth } from '../api/config';

interface Author {
    id: number;
    name: string;
}

interface Series {
    id: number;
    title: string;
    index: string;
}

interface BookData {
    title: string;
    lang: string;
    annotation: string;
    docdate: string;
    authors: Author[];
    series: Series | null;
    tags: string[];
    has_cover: boolean;
}

export interface RescanPreview {
    book_id: number;
    pending_rescan_id: number;
    old: BookData;
    new: BookData;
    diff: string[];
}

export interface FieldSelection {
    updateTitle: boolean;
    updateAnnotation: boolean;
    updateLang: boolean;
    updateDocDate: boolean;
    updateAuthors: boolean;
    updateSeries: boolean;
    updateCover: boolean;
    updateTags: boolean;
}

export interface RescanApprovalRequest {
    action: 'approve' | 'reject';
    update_title?: boolean;
    update_annotation?: boolean;
    update_lang?: boolean;
    update_docdate?: boolean;
    update_authors?: boolean;
    update_series?: boolean;
    update_cover?: boolean;
    update_tags?: boolean;
}

export interface RescanApprovalResponse {
    success: boolean;
    message: string;
    book_id: number;
    action: string;
    updated_fields?: string[];
    skipped_fields?: string[];
}

const defaultFieldSelection: FieldSelection = {
    updateTitle: true,
    updateAnnotation: true,
    updateLang: true,
    updateDocDate: true,
    updateAuthors: true,
    updateSeries: true,
    updateCover: true,
    updateTags: true,
};

export const useRescan = () => {
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [preview, setPreview] = useState<RescanPreview | null>(null);
    const [coverPreviewUrl, setCoverPreviewUrl] = useState<string | null>(null);
    const [coverLoading, setCoverLoading] = useState(false);
    const [coverError, setCoverError] = useState<string | null>(null);
    const [fieldSelection, setFieldSelection] = useState<FieldSelection>(defaultFieldSelection);
    const [approvalResponse, setApprovalResponse] = useState<RescanApprovalResponse | null>(null);

    const fetchPreview = async (bookId: number): Promise<RescanPreview | null> => {
        setLoading(true);
        setError(null);
        // Reset field selection to defaults when fetching new preview
        setFieldSelection(defaultFieldSelection);
        try {
            const response = await fetchWithAuth.post(`/admin/books/${bookId}/rescan`);
            if (response.status === 200 && response.data.result) {
                const previewData = response.data.result;
                setPreview(previewData);
                return previewData;
            } else {
                const errorMsg = response.data.error || 'Failed to fetch rescan preview';
                setError(errorMsg);
                return null;
            }
        } catch (err: any) {
            console.error('Error fetching rescan preview:', err);
            const errorMsg = err.response?.data?.detail || err.response?.data?.error || 'Failed to fetch rescan preview';
            setError(errorMsg);
            return null;
        } finally {
            setLoading(false);
        }
    };

    const toggleField = useCallback((field: keyof FieldSelection) => {
        setFieldSelection(prev => ({
            ...prev,
            [field]: !prev[field]
        }));
    }, []);

    const selectAllFields = useCallback(() => {
        setFieldSelection(defaultFieldSelection);
    }, []);

    const deselectAllFields = useCallback(() => {
        setFieldSelection({
            updateTitle: false,
            updateAnnotation: false,
            updateLang: false,
            updateDocDate: false,
            updateAuthors: false,
            updateSeries: false,
            updateCover: false,
            updateTags: false,
        });
    }, []);

    const getSelectedFieldsCount = useCallback((diff: string[]): { selected: number; total: number } => {
        const fieldMap: Record<string, keyof FieldSelection> = {
            'title': 'updateTitle',
            'annotation': 'updateAnnotation',
            'lang': 'updateLang',
            'docdate': 'updateDocDate',
            'authors': 'updateAuthors',
            'series': 'updateSeries',
            'cover': 'updateCover',
            'tags': 'updateTags',
        };

        let selected = 0;
        const changedFields = diff.filter(d => fieldMap[d]);

        changedFields.forEach(field => {
            const selectionKey = fieldMap[field];
            if (selectionKey && fieldSelection[selectionKey]) {
                selected++;
            }
        });

        return { selected, total: changedFields.length };
    }, [fieldSelection]);

    const approveRescan = async (
        bookId: number,
        action: 'approve' | 'reject',
        selectedFields?: FieldSelection
    ): Promise<boolean> => {
        setLoading(true);
        setError(null);
        setApprovalResponse(null);
        try {
            const body: RescanApprovalRequest = { action };

            if (action === 'approve' && selectedFields) {
                body.update_title = selectedFields.updateTitle;
                body.update_annotation = selectedFields.updateAnnotation;
                body.update_lang = selectedFields.updateLang;
                body.update_docdate = selectedFields.updateDocDate;
                body.update_authors = selectedFields.updateAuthors;
                body.update_series = selectedFields.updateSeries;
                body.update_cover = selectedFields.updateCover;
                body.update_tags = selectedFields.updateTags;
            }

            const response = await fetchWithAuth.post(
                `/admin/books/${bookId}/rescan/approve`,
                body
            );
            if (response.status === 200) {
                if (response.data.result) {
                    setApprovalResponse(response.data.result);
                }
                setPreview(null);
                return true;
            } else {
                const errorMsg = response.data.error || 'Failed to process rescan action';
                setError(errorMsg);
                return false;
            }
        } catch (err: any) {
            console.error('Error processing rescan action:', err);
            const errorMsg = err.response?.data?.detail || err.response?.data?.error || 'Failed to process rescan action';
            setError(errorMsg);
            return false;
        } finally {
            setLoading(false);
        }
    };

    const fetchPreviewCover = async (bookId: number): Promise<string | null> => {
        setCoverLoading(true);
        setCoverError(null);
        try {
            const response = await fetchWithAuth.get(`/admin/books/${bookId}/rescan/preview-cover`, {
                responseType: 'blob',
            });
            if (response.status === 200) {
                if (coverPreviewUrl) {
                    URL.revokeObjectURL(coverPreviewUrl);
                }
                const objectUrl = URL.createObjectURL(response.data);
                setCoverPreviewUrl(objectUrl);
                return objectUrl;
            }
            setCoverError('Failed to fetch rescan cover preview');
            return null;
        } catch (err: any) {
            const status = err.response?.status;
            if (status !== 404) {
                console.error('Error fetching rescan cover preview:', err);
                setCoverError('Failed to fetch rescan cover preview');
            }
            return null;
        } finally {
            setCoverLoading(false);
        }
    };

    const clearCoverPreview = () => {
        if (coverPreviewUrl) {
            URL.revokeObjectURL(coverPreviewUrl);
        }
        setCoverPreviewUrl(null);
        setCoverError(null);
        setCoverLoading(false);
    };

    const clearPreview = () => {
        setPreview(null);
        setError(null);
        setFieldSelection(defaultFieldSelection);
        setApprovalResponse(null);
        clearCoverPreview();
    };

    return {
        loading,
        error,
        preview,
        coverPreviewUrl,
        coverLoading,
        coverError,
        fieldSelection,
        approvalResponse,
        fetchPreview,
        fetchPreviewCover,
        approveRescan,
        clearCoverPreview,
        clearPreview,
        toggleField,
        selectAllFields,
        deselectAllFields,
        getSelectedFieldsCount,
    };
};
