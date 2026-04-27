import React, { useState } from 'react';
import {
    Alert,
    Box,
    Button,
    Card,
    CardContent,
    Stack,
    Tab,
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableRow,
    Tabs,
    TextField,
    Typography,
} from '@mui/material';
import { useTranslation } from 'react-i18next';
import { importCuratedCollection } from './api';
import { parseCsv, parseTextarea, ParsedItem } from './csvParser';

type ImportTab = 'csv' | 'text';

const PreviewTable: React.FC<{ items: ParsedItem[] }> = ({ items }) => {
    if (items.length === 0) return null;
    return (
        <Box mt={2}>
            <Typography variant="subtitle2" gutterBottom>
                Preview ({items.length})
            </Typography>
            <Box sx={{ maxHeight: 240, overflow: 'auto', border: 1, borderColor: 'divider' }}>
                <Table size="small">
                    <TableHead>
                        <TableRow>
                            <TableCell>#</TableCell>
                            <TableCell>Title</TableCell>
                            <TableCell>Author</TableCell>
                            <TableCell>Year</TableCell>
                        </TableRow>
                    </TableHead>
                    <TableBody>
                        {items.slice(0, 200).map((it, i) => (
                            <TableRow key={i}>
                                <TableCell>{i + 1}</TableCell>
                                <TableCell>{it.title}</TableCell>
                                <TableCell>{it.author}</TableCell>
                                <TableCell>{it.year ?? ''}</TableCell>
                            </TableRow>
                        ))}
                    </TableBody>
                </Table>
            </Box>
            {items.length > 200 && (
                <Typography variant="caption" color="text.secondary">
                    Showing first 200 of {items.length}
                </Typography>
            )}
        </Box>
    );
};

const ImportForm: React.FC<{ onCreated: (id: number) => void }> = ({ onCreated }) => {
    const { t } = useTranslation();
    const [tab, setTab] = useState<ImportTab>('csv');
    const [name, setName] = useState('');
    const [sourceUrl, setSourceUrl] = useState('');
    const [csvText, setCsvText] = useState('');
    const [textareaText, setTextareaText] = useState('');
    const [items, setItems] = useState<ParsedItem[]>([]);
    const [errors, setErrors] = useState<string[]>([]);
    const [submitError, setSubmitError] = useState<string | null>(null);
    const [submitting, setSubmitting] = useState(false);

    const handleFile = (file: File) => {
        const reader = new FileReader();
        reader.onload = () => {
            const text = String(reader.result ?? '');
            setCsvText(text);
            const result = parseCsv(text);
            setItems(result.items);
            setErrors(result.errors);
        };
        reader.readAsText(file);
    };

    const onCsvTextChange = (text: string) => {
        setCsvText(text);
        if (text.trim() === '') {
            setItems([]);
            setErrors([]);
            return;
        }
        const result = parseCsv(text);
        setItems(result.items);
        setErrors(result.errors);
    };

    const onTextareaChange = (text: string) => {
        setTextareaText(text);
        if (text.trim() === '') {
            setItems([]);
            setErrors([]);
            return;
        }
        const result = parseTextarea(text);
        setItems(result.items);
        setErrors(result.errors);
    };

    const onTabChange = (_: any, newTab: ImportTab) => {
        setTab(newTab);
        setItems([]);
        setErrors([]);
    };

    const onSubmit = async () => {
        if (!name.trim() || items.length === 0) return;
        setSubmitError(null);
        setSubmitting(true);
        try {
            const resp = await importCuratedCollection(name.trim(), sourceUrl.trim(), items);
            onCreated(resp.collection_id);
            setName('');
            setSourceUrl('');
            setCsvText('');
            setTextareaText('');
            setItems([]);
            setErrors([]);
        } catch (err: any) {
            setSubmitError(err?.response?.data?.error ?? err?.message ?? 'failed');
        } finally {
            setSubmitting(false);
        }
    };

    return (
        <Card sx={{ mt: 2, boxShadow: 1 }}>
            <CardContent>
                <Typography variant="h6" gutterBottom>
                    {t('curatedCollections.importTitle', 'Import collection')}
                </Typography>
                <Stack spacing={2}>
                    <TextField
                        label={t('curatedCollections.name', 'Name')}
                        size="small"
                        value={name}
                        onChange={(e) => setName(e.target.value)}
                        required
                    />
                    <TextField
                        label={t('curatedCollections.sourceUrl', 'Source URL (admin note)')}
                        size="small"
                        value={sourceUrl}
                        onChange={(e) => setSourceUrl(e.target.value)}
                        placeholder="https://..."
                    />

                    <Tabs value={tab} onChange={onTabChange}>
                        <Tab label="CSV" value="csv" />
                        <Tab label={t('curatedCollections.textPaste', 'Paste text')} value="text" />
                    </Tabs>

                    {tab === 'csv' && (
                        <Box>
                            <Button variant="outlined" component="label" size="small">
                                {t('curatedCollections.uploadCsv', 'Upload CSV')}
                                <input
                                    type="file"
                                    accept=".csv,text/csv"
                                    hidden
                                    onChange={(e) => {
                                        const f = e.target.files?.[0];
                                        if (f) handleFile(f);
                                    }}
                                />
                            </Button>
                            <TextField
                                label={t('curatedCollections.csvBody', 'CSV body')}
                                placeholder="title,author,year&#10;1984,Orwell,1949"
                                multiline
                                minRows={6}
                                fullWidth
                                value={csvText}
                                onChange={(e) => onCsvTextChange(e.target.value)}
                                sx={{ mt: 2 }}
                            />
                        </Box>
                    )}

                    {tab === 'text' && (
                        <TextField
                            label={t('curatedCollections.pasteList', 'Paste list (Author; Title)')}
                            placeholder="George Orwell; 1984&#10;Aldous Huxley; Brave New World"
                            multiline
                            minRows={6}
                            fullWidth
                            value={textareaText}
                            onChange={(e) => onTextareaChange(e.target.value)}
                        />
                    )}

                    <PreviewTable items={items} />

                    {errors.length > 0 && (
                        <Alert severity="warning">
                            <Typography variant="body2">
                                {t('curatedCollections.parseWarnings', 'Parser warnings:')}
                            </Typography>
                            <ul style={{ margin: 0 }}>
                                {errors.slice(0, 5).map((e, i) => (
                                    <li key={i}>{e}</li>
                                ))}
                                {errors.length > 5 && <li>… {errors.length - 5} more</li>}
                            </ul>
                        </Alert>
                    )}

                    {submitError && <Alert severity="error">{submitError}</Alert>}

                    <Box>
                        <Button
                            variant="contained"
                            onClick={onSubmit}
                            disabled={!name.trim() || items.length === 0 || submitting}
                        >
                            {t('curatedCollections.startImport', 'Start import')}
                        </Button>
                    </Box>
                </Stack>
            </CardContent>
        </Card>
    );
};

export default ImportForm;
