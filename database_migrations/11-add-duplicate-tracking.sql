-- Migration 11: Add duplicate tracking fields to opds_catalog_book
-- These fields support the duplicate hiding feature

-- Add duplicate_hidden flag (default false - book is visible)
ALTER TABLE public.opds_catalog_book 
    ADD COLUMN duplicate_hidden BOOLEAN DEFAULT FALSE NOT NULL;

-- Add duplicate_of_id to track which book is the "keeper" for this duplicate
-- NULL means this book is either not a duplicate or is the keeper itself
ALTER TABLE public.opds_catalog_book 
    ADD COLUMN duplicate_of_id INTEGER;

-- Add foreign key constraint to ensure duplicate_of_id references a valid book
ALTER TABLE public.opds_catalog_book
    ADD CONSTRAINT opds_catalog_book_duplicate_of_id_fk 
    FOREIGN KEY (duplicate_of_id) 
    REFERENCES public.opds_catalog_book(id) 
    ON DELETE SET NULL
    DEFERRABLE INITIALLY DEFERRED;

-- Create index on duplicate_of_id for efficient queries
CREATE INDEX opds_catalog_book_duplicate_of_id_idx 
    ON public.opds_catalog_book (duplicate_of_id);

-- Create index on duplicate_hidden for filtering visible books
CREATE INDEX opds_catalog_book_duplicate_hidden_idx 
    ON public.opds_catalog_book (duplicate_hidden);

-- Add comments for documentation
COMMENT ON COLUMN public.opds_catalog_book.duplicate_hidden IS 'Flag indicating if this book is hidden as a duplicate';
COMMENT ON COLUMN public.opds_catalog_book.duplicate_of_id IS 'Reference to the keeper book if this is a hidden duplicate';
