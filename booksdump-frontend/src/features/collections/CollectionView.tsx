import React, { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import BooksList from '@/features/catalogue/BooksList';
import { getPublicCollection, PublicCollectionDetail } from '@/api/collections';

// CollectionView wraps the regular BooksList with a heading carrying the
// collection's display name. The list itself reads `?curated_collection=:id`
// from the URL via BooksList.getParams (path: /collections/:id/page/:page).
const CollectionView: React.FC = () => {
    const { id: idParam } = useParams<{ id: string; page?: string }>();
    const id = idParam ? parseInt(idParam, 10) : 0;
    const [coll, setColl] = useState<PublicCollectionDetail | null>(null);
    const [err, setErr] = useState<string | null>(null);

    useEffect(() => {
        if (!id) return;
        let cancelled = false;
        getPublicCollection(id)
            .then((d) => {
                if (!cancelled) setColl(d);
            })
            .catch((e) => {
                if (!cancelled) setErr(e?.message ?? 'failed');
            });
        return () => {
            cancelled = true;
        };
    }, [id]);

    return (
        <>
            {(coll || err) && (
                <div className="mx-auto w-full max-w-[1200px] px-4 pt-4">
                    <h1 className="text-lg font-medium">{err ? 'Error' : coll?.name}</h1>
                </div>
            )}
            <BooksList />
        </>
    );
};

export default CollectionView;
