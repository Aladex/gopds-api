-- Migration 10: Add MD5 hash column to opds_catalog_book
-- This column will store the MD5 hash of the book file content for duplicate detection

-- Add md5 column (nullable to allow gradual population)
ALTER TABLE public.opds_catalog_book 
    ADD COLUMN md5 VARCHAR(32) DEFAULT '' NOT NULL;

-- Create partial index on md5 for fast duplicate grouping
-- Only index non-empty values to optimize storage and performance
CREATE INDEX opds_catalog_book_md5_idx 
    ON public.opds_catalog_book (md5) 
    WHERE md5 != '';

-- Optional: Add comment for documentation
COMMENT ON COLUMN public.opds_catalog_book.md5 IS 'MD5 hash of book file content for duplicate detection';
