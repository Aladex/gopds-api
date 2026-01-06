import { useState } from 'react';
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

export interface RescanApprovalResponse {
    message: string;
}

export const useRescan = () => {
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [preview, setPreview] = useState<RescanPreview | null>(null);
    const [coverPreviewUrl, setCoverPreviewUrl] = useState<string | null>(null);
    const [coverLoading, setCoverLoading] = useState(false);
    const [coverError, setCoverError] = useState<string | null>(null);

    const fetchPreview = async (bookId: number): Promise<RescanPreview | null> => {
        setLoading(true);
        setError(null);
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

    const approveRescan = async (
        bookId: number,
        action: 'approve' | 'reject'
    ): Promise<boolean> => {
        setLoading(true);
        setError(null);
        try {
            const response = await fetchWithAuth.post(
                `/admin/books/${bookId}/rescan/approve`,
                { action }
            );
            if (response.status === 200) {
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
        clearCoverPreview();
    };

    return {
        loading,
        error,
        preview,
        coverPreviewUrl,
        coverLoading,
        coverError,
        fetchPreview,
        fetchPreviewCover,
        approveRescan,
        clearCoverPreview,
        clearPreview,
    };
};
