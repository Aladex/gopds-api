-- Migration 12: Create admin_scan_jobs table
-- This table tracks the progress of duplicate scanning jobs

CREATE TABLE public.admin_scan_jobs (
    id SERIAL PRIMARY KEY,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    total_books INTEGER DEFAULT 0,
    processed_books INTEGER DEFAULT 0,
    duplicates_found INTEGER DEFAULT 0,
    started_at TIMESTAMP WITH TIME ZONE,
    finished_at TIMESTAMP WITH TIME ZONE,
    error TEXT,
    scan_params JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Add constraint to ensure status is one of the valid values
ALTER TABLE public.admin_scan_jobs
    ADD CONSTRAINT admin_scan_jobs_status_check 
    CHECK (status IN ('pending', 'running', 'completed', 'failed'));

-- Create index on status for filtering active/completed jobs
CREATE INDEX admin_scan_jobs_status_idx 
    ON public.admin_scan_jobs (status);

-- Create index on created_at for chronological queries
CREATE INDEX admin_scan_jobs_created_at_idx 
    ON public.admin_scan_jobs (created_at DESC);

-- Create trigger to auto-update updated_at timestamp
-- Reuse the existing update_updated_at_column function from migration 08
CREATE TRIGGER update_admin_scan_jobs_updated_at
    BEFORE UPDATE ON public.admin_scan_jobs
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Add comments for documentation
COMMENT ON TABLE public.admin_scan_jobs IS 'Tracks progress of duplicate scanning jobs';
COMMENT ON COLUMN public.admin_scan_jobs.status IS 'Job status: pending, running, completed, failed';
COMMENT ON COLUMN public.admin_scan_jobs.total_books IS 'Total number of books to scan';
COMMENT ON COLUMN public.admin_scan_jobs.processed_books IS 'Number of books processed so far';
COMMENT ON COLUMN public.admin_scan_jobs.duplicates_found IS 'Number of duplicate books found';
COMMENT ON COLUMN public.admin_scan_jobs.scan_params IS 'JSON parameters for the scan job';
