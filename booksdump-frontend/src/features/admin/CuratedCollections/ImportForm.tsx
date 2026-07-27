import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { AlertCircle, AlertTriangle, Upload } from 'lucide-react';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Button, buttonVariants } from '@/shared/ui/button';
import { Card, CardContent } from '@/shared/ui/card';
import { Field } from '@/shared/ui/field';
import { Input } from '@/shared/ui/input';
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from '@/shared/ui/table';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/shared/ui/tabs';
import { Textarea } from '@/shared/ui/textarea';
import { cn } from '@/shared/lib/utils';

import { importCuratedCollection } from '@/features/admin/CuratedCollections/api';
import { parseCsv, parseTextarea, ParsedItem } from '@/features/admin/CuratedCollections/csvParser';

type ImportTab = 'csv' | 'text';

/** A pasted list can be thousands of rows; the preview shows enough to check. */
const PREVIEW_LIMIT = 200;

const PreviewTable: React.FC<{ items: ParsedItem[] }> = ({ items }) => {
    const { t } = useTranslation();
    if (items.length === 0) return null;

    return (
        <div className="flex flex-col gap-1">
            <h4 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
                {t('curatedCollections.preview', 'Preview')} ({items.length})
            </h4>
            <div className="scrollbar-thin max-h-60 overflow-auto rounded border border-border">
                <Table>
                    <TableHeader>
                        <TableRow>
                            <TableHead className="w-12 text-right">#</TableHead>
                            <TableHead>{t('title', 'Title')}</TableHead>
                            <TableHead>{t('author', 'Author')}</TableHead>
                            <TableHead className="text-right">{t('year', 'Year')}</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {items.slice(0, PREVIEW_LIMIT).map((item, index) => (
                            <TableRow key={index}>
                                <TableCell className="text-right tabular-nums text-muted-foreground">
                                    {index + 1}
                                </TableCell>
                                <TableCell>{item.title}</TableCell>
                                <TableCell>{item.author}</TableCell>
                                <TableCell className="text-right tabular-nums">
                                    {item.year ?? ''}
                                </TableCell>
                            </TableRow>
                        ))}
                    </TableBody>
                </Table>
            </div>
            {items.length > PREVIEW_LIMIT && (
                <p className="text-xs text-muted-foreground">
                    {t('curatedCollections.previewTruncated', 'Showing the first {{shown}} of {{total}}', {
                        shown: PREVIEW_LIMIT,
                        total: items.length,
                    })}
                </p>
            )}
        </div>
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

    // Each tab parses by its own rules, so what the other one produced is not
    // this one's preview.
    const onTabChange = (next: string) => {
        setTab(next as ImportTab);
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
        } catch (err) {
            setSubmitError(err instanceof Error ? err.message : 'failed');
        } finally {
            setSubmitting(false);
        }
    };

    return (
        <Card>
            <CardContent className="flex flex-col gap-4">
                <h2 className="text-base font-medium">
                    {t('curatedCollections.importTitle', 'Import collection')}
                </h2>

                <Field id="import-name" label={`${t('curatedCollections.name', 'Name')} *`}>
                    <Input
                        id="import-name"
                        required
                        value={name}
                        onChange={(event) => setName(event.target.value)}
                    />
                </Field>

                <Field
                    id="import-source-url"
                    label={t('curatedCollections.sourceUrl', 'Source URL (admin note)')}
                >
                    <Input
                        id="import-source-url"
                        placeholder="https://..."
                        value={sourceUrl}
                        onChange={(event) => setSourceUrl(event.target.value)}
                    />
                </Field>

                <Tabs value={tab} onValueChange={onTabChange}>
                    <TabsList className="w-auto self-start">
                        <TabsTrigger value="csv">CSV</TabsTrigger>
                        <TabsTrigger value="text">
                            {t('curatedCollections.textPaste', 'Paste text')}
                        </TabsTrigger>
                    </TabsList>

                    <TabsContent value="csv" className="flex flex-col gap-3">
                        {/*
                          A file input styled as a button by wrapping it in its
                          own label: the browser's own control cannot be styled,
                          and replacing it with a button would mean scripting a
                          click onto a hidden input for no gain.
                        */}
                        <label
                            className={cn(
                                buttonVariants({ variant: 'outline', size: 'sm' }),
                                'cursor-pointer self-start',
                            )}
                        >
                            <Upload className="size-4" />
                            {t('curatedCollections.uploadCsv', 'Upload CSV')}
                            <input
                                type="file"
                                accept=".csv,text/csv"
                                className="sr-only"
                                onChange={(event) => {
                                    const file = event.target.files?.[0];
                                    if (file) handleFile(file);
                                }}
                            />
                        </label>

                        <Field id="import-csv" label={t('curatedCollections.csvBody', 'CSV body')}>
                            <Textarea
                                id="import-csv"
                                rows={6}
                                placeholder={'title,author,year\n1984,Orwell,1949'}
                                value={csvText}
                                onChange={(event) => onCsvTextChange(event.target.value)}
                            />
                        </Field>
                    </TabsContent>

                    <TabsContent value="text">
                        <Field
                            id="import-paste"
                            label={t('curatedCollections.pasteList', 'Paste list (Author; Title)')}
                        >
                            <Textarea
                                id="import-paste"
                                rows={6}
                                placeholder={'George Orwell; 1984\nAldous Huxley; Brave New World'}
                                value={textareaText}
                                onChange={(event) => onTextareaChange(event.target.value)}
                            />
                        </Field>
                    </TabsContent>
                </Tabs>

                <PreviewTable items={items} />

                {errors.length > 0 && (
                    <Alert>
                        <AlertTriangle className="size-4" />
                        <AlertDescription className="flex flex-col gap-1">
                            <span>{t('curatedCollections.parseWarnings', 'Parser warnings:')}</span>
                            {/* p-0 because Tailwind runs without preflight, so a
                                bare list keeps the browser's 40px indent. */}
                            <ul className="list-inside list-disc p-0">
                                {errors.slice(0, 5).map((error, index) => (
                                    <li key={index}>{error}</li>
                                ))}
                                {errors.length > 5 && (
                                    <li>
                                        {t('curatedCollections.moreWarnings', '… {{count}} more', {
                                            count: errors.length - 5,
                                        })}
                                    </li>
                                )}
                            </ul>
                        </AlertDescription>
                    </Alert>
                )}

                {submitError && (
                    <Alert variant="destructive">
                        <AlertCircle className="size-4" />
                        <AlertDescription>{submitError}</AlertDescription>
                    </Alert>
                )}

                <Button
                    className="self-start"
                    onClick={onSubmit}
                    disabled={!name.trim() || items.length === 0 || submitting}
                >
                    {t('curatedCollections.startImport', 'Start import')}
                </Button>
            </CardContent>
        </Card>
    );
};

export default ImportForm;
