import React, { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { CheckCircle2, XCircle } from 'lucide-react';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { BouncingDots } from '@/shared/ui/bouncing-dots';
import { Badge } from '@/shared/ui/badge';
import { Button } from '@/shared/ui/button';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/shared/ui/dialog';
import { cn } from '@/shared/lib/utils';

import { useRescan, type FieldSelection } from '@/features/catalogue/hooks/useRescan';

interface Author {
    id: number;
    name: string;
}

interface Series {
    id: number;
    title: string;
    index: string;
}

type RescanPreviewDialogProps = {
    open: boolean;
    onClose: () => void;
    bookId: number | null;
    onRescanCompleted: () => void;
};

/**
 * The tick boxes, in the order the fields are shown above them. The rescan
 * offers one per field it would actually change, so a librarian can take the
 * new annotation without losing the author they corrected by hand.
 */
const SELECTABLE_FIELDS: Array<{
    field: string;
    selection: keyof FieldSelection;
    labelKey: string;
}> = [
    { field: 'title', selection: 'updateTitle', labelKey: 'title' },
    { field: 'annotation', selection: 'updateAnnotation', labelKey: 'annotation' },
    { field: 'lang', selection: 'updateLang', labelKey: 'language' },
    { field: 'docdate', selection: 'updateDocDate', labelKey: 'publicationDate' },
    { field: 'authors', selection: 'updateAuthors', labelKey: 'authors' },
    { field: 'series', selection: 'updateSeries', labelKey: 'series' },
    { field: 'cover', selection: 'updateCover', labelKey: 'rescanCover' },
    { field: 'tags', selection: 'updateTags', labelKey: 'rescanTags' },
];

const RescanPreviewDialog: React.FC<RescanPreviewDialogProps> = ({
    open,
    onClose,
    bookId,
    onRescanCompleted,
}) => {
    const { t } = useTranslation();
    const {
        loading,
        error,
        preview,
        coverPreviewUrl,
        coverLoading,
        coverError,
        fieldSelection,
        fetchPreview,
        fetchPreviewCover,
        clearCoverPreview,
        approveRescan,
        clearPreview,
        toggleField,
        selectAllFields,
        deselectAllFields,
        getSelectedFieldsCount,
    } = useRescan();

    useEffect(() => {
        if (open && bookId) {
            fetchPreview(bookId);
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [open, bookId]);

    useEffect(() => {
        if (preview && bookId && preview.new.has_cover) {
            fetchPreviewCover(bookId);
            return;
        }
        clearCoverPreview();
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [preview, bookId]);

    const handleClose = () => {
        if (!loading) {
            clearPreview();
            onClose();
        }
    };

    const handleApprove = async () => {
        if (!bookId) return;
        const success = await approveRescan(bookId, 'approve', fieldSelection);
        if (success) {
            onRescanCompleted();
            handleClose();
        }
    };

    const handleReject = async () => {
        if (!bookId) return;
        const success = await approveRescan(bookId, 'reject');
        if (success) {
            onRescanCompleted();
            handleClose();
        }
    };

    const isDifferent = (field: string) => preview?.diff.includes(field);

    /**
     * A field, before and after, side by side.
     *
     * A changed pair is tinted rather than merely badged — red for what is being
     * dropped, green for what would replace it — so the extent of a rescan reads
     * at a glance from a page of otherwise identical rows.
     */
    const renderField = (
        label: string,
        oldValue: React.ReactNode,
        newValue: React.ReactNode,
        fieldName: string,
    ) => {
        const isChanged = isDifferent(fieldName);

        return (
            <div className="mb-4">
                <p className="mb-1 flex items-center gap-2 text-xs font-medium text-muted-foreground">
                    {label}
                    {isChanged && (
                        <Badge
                            variant="outline"
                            className="border-amber-500/50 text-amber-600 dark:text-amber-400"
                        >
                            {t('rescanChanged')}
                        </Badge>
                    )}
                </p>
                <div className="grid grid-cols-2 gap-2">
                    <div
                        className={cn(
                            'rounded-md border p-2',
                            isChanged
                                ? 'border-destructive/40 bg-destructive/10'
                                : 'border-border bg-background',
                        )}
                    >
                        <p className="mb-0.5 text-[11px] text-muted-foreground">
                            {t('rescanOldValue')}
                        </p>
                        <p className="text-sm break-words">{oldValue || t('rescanEmpty')}</p>
                    </div>
                    <div
                        className={cn(
                            'rounded-md border p-2',
                            isChanged
                                ? 'border-green-600/40 bg-green-500/10 dark:border-green-400/40'
                                : 'border-border bg-background',
                        )}
                    >
                        <p className="mb-0.5 text-[11px] text-muted-foreground">
                            {t('rescanNewValue')}
                        </p>
                        <p className="text-sm break-words">{newValue || t('rescanEmpty')}</p>
                    </div>
                </div>
            </div>
        );
    };

    const renderAuthors = (authors: Author[]) =>
        authors.map((a) => a.name).join(', ') || t('noAuthors');

    const renderSeries = (series: Series | null) => {
        if (!series) return t('noSeries');
        return series.title + (series.index ? ` #${series.index}` : '');
    };

    const renderTags = (tags: string[]) => tags.join(', ') || t('rescanNoTags');

    const counts = preview ? getSelectedFieldsCount(preview.diff) : { selected: 0, total: 0 };

    return (
        <Dialog open={open} onOpenChange={(next) => !next && handleClose()}>
            <DialogContent
                closeLabel={t('close')}
                className="flex max-h-[90vh] flex-col gap-0 p-0 sm:max-w-2xl"
            >
                <DialogHeader className="border-b border-border px-6 py-4 pr-12">
                    <DialogTitle>{t('rescanPreview')}</DialogTitle>
                    <DialogDescription className="sr-only">
                        {t('rescanPreviewDescription')}
                    </DialogDescription>
                </DialogHeader>

                <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4 scrollbar-thin">
                    {error && (
                        <Alert variant="destructive" className="mb-4">
                            <AlertDescription>{error}</AlertDescription>
                        </Alert>
                    )}

                    {loading && !preview && (
                        <div role="status" className="flex items-center justify-center py-10">
                            <BouncingDots size="lg" className="text-muted-foreground" />
                            <span className="sr-only">{t('loading')}</span>
                        </div>
                    )}

                    {preview && (
                        <div>
                            <Alert className="mb-4">
                                <AlertDescription>{t('rescanPreviewDescription')}</AlertDescription>
                            </Alert>

                            {renderField(t('title'), preview.old.title, preview.new.title, 'title')}
                            {renderField(t('language'), preview.old.lang, preview.new.lang, 'lang')}
                            {renderField(
                                t('authors'),
                                renderAuthors(preview.old.authors),
                                renderAuthors(preview.new.authors),
                                'authors',
                            )}
                            {renderField(
                                t('series'),
                                renderSeries(preview.old.series),
                                renderSeries(preview.new.series),
                                'series',
                            )}
                            {renderField(
                                t('rescanTags'),
                                renderTags(preview.old.tags),
                                renderTags(preview.new.tags),
                                'tags',
                            )}
                            {renderField(
                                t('publicationDate'),
                                preview.old.docdate,
                                preview.new.docdate,
                                'docdate',
                            )}
                            {renderField(
                                t('annotation'),
                                preview.old.annotation
                                    ? preview.old.annotation.substring(0, 200) + '...'
                                    : '',
                                preview.new.annotation
                                    ? preview.new.annotation.substring(0, 200) + '...'
                                    : '',
                                'annotation',
                            )}
                            {renderField(
                                t('rescanCover'),
                                preview.old.has_cover ? t('rescanHasCover') : t('rescanNoCover'),
                                preview.new.has_cover ? t('rescanHasCover') : t('rescanNoCover'),
                                'cover',
                            )}

                            {preview.new.has_cover && (
                                <div className="mt-2">
                                    <p className="mb-1 text-xs font-medium text-muted-foreground">
                                        {t('rescanCoverPreview')}
                                    </p>
                                    {coverError && (
                                        <Alert className="mb-2 border-amber-500/50">
                                            <AlertDescription className="text-amber-600 dark:text-amber-400">
                                                {coverError}
                                            </AlertDescription>
                                        </Alert>
                                    )}
                                    {coverLoading && (
                                        <div className="flex items-center gap-2 text-sm text-muted-foreground">
                                            <BouncingDots />
                                            {t('loading')}
                                        </div>
                                    )}
                                    {coverPreviewUrl && (
                                        <img
                                            src={coverPreviewUrl}
                                            alt={t('rescanCover')}
                                            className="max-h-80 max-w-[220px] rounded-md border border-border"
                                        />
                                    )}
                                </div>
                            )}

                            {preview.diff.length > 0 && (
                                <div className="mt-4 rounded-md border border-border bg-card p-3">
                                    <p className="mb-2 text-sm font-semibold">
                                        {t('rescanSelectFields')}
                                    </p>

                                    <div className="grid gap-1 md:grid-cols-2">
                                        {SELECTABLE_FIELDS.filter((entry) =>
                                            preview.diff.includes(entry.field),
                                        ).map((entry) => (
                                            <label
                                                key={entry.field}
                                                className="flex cursor-pointer items-center gap-2 text-sm"
                                            >
                                                <input
                                                    type="checkbox"
                                                    checked={fieldSelection[entry.selection]}
                                                    onChange={() => toggleField(entry.selection)}
                                                    className="size-4 accent-primary"
                                                />
                                                {t(entry.labelKey)}
                                            </label>
                                        ))}
                                    </div>

                                    <div className="mt-3 flex flex-wrap items-center gap-2">
                                        <Button
                                            variant="outline"
                                            size="sm"
                                            onClick={selectAllFields}
                                        >
                                            {t('selectAll')}
                                        </Button>
                                        <Button
                                            variant="outline"
                                            size="sm"
                                            onClick={deselectAllFields}
                                        >
                                            {t('deselectAll')}
                                        </Button>
                                        <span aria-hidden="true" className="h-5 w-px bg-border" />
                                        <span className="text-sm text-muted-foreground">
                                            {t('rescanUpdateSummary', {
                                                count: counts.selected,
                                                total: counts.total,
                                            })}
                                        </span>
                                    </div>
                                </div>
                            )}

                            <dl className="mt-4 grid grid-cols-[auto_minmax(0,1fr)] gap-x-2 gap-y-1 rounded-md bg-muted px-3 py-2 text-xs text-muted-foreground">
                                <dt className="font-semibold">{t('bookId')}:</dt>
                                <dd className="min-w-0 truncate">{preview.book_id}</dd>
                                <dt className="font-semibold">{t('rescanPendingId')}:</dt>
                                <dd className="min-w-0 truncate">{preview.pending_rescan_id}</dd>
                            </dl>
                        </div>
                    )}
                </div>

                <DialogFooter className="border-t border-border px-6 py-4">
                    <Button variant="ghost" onClick={handleClose} disabled={loading}>
                        {t('cancel')}
                    </Button>
                    <Button
                        variant="outline"
                        onClick={handleReject}
                        disabled={loading || !preview}
                        className="border-destructive/50 text-destructive hover:bg-destructive/10 hover:text-destructive"
                    >
                        {loading ? (
                            <BouncingDots />
                        ) : (
                            <XCircle aria-hidden="true" className="size-4" />
                        )}
                        {t('rescanReject')}
                    </Button>
                    {/* Approving is the affirmative act, so it is the solid button. */}
                    <Button onClick={handleApprove} disabled={loading || !preview}>
                        {loading ? (
                            <BouncingDots />
                        ) : (
                            <CheckCircle2 aria-hidden="true" className="size-4" />
                        )}
                        {t('rescanApprove')}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
};

export default RescanPreviewDialog;
