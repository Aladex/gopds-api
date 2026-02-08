CREATE TABLE IF NOT EXISTS public.opds_catalog_genre (
    id SERIAL PRIMARY KEY,
    genre VARCHAR(128) NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS opds_catalog_genre_genre_uniq ON public.opds_catalog_genre (genre);

CREATE TABLE IF NOT EXISTS public.opds_catalog_bgenre (
    id SERIAL PRIMARY KEY,
    genre_id INTEGER NOT NULL REFERENCES public.opds_catalog_genre(id) DEFERRABLE INITIALLY DEFERRED,
    book_id INTEGER NOT NULL REFERENCES public.opds_catalog_book(id) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX IF NOT EXISTS opds_catalog_bgenre_genre_id ON public.opds_catalog_bgenre (genre_id);
CREATE INDEX IF NOT EXISTS opds_catalog_bgenre_book_id ON public.opds_catalog_bgenre (book_id);
