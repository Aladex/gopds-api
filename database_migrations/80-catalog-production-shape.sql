-- 80: the catalog production has and no migration created. The production
-- database started from a dump of the old catalog, and four things in it never
-- made it into a file, so a new install differs from it:
--
--   * opds_catalog_genre.title: models.Genre reads it and a scan writes it, so
--     without it a scan of a book with a genre and every list that loads
--     genres fail.
--   * opds_catalog_author.full_name is 1024 wide, not 128: a long author line
--     fails the scan of its book on the narrow column.
--   * auth_user.password is 512 wide, not 128.
--   * idx_book_title_trgm, a trigram index on the bare title, used by
--     production's title searches beside the one on lower(title) of 18.
--
-- Every piece reads the catalog first and acts only when it is missing, because
-- the IF NOT EXISTS forms are not free: ALTER TABLE takes ACCESS EXCLUSIVE and
-- CREATE INDEX takes SHARE before they find out there is nothing to do, and
-- a rewidening ALTER TYPE takes ACCESS EXCLUSIVE and rebuilds the expression
-- indexes on the column. On production all four are present, so this file takes
-- no table lock and changes nothing.
--
-- On a database with rows that lacks them, the column has a constant default
-- and takes no rewrite, the widening takes none either, and the index is built
-- under SHARE: writers to opds_catalog_book wait for the build, readers do not.
-- A held table gives up on lock_timeout and is retried by a starting server.

SET LOCAL lock_timeout = '5s';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public' AND table_name = 'opds_catalog_genre' AND column_name = 'title'
    ) THEN
        ALTER TABLE public.opds_catalog_genre
            ADD COLUMN title character varying(256) DEFAULT ''::character varying NOT NULL;
    END IF;

    IF (SELECT character_maximum_length FROM information_schema.columns
        WHERE table_schema = 'public' AND table_name = 'opds_catalog_author' AND column_name = 'full_name') < 1024 THEN
        ALTER TABLE public.opds_catalog_author ALTER COLUMN full_name TYPE character varying(1024);
    END IF;

    IF (SELECT character_maximum_length FROM information_schema.columns
        WHERE table_schema = 'public' AND table_name = 'auth_user' AND column_name = 'password') < 512 THEN
        ALTER TABLE public.auth_user ALTER COLUMN password TYPE character varying(512);
    END IF;

    IF to_regclass('public.idx_book_title_trgm') IS NULL THEN
        CREATE INDEX idx_book_title_trgm ON public.opds_catalog_book USING gin (title public.gin_trgm_ops);
    END IF;
END$$;
