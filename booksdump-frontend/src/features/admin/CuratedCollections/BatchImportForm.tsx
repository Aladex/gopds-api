import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { AlertTriangle, Trash2, Upload } from 'lucide-react';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Badge } from '@/shared/ui/badge';
import { Button, buttonVariants } from '@/shared/ui/button';
import { Card, CardContent } from '@/shared/ui/card';
import { Progress } from '@/shared/ui/progress';
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from '@/shared/ui/table';
import { cn } from '@/shared/lib/utils';

import { importCuratedCollection } from '@/features/admin/CuratedCollections/api';
import { parseCsv, ParsedItem } from '@/features/admin/CuratedCollections/csvParser';

type Status = 'pending' | 'importing' | 'done' | 'error';

interface FileEntry {
    file: File;
    name: string;
    items: ParsedItem[];
    errors: string[];
    status: Status;
    statusMsg?: string;
}

const stripCsvExt = (name: string) => name.replace(/\.csv$/i, '').replace(/[_-]+/g, ' ').trim();

/** The badge says which of the four states a file is in, by colour and word. */
const STATUS_VARIANT: Record<Status, 'default' | 'secondary' | 'destructive' | 'outline'> = {
    pending: 'secondary',
    importing: 'outline',
    done: 'default',
    error: 'destructive',
};

