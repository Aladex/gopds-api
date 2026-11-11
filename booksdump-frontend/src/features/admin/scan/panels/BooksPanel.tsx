import React from 'react';
import { useTranslation } from 'react-i18next';

import { Badge } from '@/shared/ui/badge';
import AuthorMetadataCard from '@/features/admin/AuthorMetadataCard';
import Fold from '../Fold';
import { isUsableRun, runErrors } from '../metadataRun';
import { newestArchive, type Pipeline, type StageKey } from '../pipeline';
import ProblemStrip from '../ProblemStrip';
import { splitScanErrors } from '../scanErrors';
import { useScanSource } from '../ScanSources';
import { formatCount, ProgressRow, StagePanel } from '../scanUi';
import type { BookScanState } from '../useBookScan';
import { ErrorFolds } from './ArchivesPanel';

/*
 * «Книги»: the books' author metadata. One summary line, the fix scan's
 * progress while it runs, the books that never read (the read errors of every
 * archive but the newest, which «Архивы» shows), then the metadata card's
 * panel: problem archives and a paused volume on top, the catalogue pass of
 * the migration folded.
 */

interface BooksPanelProps {
    scan: BookScanState;
    pipeline: Pipeline;
    onOpen: (stage: StageKey) => void;
}

const BooksPanel: React.FC<BooksPanelProps> = ({ scan, pipeline, onOpen }) => {
    const { t } = useTranslation();
    const source = useScanSource('metadataRun');
    const raw = source.data?.run ?? null;
    const run = raw !== null && isUsableRun(raw) ? raw : null;
    const node = pipeline.nodes.books;
    const newest = newestArchive(scan.scannedArchives);
    const { old } = splitScanErrors(scan.scanErrors, newest?.name ?? null);
    const unread = run === null ? 0 : runErrors(run);

    return (
        <StagePanel
            stage="books"
            state={node.state}
            badge={
                node.figure === null ? undefined : (
                    <Badge variant="secondary" className="tabular-nums">
                        {formatCount(node.figure)}
                    </Badge>
                )
            }
            strips={<ProblemStrip pipeline={pipeline} current="books" onOpen={onOpen} />}
        >
            {run !== null && (
                <p className="text-sm tabular-nums">
                    {unread === 0
                        ? t('scanPanel.books.summaryClean', {
                              done: formatCount(run.stages.extraction.done),
                          })
                        : t('scanPanel.books.summary', {
                              count: unread,
                              done: formatCount(run.stages.extraction.done),
                          })}
                </p>
            )}
            {scan.isFixScanning && scan.fixScanStatus !== null && (
                <ProgressRow
                    label={t('fixScanStatusTitle')}
                    percent={scan.fixScanStatus.progress_percent}
                />
            )}
            <div className="flex flex-col gap-1.5">
                <Fold
                    label={t('scanPanel.books.unread')}
                    meta={t('scanPanel.books.unreadMeta', { count: unread })}
                >
                    {old.length === 0 ? (
                        <p className="text-sm text-muted-foreground">
                            {t('scanPanel.books.noOldErrors')}
                        </p>
                    ) : (
                        <ErrorFolds
                            errors={old}
                            onDownload={scan.handleDownloadErrorFile}
                            openFirst={false}
                        />
                    )}
                </Fold>
                <AuthorMetadataCard variant="panel" />
            </div>
        </StagePanel>
    );
};

export default BooksPanel;
