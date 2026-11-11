import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { AlertCircle, RefreshCw, RotateCcw, Trash2 } from 'lucide-react';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Badge } from '@/shared/ui/badge';
import { Button } from '@/shared/ui/button';
import { Input } from '@/shared/ui/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/shared/ui/table';
import { cn } from '@/shared/lib/utils';
import Fold from '../Fold';
import { newestArchive, type Pipeline, type StageKey } from '../pipeline';
import ProblemStrip from '../ProblemStrip';
import { groupScanErrors, splitScanErrors } from '../scanErrors';
import {
    formatCount,
    formatDay,
    ProgressRow,
    StagePanel,
    WARNING_BUTTON,
    WARNING_TEXT,
} from '../scanUi';
import type { BookScanState, ScanErrorItem, ScannedArchiveInfo } from '../useBookScan';

/*
 * «Архивы»: the archive scan. On top what needs a hand — the scan's message
 * and error, the running scan, the newest archive's unread files by class with
 * their downloads and a rescan — then the new archives when there are any,
 * every scanned archive in a fold with a name filter, and the rare repair
 * tools folded under «Инструменты» (DECISION 3), open while a fix runs.
 *
 * The tables, cards and per-archive actions are the old tab's, moved.
 */

interface ArchivesPanelProps {
    scan: BookScanState;
    pipeline: Pipeline;
    onOpen: (stage: StageKey) => void;
    /** Wide enough for the five-column tables; below, each archive is a card. */
    wide: boolean;
}

/** One unread file: its name, the error, its download. */
const ErrorRow: React.FC<{ item: ScanErrorItem; onDownload: (item: ScanErrorItem) => void }> = ({
    item,
    onDownload,
}) => {
    const { t } = useTranslation();
    return (
        <li className="flex min-w-0 items-center gap-2.5 text-sm">
            <span className="min-w-0 flex-1 break-all sm:flex-none">{item.file_name}</span>
            <span className="hidden min-w-0 flex-1 truncate text-muted-foreground sm:inline">
                {item.error}
            </span>
            <Button
                variant="ghost"
                size="sm"
                className="h-10 sm:h-7"
                aria-label={`${t('scanPanel.arch.download')}: ${item.file_name}`}
                onClick={() => onDownload(item)}
            >
                {t('scanPanel.arch.download')}
            </Button>
        </li>
    );
};

/** Read errors grouped by class, one fold each, the largest open. */
export const ErrorFolds: React.FC<{
    errors: ScanErrorItem[];
    onDownload: (item: ScanErrorItem) => void;
    openFirst: boolean;
}> = ({ errors, onDownload, openFirst }) => {
    const { t } = useTranslation();
    return (
        <>
            {groupScanErrors(errors).map(({ cls, items }, index) => (
                <Fold
                    key={cls}
                    label={t(`scanPanel.errorClass.${cls}`)}
                    meta={t('scanPanel.arch.files', { count: items.length })}
                    defaultOpen={openFirst && index === 0}
                >
                    <ul className="flex flex-col gap-1">
                        {items.map((item, i) => (
                            <ErrorRow
                                key={`${item.archive_name}/${item.file_name}/${i}`}
                                item={item}
                                onDownload={onDownload}
                            />
                        ))}
                    </ul>
                </Fold>
            ))}
        </>
    );
};

