-- Migration: Add scan tracking columns to opds_catalog_catalog
-- Date: 2026-01-07
-- Description: Add scanned_at, books_count, and errors_count columns to track scanning progress

-- Add new columns to opds_catalog_catalog table
ALTER TABLE public.opds_catalog_catalog
ADD COLUMN IF NOT EXISTS scanned_at TIMESTAMP NULL;

ALTER TABLE public.opds_catalog_catalog
ADD COLUMN IF NOT EXISTS books_count INTEGER DEFAULT 0;

ALTER TABLE public.opds_catalog_catalog
ADD COLUMN IF NOT EXISTS errors_count INTEGER DEFAULT 0;

-- Create index for efficient queries on is_scanned status
CREATE INDEX IF NOT EXISTS idx_catalog_is_scanned
ON public.opds_catalog_catalog(is_scanned);

-- Create index for queries on scanned_at timestamp
CREATE INDEX IF NOT EXISTS idx_catalog_scanned_at
ON public.opds_catalog_catalog(scanned_at);

COMMENT ON COLUMN public.opds_catalog_catalog.scanned_at IS 'Timestamp when the archive was last scanned';
COMMENT ON COLUMN public.opds_catalog_catalog.books_count IS 'Number of books successfully added from this archive';
COMMENT ON COLUMN public.opds_catalog_catalog.errors_count IS 'Number of errors encountered during scanning';

