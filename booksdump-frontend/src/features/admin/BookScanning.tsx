import React, { useMemo } from 'react';
import { useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';

import { Button } from '@/shared/ui/button';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/shared/ui/dialog';
import { Progress } from '@/shared/ui/progress';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { ADMIN_TABLE_WIDE_QUERY } from '@/shared/layout/breakpoints';
import { cn } from '@/shared/lib/utils';
import PipelineCarousel from '@/features/admin/scan/PipelineCarousel';
import PipelineGraph from '@/features/admin/scan/PipelineGraph';
import {
    derivePipeline,
    stageFromParams,
    type Pipeline,
    type StageKey,
} from '@/features/admin/scan/pipeline';
import ArchivesPanel from '@/features/admin/scan/panels/ArchivesPanel';
import BooksPanel from '@/features/admin/scan/panels/BooksPanel';
import EntitiesPanel from '@/features/admin/scan/panels/EntitiesPanel';
import ModelPanel from '@/features/admin/scan/panels/ModelPanel';
import NamesPanel from '@/features/admin/scan/panels/NamesPanel';
import ReviewPanel from '@/features/admin/scan/panels/ReviewPanel';
import { ScanSourcesProvider, useScanSource } from '@/features/admin/scan/ScanSources';
import { ProgressRow } from '@/features/admin/scan/scanUi';
import { useBookScan, type BookScanState } from '@/features/admin/scan/useBookScan';

/*
 * The scanning section as a pipeline: archives → books → names → model →
 * entities, with the human review branching off. The chain says each stage's
 * state and why; under it is the selected stage's panel, and only that one is
 * mounted, so a stage nobody looks at reads nothing of its own.
 *
 * The selected stage lives in the address (?stage=…). A page without one
 * follows the derived default — the first problem, or the review queue when
 * all is well — once every source has answered, so a poll that brings a new
 * problem moves only a page nobody chose a stage on. The old ?tab=authors
 * (the review detail's Back) and the archive tabs still land where they did.
 */

/** Whether a source has answered once, with data or with a failure. */
const answered = (source: { data: unknown; failed: boolean }) =>
    source.data !== null || source.failed;

interface StagePanelSwitchProps {
    stage: StageKey;
    scan: BookScanState;
    pipeline: Pipeline;
    onOpen: (stage: StageKey) => void;
    wide: boolean;
}

/** The selected stage's panel, and no other. */
const SelectedPanel: React.FC<StagePanelSwitchProps> = ({
    stage,
    scan,
    pipeline,
    onOpen,
    wide,
}) => {
    switch (stage) {
        case 'arch':
            return <ArchivesPanel scan={scan} pipeline={pipeline} onOpen={onOpen} wide={wide} />;
        case 'books':
            return <BooksPanel scan={scan} pipeline={pipeline} onOpen={onOpen} />;
        case 'names':
            return <NamesPanel pipeline={pipeline} onOpen={onOpen} />;
        case 'model':
            return <ModelPanel pipeline={pipeline} onOpen={onOpen} />;
        case 'ent':
            return <EntitiesPanel pipeline={pipeline} onOpen={onOpen} />;
        case 'human':
            return <ReviewPanel pipeline={pipeline} onOpen={onOpen} />;
    }
};

const BookScanning: React.FC = () => {
    const { t } = useTranslation();
    const scan = useBookScan();
    // The graph from 900 px, where the queue also switches from its route to
    // its modal; below it the chain is a carousel.
    const wide = useMediaQuery(ADMIN_TABLE_WIDE_QUERY);
    const metadata = useScanSource('metadataRun');
    const llmNames = useScanSource('llmNames');
    const llmMerge = useScanSource('llmMerge');
    const entity = useScanSource('entity');
    const merges = useScanSource('merges');
    const reviewCounts = useScanSource('reviewCounts');
    const [searchParams, setSearchParams] = useSearchParams();

    const fixPercent = scan.fixScanStatus?.progress_percent ?? 0;
    const pipeline = useMemo(
        () =>
            derivePipeline(
                {
                    scanned: scan.scannedLoaded ? scan.scannedArchives : null,
                    unscannedCount: scan.unscannedLoaded ? scan.unscannedArchives.length : null,
                    scanStatus: scan.status,
                    fixScan: scan.isFixScanning ? { running: true, percent: fixPercent } : null,
                    metadataRun: metadata.data?.run ?? null,
                    llmNames: llmNames.data,
                    llmMerge: llmMerge.data,
                    entity: entity.data,
                    mergesUnanchored: merges.data?.unanchored ?? null,
                    reviewCounts: reviewCounts.data,
                },
                new Date(),
            ),
        [
            scan.scannedLoaded,
            scan.scannedArchives,
            scan.unscannedLoaded,
            scan.unscannedArchives,
            scan.status,
            scan.isFixScanning,
            fixPercent,
            metadata.data,
            llmNames.data,
            llmMerge.data,
            entity.data,
            merges.data,
            reviewCounts.data,
        ],
    );

    const settled =
        scan.scannedLoaded &&
        answered(metadata) &&
        answered(llmNames) &&
        answered(llmMerge) &&
        answered(entity) &&
        answered(merges);
    const chosen = stageFromParams(searchParams);
    const selected = chosen ?? (settled ? pipeline.defaultStage : null);

    /**
     * Selects a stage: it goes into the address, and the old tab leaves it. A
     * carousel swipe replaces the entry, so swiping does not fill the history.
     */
    const select = (stage: StageKey, options?: { replace: boolean }) => {
        const next = new URLSearchParams(searchParams);
        next.delete('tab');
        next.set('stage', stage);
        setSearchParams(next, { replace: options?.replace ?? false });
    };

    return (
        <div className={cn('flex min-w-0 flex-col', wide ? 'gap-4' : 'gap-3')}>
            {wide ? (
                <PipelineGraph
                    pipeline={pipeline}
                    selected={selected}
                    onSelect={select}
                    onScan={scan.handleStartScan}
                    scanDisabled={Boolean(scan.status?.is_running || scan.isFixScanning)}
                />
            ) : (
                <PipelineCarousel pipeline={pipeline} selected={selected} onSelect={select} />
            )}

            {selected === null ? (
                <p role="status" className="px-1 text-sm text-muted-foreground">
                    {t('loading')}
                </p>
            ) : (
                <SelectedPanel
                    stage={selected}
                    scan={scan}
                    pipeline={pipeline}
                    onOpen={select}
                    wide={wide}
                />
            )}

            {/* Rescan Confirmation Dialog */}
            <Dialog
                open={scan.rescanDialogOpen}
                onOpenChange={(next) => {
                    // A rescan in flight owns the dialog: closing it would strand
                    // the progress it is the only view of.
                    if (!next && !scan.isRescanning) scan.handleRescanDialogClose();
                }}
            >
                <DialogContent closeLabel={t('close')} showCloseButton={!scan.isRescanning}>
                    <DialogHeader>
                        <DialogTitle>
                            {scan.isRescanning
                                ? t('rescanningArchive')
                                : `${t('refresh')} ${t('bookScanArchive')}`}
                        </DialogTitle>
                        <DialogDescription>
                            {scan.isRescanning
                                ? `${t('rescanning')}: ${scan.archiveToRescan ?? ''}`
                                : t('bookScanResetConfirm', { name: scan.archiveToRescan || '' })}
                        </DialogDescription>
                    </DialogHeader>

                    {scan.isRescanning &&
                        (scan.rescanProgress ? (
                            <div className="flex flex-col gap-3">
                                <ProgressRow
                                    label={t('rescanProgress')}
                                    percent={scan.rescanProgress.progress_percent}
                                />

                                {scan.rescanProgress.current_archive && (
                                    <p className="text-sm break-words text-muted-foreground">
                                        {t('rescanCurrent')}: {scan.rescanProgress.current_archive}
                                    </p>
                                )}

                                <div className="flex flex-wrap gap-6">
                                    <div className="flex flex-col">
                                        <span className="text-xs text-muted-foreground">
                                            {t('rescanBooksProcessed')}
                                        </span>
                                        <span className="text-lg font-medium text-primary tabular-nums">
                                            {scan.rescanProgress.total_books}
                                        </span>
                                    </div>
                                    <div className="flex flex-col">
                                        <span className="text-xs text-muted-foreground">
                                            {t('rescanErrors')}
                                        </span>
                                        <span
                                            className={cn(
                                                'text-lg font-medium tabular-nums',
                                                scan.rescanProgress.total_errors > 0
                                                    ? 'text-destructive'
                                                    : 'text-primary',
                                            )}
                                        >
                                            {scan.rescanProgress.total_errors}
                                        </span>
                                    </div>
                                    <div className="flex flex-col">
                                        <span className="text-xs text-muted-foreground">
                                            {t('rescanElapsedTime')}
                                        </span>
                                        <span className="text-lg font-medium tabular-nums">
                                            {scan.rescanProgress.elapsed_seconds}s
                                        </span>
                                    </div>
                                </div>
                            </div>
                        ) : (
                            <div role="status" className="flex flex-col gap-2">
                                <p className="text-sm text-muted-foreground">
                                    {t('rescanStarting')}
                                </p>
                                <Progress aria-label={t('rescanStarting')} />
                            </div>
                        ))}

                    <DialogFooter>
                        <Button
                            variant="ghost"
                            onClick={scan.handleRescanDialogClose}
                            disabled={scan.isRescanning}
                        >
                            {t('cancel')}
                        </Button>
                        {!scan.isRescanning && (
                            <Button onClick={scan.handleRescanConfirm}>{t('refresh')}</Button>
                        )}
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
};

/**
 * The tab under one set of data sources, so every block reading the same
 * status shares one request and one poll.
 */
const BookScanningTab: React.FC = () => (
    <ScanSourcesProvider>
        <BookScanning />
    </ScanSourcesProvider>
);

export default BookScanningTab;
