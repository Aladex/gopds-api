-- An approved single-book rescan refreshes the book's author metadata layer
-- from the bytes its preview read: the preview keeps the head of the file the
-- extractor needs (it never reads past </description> once the MD5 is known)
-- and the MD5 of the whole file, so the approval writes the layer without
-- reading the archive a second time. A pending rescan created before this
-- migration has neither and refreshes nothing.

SET LOCAL lock_timeout = '5s';

ALTER TABLE public.book_rescan_pending
    ADD COLUMN author_source_head BYTEA,
    ADD COLUMN author_source_md5 TEXT,
    ADD CONSTRAINT book_rescan_pending_author_source_check
        CHECK ((author_source_head IS NULL) = (author_source_md5 IS NULL)
               AND (author_source_md5 IS NULL OR author_source_md5 ~ '^[0-9a-f]{32}$'));
