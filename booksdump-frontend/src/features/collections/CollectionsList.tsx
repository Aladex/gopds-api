import React, { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { AlertCircle } from 'lucide-react';

import { Alert, AlertDescription, AlertTitle } from '@/shared/ui/alert';

import { API_URL } from '@/api/config';
import BookPagination from '@/features/catalogue/BookPagination';
import { CollectionCoverBook, listPublicCollections, PublicCollectionRow } from '@/api/collections';

// coverPath mirrors Userspace/BooksList: covers live under the
// books-posters/<sanitized-path>/<sanitized-filename>.jpg URL pattern.
const coverPath = (value: string) => value.replaceAll('.', '-').replace(/^\/+/, '');

const coverURL = (book: CollectionCoverBook) =>
    book.cover && book.path && book.filename
        ? `${API_URL}/books-posters/${coverPath(book.path)}/${coverPath(book.filename)}.jpg`
        : null;

// hashHue maps a collection name to a stable HSL hue so the empty-state
// fallback gradient is at least consistent between reloads.
const hashHue = (s: string) => {
    let h = 0;
    for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0;
    return h % 360;
};

/**
 * The gradients are derived from the collection name, so they live in style
 * rather than in a class: there is no finite set of them to name.
 */
const gradient = (hue: number, offset: number, saturation: number, from: number, to: number) =>
    `linear-gradient(135deg, hsl(${hue} ${saturation}% ${from}%), hsl(${(hue + offset) % 360} ${saturation}% ${to}%))`;

/** Tall enough for four cover tiles to read as covers rather than as swatches. */
const MOSAIC_HEIGHT = 220;

const CoverMosaic: React.FC<{ name: string; books?: CollectionCoverBook[] }> = ({
    name,
    books,
}) => {
    const real = (books ?? []).map((b) => ({ url: coverURL(b), title: b.title }));
    const withCover = real.filter((b) => b.url);
    // Pad to 4 tiles. Empty tiles get a generated initial-tile so the mosaic
    // never has half-rendered holes.
    const tiles = withCover.slice(0, 4);
    while (tiles.length < 4) tiles.push({ url: null, title: name });

    if (withCover.length === 0) {
        // No covers anywhere — full-card gradient with the collection initial.
        const initial = name.trim().charAt(0).toUpperCase() || '·';
        return (
            <div
                aria-hidden="true"
                style={{
                    height: MOSAIC_HEIGHT,
                    background: gradient(hashHue(name), 50, 40, 50, 30),
                }}
                className="flex items-center justify-center text-[84px] font-light leading-none tracking-tighter text-white"
            >
                {initial}
            </div>
        );
    }

    return (
        // The gap shows the surface underneath, which is what draws the seams
        // between the four tiles — no borders involved.
        <div
            aria-hidden="true"
            style={{ height: MOSAIC_HEIGHT }}
            className="grid grid-cols-2 grid-rows-2 gap-0.5 bg-border"
        >
            {tiles.map((tile, i) =>
                tile.url ? (
                    <img
                        key={i}
                        src={tile.url}
                        alt=""
                        loading="lazy"
                        className="size-full bg-muted object-cover"
                    />
                ) : (
                    <div
                        key={i}
                        style={{ background: gradient(hashHue(name + i), 60, 30, 55, 35) }}
                    />
                ),
            )}
        </div>
    );
};

/** A card-shaped placeholder, so the grid does not jump when the rows arrive. */
const SkeletonTile: React.FC = () => (
    <div aria-hidden="true" className="overflow-hidden rounded-xl border border-border bg-card">
        <div style={{ height: MOSAIC_HEIGHT }} className="animate-pulse bg-muted" />
        <div className="p-4">
            <div className="h-5 w-1/2 animate-pulse rounded bg-muted" />
        </div>
    </div>
);

const PAGE_SIZE = 12;

const CollectionsList: React.FC = () => {
    const { t } = useTranslation();
    const { page: pageParam } = useParams<{ page?: string }>();
    const page = Math.max(1, parseInt(pageParam ?? '1', 10) || 1);

    const [rows, setRows] = useState<PublicCollectionRow[]>([]);
    const [total, setTotal] = useState(0);
    const [loadError, setLoadError] = useState<string | null>(null);
    const [loaded, setLoaded] = useState(false);

    useEffect(() => {
        let cancelled = false;
        listPublicCollections(page, PAGE_SIZE)
            .then((data) => {
                if (!cancelled) {
                    setRows(data.rows);
                    setTotal(data.total);
                    setLoaded(true);
                    if (typeof window !== 'undefined') window.scrollTo(0, 0);
                }
            })
            .catch((err) => {
                if (!cancelled) {
                    setLoadError(err?.message ?? 'failed');
                    setLoaded(true);
                }
            });
        return () => {
            cancelled = true;
        };
    }, [page]);

    const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

    if (loadError) {
        return (
            <div className="mx-auto w-full max-w-[1200px] p-4">
                <Alert variant="destructive">
                    <AlertCircle />
                    <AlertTitle>
                        {t('publicCollections.loadError', 'Could not load collections')}
                    </AlertTitle>
                    <AlertDescription>{loadError}</AlertDescription>
                </Alert>
            </div>
        );
    }

    return (
        <div className="min-h-[calc(100vh-160px)] p-4">
            <div className="mx-auto flex min-h-[calc(100vh-200px)] w-full max-w-[1200px] flex-col">
                <h1 className="mb-2 text-2xl font-medium">
                    {t('publicCollections.title', 'Collections')}
                </h1>

                {loaded && rows.length === 0 && (
                    <p className="text-muted-foreground">
                        {t('publicCollections.empty', 'No collections yet')}
                    </p>
                )}

                <div className="mt-4 grid gap-5 sm:grid-cols-2 md:grid-cols-3">
                    {loaded
                        ? rows.map((c) => (
                              <Link
                                  key={c.id}
                                  to={`/collections/${c.id}/page/1`}
                                  className="block overflow-hidden rounded-xl border border-border bg-card transition-colors hover:border-muted-foreground"
                              >
                                  <CoverMosaic name={c.name} books={c.cover_books} />
                                  <h2 className="p-4 text-lg font-medium leading-tight text-balance">
                                      {c.name}
                                  </h2>
                              </Link>
                          ))
                        : Array.from({ length: PAGE_SIZE }).map((_, index) => (
                              <SkeletonTile key={index} />
                          ))}
                </div>

                {totalPages > 1 && (
                    <div className="mt-auto flex justify-center pt-6">
                        <BookPagination
                            totalPages={totalPages}
                            currentPage={page}
                            baseUrl={`/collections/page/${page}`}
                        />
                    </div>
                )}
            </div>
        </div>
    );
};

export default CollectionsList;