const ArchivesPanel: React.FC<ArchivesPanelProps> = ({ scan, pipeline, onOpen, wide }) => {
    const { t } = useTranslation();
    const [filter, setFilter] = useState('');
    const {
        status,
        progressPercent,
        unscannedArchives,
        scannedArchives,
        scannedTotalCount,
        scanErrors,
        isLoadingScanned,
        statusMessage,
        scanError,
        lastBookTitle,
        fixScanStatus,
        isFixScanning,
        formatScannedDate,
        handleStartScan,
        handleScanArchive,
        handleRescanClick,
        handleResetArchive,
        handleDeleteArchiveBooks,
        handleDownloadErrorFile,
        handleStartFixScan,
        handleCancelFixScan,
    } = scan;
    const node = pipeline.nodes.arch;
    const newest = newestArchive(scannedArchives);
    const { fresh } = splitScanErrors(scanErrors, newest?.name ?? null);
    const unread = newest !== null && newest.errors_count > 0;
    const needle = filter.trim().toLocaleLowerCase();
    const shownArchives =
        needle === ''
            ? scannedArchives
            : scannedArchives.filter((a) => a.name.toLocaleLowerCase().includes(needle));
    const busy = status?.is_running || isFixScanning;

    /** The three per-archive actions, shared by the card and the table row. */
    const archiveActions = (archive: ScannedArchiveInfo) => (
        <div className="flex gap-2">
            <Button
                size="icon"
                onClick={() => handleRescanClick(archive.name)}
                title={t('refresh')}
                aria-label={`${t('refresh')}: ${archive.name}`}
            >
                <RefreshCw className="size-4" />
            </Button>
            <Button
                variant="outline"
                size="icon"
                className={WARNING_BUTTON}
                onClick={() => handleResetArchive(archive.name)}
                title={t('bookScanResetTitle')}
                aria-label={`${t('bookScanResetTitle')}: ${archive.name}`}
            >
                <RotateCcw className="size-4" />
            </Button>
            <Button
                variant="destructive"
                size="icon"
                onClick={() => handleDeleteArchiveBooks(archive.name)}
                title={t('bookScanDeleteBooks')}
                aria-label={`${t('bookScanDeleteBooks')}: ${archive.name}`}
            >
                <Trash2 className="size-4" />
            </Button>
        </div>
    );

    return (
        <StagePanel
            stage="arch"
            state={node.state}
            badge={
                unread ? (
                    <Badge variant="outline" className={cn('tabular-nums', WARNING_TEXT)}>
                        {t('scanPanel.arch.errorsBadge', { count: newest.errors_count })}
                    </Badge>
                ) : (
                    <Badge variant="secondary" className="tabular-nums">
                        {formatCount(scannedTotalCount || scannedArchives.length)}
                    </Badge>
                )
            }
            aside={
                <Button variant="outline" disabled={busy} onClick={handleStartScan}>
                    {t('scanPanel.scanNew')}
                </Button>
            }
            strips={<ProblemStrip pipeline={pipeline} current="arch" onOpen={onOpen} />}
        >
            {statusMessage && <p className="text-sm text-muted-foreground">{statusMessage}</p>}
            {scanError && (
                <Alert variant="destructive">
                    <AlertCircle className="size-4" />
                    <AlertDescription>{scanError}</AlertDescription>
                </Alert>
            )}

            {status?.is_running ? (
                <div className="flex flex-col gap-2">
                    <h4 className="text-sm font-medium">{t('scanPanel.arch.running')}</h4>
                    <p className="text-sm tabular-nums">
                        {t('bookScanArchivesProcessed', {
                            processed: status.archives_processed,
                            total: status.total_archives,
                        })}
                    </p>
                    <p className="text-sm tabular-nums">
                        {t('bookScanBooksProcessed', { count: status.total_books })}
                    </p>
                    <p className="text-sm tabular-nums">
                        {t('bookScanErrors', { count: status.total_errors })}
                    </p>
                    {status.current_archive && (
                        <p className="text-sm break-words">
                            {t('bookScanCurrentArchive')}: {status.current_archive}
                        </p>
                    )}
                    {lastBookTitle && (
                        <p className="text-sm break-words">
                            {t('bookScanLastBook')}: {lastBookTitle}
                        </p>
                    )}
                    <ProgressRow label={t('bookScanProgress')} percent={progressPercent} />
                </div>
            ) : (
                <p className="text-sm break-words tabular-nums">
                    {newest === null
                        ? t('scanPanel.arch.noScans')
                        : t('scanPanel.arch.lastScan', {
                              date: formatDay(newest.scanned_at),
                              archive: newest.name,
                              books: formatCount(newest.books_count),
                          })}
                </p>
            )}

            {unread && (
                <div className="flex flex-col gap-2">
                    <div className="flex flex-col gap-1 rounded-[10px] border border-amber-500/45 bg-amber-500/10 px-3.5 py-3">
                        <p className="text-sm break-words tabular-nums">
                            <span className="font-semibold">
                                {t('scanPanel.arch.unreadTitle', {
                                    count: newest.errors_count,
                                    archive: newest.name,
                                })}
                            </span>
                            <span className="text-muted-foreground">
                                {' · '}
                                {formatDay(newest.scanned_at)}
                            </span>
                        </p>
                        <p className="text-sm text-muted-foreground">
                            {t('scanPanel.arch.unreadHint')}
                        </p>
                    </div>
                    <ErrorFolds errors={fresh} onDownload={handleDownloadErrorFile} openFirst />
                    <div>
                        <Button variant="outline" onClick={() => handleRescanClick(newest.name)}>
                            {t('scanPanel.arch.rescan')}
                        </Button>
                    </div>
                </div>
            )}

            {unscannedArchives.length > 0 && (
                <div className="flex flex-col gap-3">
                    <h4 className="text-sm font-medium tabular-nums">
                        {t('scanPanel.arch.newArchives', { count: unscannedArchives.length })}
                    </h4>
                    {!wide ? (
                        <div className="flex flex-col gap-3">
                            {unscannedArchives.map((archive) => (
                                <div
                                    key={archive.name}
                                    className="flex flex-col gap-2 rounded-lg border border-border p-3"
                                >
                                    <p className="text-sm font-semibold break-words">
                                        {archive.name}
                                    </p>
                                    <div className="flex flex-col gap-0.5 text-xs text-muted-foreground">
                                        <span>
                                            {t('bookScanSize')}:{' '}
                                            <strong className="tabular-nums">
                                                {archive.size_mb} MB
                                            </strong>
                                        </span>
                                        <span>
                                            {t('bookScanFileCount')}:{' '}
                                            <strong className="tabular-nums">
                                                {archive.file_count}
                                            </strong>
                                        </span>
                                        <span>
                                            {t('bookScanCreated')}:{' '}
                                            <strong>
                                                {new Date(archive.created_date).toLocaleString()}
                                            </strong>
                                        </span>
                                    </div>
                                    <Button
                                        variant="outline"
                                        className="w-full"
                                        onClick={() => handleScanArchive(archive.name)}
                                    >
                                        {t('bookScanArchiveButton')}
                                    </Button>
                                </div>
                            ))}
                        </div>
                    ) : (
                        <div className="rounded border border-border">
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead>{t('bookScanArchive')}</TableHead>
                                        <TableHead className="text-right">
                                            {t('bookScanSize')}
                                        </TableHead>
                                        <TableHead className="text-right">
                                            {t('bookScanFileCount')}
                                        </TableHead>
                                        <TableHead>{t('bookScanCreated')}</TableHead>
                                        <TableHead>
                                            <span className="sr-only">{t('actions')}</span>
                                        </TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {unscannedArchives.map((archive) => (
                                        <TableRow key={archive.name}>
                                            <TableCell className="break-all">
                                                {archive.name}
                                            </TableCell>
                                            <TableCell className="text-right tabular-nums whitespace-nowrap">
                                                {archive.size_mb} MB
                                            </TableCell>
                                            <TableCell className="text-right tabular-nums">
                                                {archive.file_count}
                                            </TableCell>
                                            <TableCell className="whitespace-nowrap tabular-nums">
                                                {new Date(archive.created_date).toLocaleString()}
                                            </TableCell>
                                            <TableCell>
                                                <Button
                                                    variant="outline"
                                                    size="sm"
                                                    onClick={() => handleScanArchive(archive.name)}
                                                >
                                                    {t('bookScanArchiveButton')}
                                                </Button>
                                            </TableCell>
                                        </TableRow>
                                    ))}
                                </TableBody>
                            </Table>
                        </div>
                    )}
                </div>
            )}

            <div className="flex flex-col gap-1.5">
                <Fold
                    label={t('scanPanel.arch.allArchives')}
                    meta={formatCount(scannedTotalCount || scannedArchives.length)}
                >
                    <Input
                        type="search"
                        aria-label={t('scanPanel.arch.filter')}
                        placeholder={t('scanPanel.arch.filter')}
                        className="sm:max-w-80"
                        value={filter}
                        onChange={(e) => setFilter(e.target.value)}
                    />
                    {isLoadingScanned && scannedArchives.length === 0 ? (
                        <p className="text-sm text-muted-foreground">{t('loading')}</p>
                    ) : shownArchives.length === 0 ? (
                        <p className="text-sm text-muted-foreground">{t('bookScanNoScanned')}</p>
                    ) : !wide ? (
                        <div className="flex flex-col gap-3">
                            {shownArchives.map((archive) => (
                                <div
                                    key={archive.name}
                                    className="flex flex-col gap-2 rounded-lg border border-border p-3"
                                >
                                    <p className="text-sm font-semibold break-words">
                                        {archive.name}
                                    </p>
                                    <div className="flex flex-col gap-1 text-xs text-muted-foreground">
                                        <span className="flex items-center gap-2">
                                            {t('bookScanBooksCount')}:
                                            <Badge className="tabular-nums">
                                                {archive.books_count}
                                            </Badge>
                                        </span>
                                        <span className="flex items-center gap-2">
                                            {t('bookScanErrorsCount')}:
                                            <Badge
                                                variant={
                                                    archive.errors_count > 0
                                                        ? 'destructive'
                                                        : 'default'
                                                }
                                                className="tabular-nums"
                                            >
                                                {archive.errors_count}
                                            </Badge>
                                        </span>
                                        <span>
                                            {t('bookScanScannedAt')}:{' '}
                                            <strong>{formatScannedDate(archive.scanned_at)}</strong>
                                        </span>
                                    </div>
                                    {archiveActions(archive)}
                                </div>
                            ))}
                        </div>
                    ) : (
                        <div className="rounded border border-border">
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead>{t('bookScanArchive')}</TableHead>
                                        <TableHead className="text-right">
                                            {t('bookScanBooksCount')}
                                        </TableHead>
                                        <TableHead className="text-right">
                                            {t('bookScanErrorsCount')}
                                        </TableHead>
                                        <TableHead>{t('bookScanScannedAt')}</TableHead>
                                        <TableHead>
                                            <span className="sr-only">{t('actions')}</span>
                                        </TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {shownArchives.map((archive) => (
                                        <TableRow key={archive.name}>
                                            <TableCell className="break-all">
                                                {archive.name}
                                            </TableCell>
                                            <TableCell className="text-right">
                                                <Badge className="tabular-nums">
                                                    {archive.books_count}
                                                </Badge>
                                            </TableCell>
                                            <TableCell className="text-right">
                                                <Badge
                                                    variant={
                                                        archive.errors_count > 0
                                                            ? 'destructive'
                                                            : 'default'
                                                    }
                                                    className="tabular-nums"
                                                >
                                                    {archive.errors_count}
                                                </Badge>
                                            </TableCell>
                                            <TableCell className="whitespace-nowrap tabular-nums">
                                                {formatScannedDate(archive.scanned_at)}
                                            </TableCell>
                                            <TableCell>{archiveActions(archive)}</TableCell>
                                        </TableRow>
                                    ))}
                                </TableBody>
                            </Table>
                        </div>
                    )}
                </Fold>

                <Fold label={t('scanPanel.arch.tools')} defaultOpen={isFixScanning}>
                    <p className="text-sm text-muted-foreground">{t('scanPanel.arch.toolsHint')}</p>
                    <div>
                        <Button variant="secondary" onClick={handleStartFixScan} disabled={busy}>
                            {t('fixScanStart')}
                        </Button>
                    </div>
                    {fixScanStatus && (
                        <div className="flex flex-col gap-2">
                            <h4 className="text-sm font-medium">{t('fixScanStatusTitle')}</h4>
                            <p className="text-sm tabular-nums">
                                {t('fixScanBooksProcessed', {
                                    processed: fixScanStatus.books_processed,
                                    total: fixScanStatus.total_books,
                                })}
                            </p>
                            <p className="text-sm tabular-nums">
                                {t('fixScanBooksUpdated', { count: fixScanStatus.books_updated })}
                            </p>
                            <p className="text-sm tabular-nums">
                                {t('fixScanErrors', { count: fixScanStatus.error_count })}
                            </p>
                            {fixScanStatus.current_archive && (
                                <p className="text-sm break-words">
                                    {t('bookScanCurrentArchive')}: {fixScanStatus.current_archive}
                                </p>
                            )}
                            <ProgressRow
                                label={t('bookScanProgress')}
                                percent={fixScanStatus.progress_percent}
                            />
                            {isFixScanning && (
                                <Button
                                    variant="outline"
                                    className="mt-2 self-start text-destructive hover:text-destructive"
                                    onClick={handleCancelFixScan}
                                >
                                    {t('cancel')}
                                </Button>
                            )}
                        </div>
                    )}
                </Fold>
            </div>
        </StagePanel>
    );
};

export default ArchivesPanel;
