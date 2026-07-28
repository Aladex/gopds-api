import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Pencil, Save, Sparkles, X } from 'lucide-react';

import { Button } from '@/shared/ui/button';
import { Card, CardContent } from '@/shared/ui/card';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/shared/ui/dialog';
import { Field } from '@/shared/ui/field';
import { Input } from '@/shared/ui/input';
import { Progress } from '@/shared/ui/progress';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/shared/ui/table';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import * as adminApi from '@/api/admin';
import { WS_URL } from '@/api/config';

interface GenreAdmin {
    id: number;
    genre: string;
    title: string;
}

interface GenreTitleGenStartedEvent {
    total: number;
    timestamp: string;
}

interface GenreTitleGenProgressEvent {
    total: number;
    processed: number;
    current_genre: string;
    progress_percent: number;
    timestamp: string;
}

interface GenreTitleGenCompletedEvent {
    total: number;
    updated: number;
    duration_ms: number;
    timestamp: string;
}

const GenreManagement: React.FC = () => {
    const { t } = useTranslation();
    // Below this the three-column table stops fitting and each genre becomes a
    // card of its own. It is the width MUI called `md`, kept so the layout does
    // not shift underneath anyone mid-migration.
    const isMobile = useMediaQuery('(max-width: 899px)');

    const [genres, setGenres] = useState<GenreAdmin[]>([]);
    const [searchQuery, setSearchQuery] = useState('');
    const [editingId, setEditingId] = useState<number | null>(null);
    const [editValue, setEditValue] = useState('');
    const [isLoading, setIsLoading] = useState(false);
    const [isGenerating, setIsGenerating] = useState(false);
    const [progressPercent, setProgressPercent] = useState(0);
    const [progressCurrent, setProgressCurrent] = useState('');
    const [progressProcessed, setProgressProcessed] = useState(0);
    const [progressTotal, setProgressTotal] = useState(0);
    const [confirmDialogOpen, setConfirmDialogOpen] = useState(false);
    const wsRef = useRef<WebSocket | null>(null);

    const fetchGenres = useCallback(async () => {
        setIsLoading(true);
        try {
            const data = await adminApi.listGenres<GenreAdmin>();
            setGenres(data.result || []);
        } catch (error) {
            console.error('Failed to fetch genres', error);
        } finally {
            setIsLoading(false);
        }
    }, []);

    useEffect(() => {
        fetchGenres();
    }, [fetchGenres]);

    // WebSocket for genre title generation progress
    useEffect(() => {
        const ws = new WebSocket(`${WS_URL}/api/ws`);
        wsRef.current = ws;

        ws.onmessage = (event) => {
            try {
                const message = JSON.parse(event.data);
                switch (message.type) {
                    case 'genre_title_gen_started': {
                        const payload = message.data as GenreTitleGenStartedEvent;
                        setIsGenerating(true);
                        setProgressPercent(0);
                        setProgressProcessed(0);
                        setProgressTotal(payload.total);
                        setProgressCurrent('');
                        break;
                    }
                    case 'genre_title_gen_progress': {
                        const payload = message.data as GenreTitleGenProgressEvent;
                        setIsGenerating(true);
                        setProgressPercent(payload.progress_percent);
                        setProgressProcessed(payload.processed);
                        setProgressTotal(payload.total);
                        setProgressCurrent(payload.current_genre);
                        break;
                    }
                    case 'genre_title_gen_completed': {
                        const payload = message.data as GenreTitleGenCompletedEvent;
                        setIsGenerating(false);
                        setProgressPercent(100);
                        toast.success(t('titlesGenerated', { count: payload.updated }));
                        fetchGenres();
                        break;
                    }
                    default:
                        break;
                }
            } catch (error) {
                console.error('Failed to parse WebSocket message', error);
            }
        };

        ws.onerror = (error) => {
            console.error('Genre WS error', error);
        };

        ws.onclose = () => {
            wsRef.current = null;
        };

        return () => {
            if (wsRef.current) {
                wsRef.current.close();
                wsRef.current = null;
            }
        };
    }, [fetchGenres, t]);

    const handleEdit = (genre: GenreAdmin) => {
        setEditingId(genre.id);
        setEditValue(genre.title);
    };

    const handleCancelEdit = () => {
        setEditingId(null);
        setEditValue('');
    };

    const handleSave = async (id: number) => {
        try {
            await adminApi.updateGenre(id, { title: editValue });
            setGenres((prev) => prev.map((g) => (g.id === id ? { ...g, title: editValue } : g)));
            setEditingId(null);
            setEditValue('');
            toast.success(t('genreTitleUpdated'));
        } catch (error) {
            console.error('Failed to update genre title', error);
            // Not errorUpdatingBook: this screen has no books in it, and the
            // old message told the admin a book had failed to save.
            toast.error(t('genreTitleUpdateError', 'Could not update the genre title'));
        }
    };

    const handleConfirmGenerate = async () => {
        setConfirmDialogOpen(false);
        try {
            await adminApi.generateGenreTitles();
        } catch (error) {
            console.error('Failed to start genre title generation', error);
            toast.error(t('bookScanStartError'));
        }
    };

    const filteredGenres = genres.filter((g) => {
        if (!searchQuery) return true;
        const q = searchQuery.toLowerCase();
        return g.genre.toLowerCase().includes(q) || g.title.toLowerCase().includes(q);
    });

    /** The pair of buttons an open editor shows, shared by both layouts. */
    const renderEditActions = (genre: GenreAdmin) => (
        <>
            <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => handleSave(genre.id)}
                title={t('save')}
                aria-label={`${t('save')}: ${genre.genre}`}
            >
                <Save className="size-4 text-primary" />
            </Button>
            <Button
                variant="ghost"
                size="icon-sm"
                onClick={handleCancelEdit}
                title={t('cancel')}
                aria-label={`${t('cancel')}: ${genre.genre}`}
            >
                <X className="size-4" />
            </Button>
        </>
    );

    const renderEditInput = (genre: GenreAdmin) => (
        <Input
            autoFocus
            value={editValue}
            aria-label={`${t('genreTitle')}: ${genre.genre}`}
            onChange={(event) => setEditValue(event.target.value)}
            onKeyDown={(event) => {
                if (event.key === 'Enter') handleSave(genre.id);
                if (event.key === 'Escape') handleCancelEdit();
            }}
        />
    );

    const renderGenreRow = (genre: GenreAdmin) => {
        const isEditing = editingId === genre.id;
        return (
            <TableRow key={genre.id}>
                <TableCell className="font-mono text-[0.85rem]">{genre.genre}</TableCell>
                <TableCell>{isEditing ? renderEditInput(genre) : genre.title}</TableCell>
                <TableCell className="whitespace-nowrap text-right">
                    {isEditing ? (
                        renderEditActions(genre)
                    ) : (
                        <Button
                            variant="ghost"
                            size="icon-sm"
                            onClick={() => handleEdit(genre)}
                            title={t('edit')}
                            aria-label={`${t('edit')}: ${genre.genre}`}
                        >
                            <Pencil className="size-4" />
                        </Button>
                    )}
                </TableCell>
            </TableRow>
        );
    };

    const renderGenreCard = (genre: GenreAdmin) => {
        const isEditing = editingId === genre.id;
        return (
            <Card key={genre.id} className="gap-0 py-2">
                <CardContent className="flex flex-col gap-1 px-3">
                    <span className="font-mono text-xs text-muted-foreground">{genre.genre}</span>
                    {isEditing ? (
                        <div className="flex items-center gap-1">
                            {renderEditInput(genre)}
                            {renderEditActions(genre)}
                        </div>
                    ) : (
                        <div className="flex items-center justify-between gap-2">
                            <span className="text-sm">{genre.title}</span>
                            <Button
                                variant="ghost"
                                size="icon-sm"
                                onClick={() => handleEdit(genre)}
                                title={t('edit')}
                                aria-label={`${t('edit')}: ${genre.genre}`}
                            >
                                <Pencil className="size-4" />
                            </Button>
                        </div>
                    )}
                </CardContent>
            </Card>
        );
    };

    return (
        <div className="mt-4 flex flex-col gap-4">
            <div className="flex flex-wrap items-end gap-4">
                <Field
                    id="genre-search"
                    label={t('searchGenres')}
                    className="min-w-[12.5rem] max-w-[25rem] flex-1"
                >
                    <Input
                        id="genre-search"
                        value={searchQuery}
                        onChange={(event) => setSearchQuery(event.target.value)}
                    />
                </Field>
                <Button onClick={() => setConfirmDialogOpen(true)} disabled={isGenerating}>
                    <Sparkles className="size-4" />
                    {t('generateWithAI')}
                </Button>
            </div>

            {isGenerating && (
                <div className="flex flex-col gap-1">
                    <p className="text-sm text-muted-foreground">
                        {t('generatingTitles')}{' '}
                        <span className="tabular-nums">
                            {progressProcessed}/{progressTotal}
                        </span>
                        {progressCurrent && ` — ${progressCurrent}`}
                    </p>
                    <Progress value={progressPercent} aria-label={t('generatingTitles')} />
                </div>
            )}

            {isLoading ? (
                <Progress aria-label={t('loading')} />
            ) : filteredGenres.length === 0 ? (
                <p className="py-6 text-center text-sm text-muted-foreground">{t('noGenres')}</p>
            ) : isMobile ? (
                <div className="flex flex-col gap-2">{filteredGenres.map(renderGenreCard)}</div>
            ) : (
                <div className="rounded border border-border bg-card">
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t('genreTag')}</TableHead>
                                <TableHead>{t('genreTitle')}</TableHead>
                                <TableHead className="text-right">{t('actions')}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>{filteredGenres.map(renderGenreRow)}</TableBody>
                    </Table>
                </div>
            )}

            <Dialog open={confirmDialogOpen} onOpenChange={setConfirmDialogOpen}>
                <DialogContent closeLabel={t('close')}>
                    <DialogHeader>
                        <DialogTitle>{t('generateWithAI')}</DialogTitle>
                        <DialogDescription>{t('generateConfirm')}</DialogDescription>
                    </DialogHeader>
                    <DialogFooter>
                        <Button variant="ghost" onClick={() => setConfirmDialogOpen(false)}>
                            {t('cancel')}
                        </Button>
                        <Button onClick={handleConfirmGenerate}>{t('generateWithAI')}</Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
};

export default GenreManagement;
