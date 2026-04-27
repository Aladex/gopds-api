import React, { useState } from 'react';
import {
    Alert,
    Box,
    Button,
    Card,
    CardContent,
    Chip,
    IconButton,
    LinearProgress,
    Stack,
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableRow,
    Typography,
} from '@mui/material';
import DeleteIcon from '@mui/icons-material/Delete';
import { useTranslation } from 'react-i18next';
import { importCuratedCollection } from './api';
import { parseCsv, ParsedItem } from './csvParser';

type Status = 'pending' | 'importing' | 'done' | 'error';

interface FileEntry {
    file: File;
    name: string;
    items: ParsedItem[];
    errors: string[];
    status: Status;
    statusMsg?: string;
}

const stripCsvExt = (name: string) =>
    name.replace(/\.csv$/i, '').replace(/[_-]+/g, ' ').trim();

// BatchImportForm lets the admin drop a stack of *.csv files; each file becomes
// one curated collection, with the collection name auto-derived from the file
// name (sans extension, with -/_ replaced by spaces). Files import sequentially
// so a single failure does not abort the rest, and per-file status is shown.
const BatchImportForm: React.FC<{ onCreated: () => void }> = ({ onCreated }) => {
    const { t } = useTranslation();
    const [entries, setEntries] = useState<FileEntry[]>([]);
    const [running, setRunning] = useState(false);

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

    const removeAt = (idx: number) =>
        setEntries((prev) => prev.filter((_, i) => i !== idx));

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
                } catch (err: any) {
                    const msg = err?.response?.data?.error ?? err?.message ?? 'failed';
                    setEntries((prev) =>
                        prev.map((it, idx) => (idx === i ? { ...it, status: 'error', statusMsg: msg } : it)),
                    );
                }
            }
            onCreated();
        } finally {
            setRunning(false);
        }
    };

    const pendingCount = entries.filter((e) => e.status === 'pending' && e.items.length > 0).length;
    const totalItems = entries.reduce((acc, e) => acc + (e.status === 'pending' ? e.items.length : 0), 0);

    return (
        <Card sx={{ mt: 2, boxShadow: 1 }}>
            <CardContent>
                <Typography variant="h6" gutterBottom>
                    {t('curatedCollections.batchTitle', 'Batch import')}
                </Typography>
                <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
                    {t(
                        'curatedCollections.batchHint',
                        'Drop one or more CSV files. Each file becomes one collection — name is taken from the file name.',
                    )}
                </Typography>

                <Box
                    onDragOver={(e) => e.preventDefault()}
                    onDrop={(e) => {
                        e.preventDefault();
                        if (e.dataTransfer.files.length > 0) addFiles(e.dataTransfer.files);
                    }}
                    sx={{
                        p: 3,
                        border: '2px dashed',
                        borderColor: 'divider',
                        borderRadius: 2,
                        textAlign: 'center',
                        bgcolor: 'background.default',
                    }}
                >
                    <Typography variant="body2" color="text.secondary" gutterBottom>
                        {t('curatedCollections.dropHere', 'Drop CSV files here')}
                    </Typography>
                    <Button variant="outlined" component="label" size="small">
                        {t('curatedCollections.selectFiles', 'Select files')}
                        <input
                            type="file"
                            accept=".csv,text/csv"
                            hidden
                            multiple
                            onChange={(e) => {
                                if (e.target.files) addFiles(e.target.files);
                                e.target.value = '';
                            }}
                        />
                    </Button>
                </Box>

                {entries.length > 0 && (
                    <Box sx={{ mt: 2, border: 1, borderColor: 'divider', borderRadius: 1 }}>
                        <Table size="small">
                            <TableHead>
                                <TableRow>
                                    <TableCell>{t('curatedCollections.name', 'Name')}</TableCell>
                                    <TableCell align="right">
                                        {t('curatedCollections.batchItems', 'Items')}
                                    </TableCell>
                                    <TableCell align="right">
                                        {t('curatedCollections.batchErrors', 'Parse errors')}
                                    </TableCell>
                                    <TableCell>{t('curatedCollections.status', 'Status')}</TableCell>
                                    <TableCell></TableCell>
                                </TableRow>
                            </TableHead>
                            <TableBody>
                                {entries.map((e, idx) => {
                                    const color =
                                        e.status === 'done'
                                            ? 'success'
                                            : e.status === 'error'
                                                ? 'error'
                                                : e.status === 'importing'
                                                    ? 'warning'
                                                    : 'default';
                                    return (
                                        <TableRow key={idx}>
                                            <TableCell>{e.name}</TableCell>
                                            <TableCell align="right">{e.items.length}</TableCell>
                                            <TableCell align="right">{e.errors.length}</TableCell>
                                            <TableCell>
                                                <Chip size="small" label={e.status} color={color as any} />
                                                {e.statusMsg && (
                                                    <Typography variant="caption" display="block" color="error.main">
                                                        {e.statusMsg}
                                                    </Typography>
                                                )}
                                            </TableCell>
                                            <TableCell>
                                                <IconButton
                                                    size="small"
                                                    disabled={running || e.status === 'importing'}
                                                    onClick={() => removeAt(idx)}
                                                >
                                                    <DeleteIcon fontSize="small" />
                                                </IconButton>
                                            </TableCell>
                                        </TableRow>
                                    );
                                })}
                            </TableBody>
                        </Table>
                    </Box>
                )}

                {running && <LinearProgress sx={{ mt: 2 }} />}

                {entries.some((e) => e.errors.length > 0) && (
                    <Alert severity="warning" sx={{ mt: 2 }}>
                        {t('curatedCollections.batchHasParseErrors', 'Some files have parse warnings — open them individually to inspect.')}
                    </Alert>
                )}

                <Stack direction="row" spacing={1} mt={2}>
                    <Button
                        variant="contained"
                        disabled={running || pendingCount === 0}
                        onClick={importAll}
                    >
                        {running
                            ? t('curatedCollections.batchImporting', 'Importing…')
                            : t(
                                  'curatedCollections.batchImport',
                                  'Import {{count}} collections ({{items}} items)',
                                  { count: pendingCount, items: totalItems },
                              )}
                    </Button>
                    {entries.length > 0 && (
                        <Button onClick={() => setEntries([])} disabled={running}>
                            {t('curatedCollections.batchClear', 'Clear')}
                        </Button>
                    )}
                </Stack>
            </CardContent>
        </Card>
    );
};

export default BatchImportForm;