// BatchImportForm lets the admin drop a stack of *.csv files; each file becomes
// one curated collection, with the collection name auto-derived from the file
// name (sans extension, with -/_ replaced by spaces). Files import sequentially
// so a single failure does not abort the rest, and per-file status is shown.
const BatchImportForm: React.FC<{ onCreated: () => void }> = ({ onCreated }) => {
    const { t } = useTranslation();
    const [entries, setEntries] = useState<FileEntry[]>([]);
    const [running, setRunning] = useState(false);
    const [dragging, setDragging] = useState(false);

    const addFiles = async (files: FileList | File[]) => {
        const next: FileEntry[] = [];
        for (const f of Array.from(files)) {
            const text = await f.text();
            const { items, errors } = parseCsv(text);
            next.push({
                file: f,
                name: stripCsvExt(f.name),
                items,
                errors,
                status: 'pending',
            });
        }
        setEntries((prev) => [...prev, ...next]);
    };

    const removeAt = (idx: number) => setEntries((prev) => prev.filter((_, i) => i !== idx));

    const importAll = async () => {
        if (running || entries.length === 0) return;
        setRunning(true);
        try {
            for (let i = 0; i < entries.length; i++) {
                const e = entries[i];
                if (e.status === 'done' || e.items.length === 0 || !e.name.trim()) {
                    continue;
                }
                setEntries((prev) =>
                    prev.map((it, idx) => (idx === i ? { ...it, status: 'importing' } : it)),
                );
                try {
                    await importCuratedCollection(e.name.trim(), '', e.items);
                    setEntries((prev) =>
                        prev.map((it, idx) => (idx === i ? { ...it, status: 'done' } : it)),
                    );
                } catch (err) {
                    const msg = err instanceof Error ? err.message : 'failed';
                    setEntries((prev) =>
                        prev.map((it, idx) =>
                            idx === i ? { ...it, status: 'error', statusMsg: msg } : it,
                        ),
                    );
                }
            }
            onCreated();
        } finally {
            setRunning(false);
        }
    };

    const pendingCount = entries.filter((e) => e.status === 'pending' && e.items.length > 0).length;
    const totalItems = entries.reduce(
        (acc, e) => acc + (e.status === 'pending' ? e.items.length : 0),
        0,
    );

    return (
        <Card>
            <CardContent className="flex flex-col gap-4">
                <div className="flex flex-col gap-1">
                    <h2 className="text-base font-medium">
                        {t('curatedCollections.batchTitle', 'Batch import')}
                    </h2>
                    <p className="text-sm text-muted-foreground">
                        {t(
                            'curatedCollections.batchHint',
                            'Drop one or more CSV files. Each file becomes one collection — name is taken from the file name.',
                        )}
                    </p>
                </div>

                {/* The drop target lights up while something is over it, which is
                    the only confirmation a drag gets before it is released. */}
                <div
                    onDragOver={(event) => {
                        event.preventDefault();
                        setDragging(true);
                    }}
                    onDragLeave={() => setDragging(false)}
                    onDrop={(event) => {
                        event.preventDefault();
                        setDragging(false);
                        if (event.dataTransfer.files.length > 0) addFiles(event.dataTransfer.files);
                    }}
                    className={cn(
                        'flex flex-col items-center gap-2 rounded-lg border-2 border-dashed border-border p-6 text-center transition-colors',
                        dragging && 'border-primary bg-accent',
                    )}
                >
                    <p className="text-sm text-muted-foreground">
                        {t('curatedCollections.dropHere', 'Drop CSV files here')}
                    </p>
                    <label className={cn(buttonVariants({ variant: 'outline', size: 'sm' }), 'cursor-pointer')}>
                        <Upload className="size-4" />
                        {t('curatedCollections.selectFiles', 'Select files')}
                        <input
                            type="file"
                            accept=".csv,text/csv"
                            multiple
                            className="sr-only"
                            onChange={(event) => {
                                if (event.target.files) addFiles(event.target.files);
                                // Cleared so choosing the same file again still
                                // fires a change event.
                                event.target.value = '';
                            }}
                        />
                    </label>
                </div>

                {entries.length > 0 && (
                    <div className="rounded border border-border">
                        <Table>
                            <TableHeader>
                                <TableRow>
                                    <TableHead>{t('curatedCollections.name', 'Name')}</TableHead>
                                    <TableHead className="text-right">
                                        {t('curatedCollections.batchItems', 'Items')}
                                    </TableHead>
                                    <TableHead className="text-right">
                                        {t('curatedCollections.batchErrors', 'Parse errors')}
                                    </TableHead>
                                    <TableHead>{t('curatedCollections.status', 'Status')}</TableHead>
                                    <TableHead>
                                        <span className="sr-only">
                                            {t('curatedCollections.delete', 'Delete')}
                                        </span>
                                    </TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {entries.map((entry, idx) => (
                                    <TableRow key={idx}>
                                        <TableCell>{entry.name}</TableCell>
                                        <TableCell className="text-right tabular-nums">
                                            {entry.items.length}
                                        </TableCell>
                                        <TableCell
                                            className={cn(
                                                'text-right tabular-nums',
                                                entry.errors.length > 0 && 'text-destructive',
                                            )}
                                        >
                                            {entry.errors.length}
                                        </TableCell>
                                        <TableCell>
                                            <div className="flex flex-col items-start gap-1">
                                                <Badge variant={STATUS_VARIANT[entry.status]}>
                                                    {entry.status}
                                                </Badge>
                                                {entry.statusMsg && (
                                                    <span className="text-xs text-destructive">
                                                        {entry.statusMsg}
                                                    </span>
                                                )}
                                            </div>
                                        </TableCell>
                                        <TableCell className="w-12">
                                            <Button
                                                variant="ghost"
                                                size="icon-sm"
                                                disabled={running || entry.status === 'importing'}
                                                onClick={() => removeAt(idx)}
                                                title={t('curatedCollections.delete', 'Delete')}
                                                aria-label={`${t('curatedCollections.delete', 'Delete')}: ${entry.name}`}
                                            >
                                                <Trash2 className="size-4" />
                                            </Button>
                                        </TableCell>
                                    </TableRow>
                                ))}
                            </TableBody>
                        </Table>
                    </div>
                )}

                {/* No value: the files import one after another and the bar says
                    only that something is happening. */}
                {running && <Progress aria-label={t('curatedCollections.batchImporting', 'Importing…')} />}

                {entries.some((e) => e.errors.length > 0) && (
                    <Alert>
                        <AlertTriangle className="size-4" />
                        <AlertDescription>
                            {t(
                                'curatedCollections.batchHasParseErrors',
                                'Some files have parse warnings — open them individually to inspect.',
                            )}
                        </AlertDescription>
                    </Alert>
                )}

                <div className="flex flex-wrap gap-2">
                    <Button disabled={running || pendingCount === 0} onClick={importAll}>
                        {running
                            ? t('curatedCollections.batchImporting', 'Importing…')
                            : t(
                                  'curatedCollections.batchImport',
                                  'Import {{count}} collections ({{items}} items)',
                                  { count: pendingCount, items: totalItems },
                              )}
                    </Button>
                    {entries.length > 0 && (
                        <Button variant="ghost" onClick={() => setEntries([])} disabled={running}>
                            {t('curatedCollections.batchClear', 'Clear')}
                        </Button>
                    )}
                </div>
            </CardContent>
        </Card>
    );
};

export default BatchImportForm;
