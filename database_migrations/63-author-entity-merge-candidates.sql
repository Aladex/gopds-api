-- 63: merge candidates in the entity build (E4a, plan §2.3, §4.2, §4.6,
-- §4.10). Observation only: nothing here merges an entity or calls a model.
--
--   * author_entity_merge_candidate — the pairs a model may later be asked
--     "same person?", one row per digest. The digest names the exact
--     question: the sides' member keys and the card input (§4.2). The build
--     writes the table as a diff once its pass has committed: a new digest
--     is inserted, a gone one deleted, a kept one left alone, so a pass
--     that changed nothing writes nothing. IDs and hashes only, no card text.
--   * author_entity_merge_candidate_member — the member keys of each side.
--   * author_entity_merge_candidate_sample — per (kind, script, reason) the
--     rows of the lowest member hash, with the sides' Go keys: the census
--     SQL is reconciled against it (§2.3). One sample, the newest pass's,
--     written as a diff too.
--
-- And the dependency marks (§4.10): author_autopilot_mark now means "an
-- input of the entity build changed", every input of the candidates' cards
-- included. Statement triggers reuse author_autopilot_mark() (a statement
-- that changed no row marks nothing); where only some columns are inputs —
-- a book's title, annotation, docdate and lang, a snapshot's is_current and
-- source_year, a genre's code, a series' name — a row trigger with a WHEN
-- marks only when one of them changed, and costs nothing for the other
-- updates (opds_catalog_book takes ~1.9M of them per stats period). The
-- assignment tables mark only for a manual row: the build writes the
-- automatic ones and must never make a pass due by its own writes. The
-- Wikidata extract marks when one is published or stops being.
--
-- Locks: every table that gets a trigger is taken SHARE ROW EXCLUSIVE, up
-- front, NOWAIT, like 28-62: a writer holding one makes this file fail at
-- once, and the next start applies it whole. The new tables reference no
-- table, so a running pass or any other writer does not hold them back.
-- They are empty.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.contributor_normalization_result IN SHARE ROW EXCLUSIVE MODE NOWAIT;
LOCK TABLE public.book_metadata_snapshot IN SHARE ROW EXCLUSIVE MODE NOWAIT;
LOCK TABLE public.opds_catalog_book IN SHARE ROW EXCLUSIVE MODE NOWAIT;
LOCK TABLE public.opds_catalog_bgenre IN SHARE ROW EXCLUSIVE MODE NOWAIT;
LOCK TABLE public.opds_catalog_genre IN SHARE ROW EXCLUSIVE MODE NOWAIT;
LOCK TABLE public.opds_catalog_bseries IN SHARE ROW EXCLUSIVE MODE NOWAIT;
LOCK TABLE public.opds_catalog_series IN SHARE ROW EXCLUSIVE MODE NOWAIT;
LOCK TABLE public.author_entity_assignment IN SHARE ROW EXCLUSIVE MODE NOWAIT;
LOCK TABLE public.author_entity_credit_assignment IN SHARE ROW EXCLUSIVE MODE NOWAIT;
LOCK TABLE public.wikidata_extract IN SHARE ROW EXCLUSIVE MODE NOWAIT;

CREATE TABLE public.author_entity_merge_candidate (
    digest             bytea  PRIMARY KEY,
    kind               text   NOT NULL,
    script             text   NOT NULL,
    item_kind          text   NOT NULL,
    side_a_entity      uuid   NOT NULL,
    side_b_entity      uuid   NOT NULL,
    -- A book item's bucket credit.
    credit_id          bigint,
    credit_ordinal     smallint,
    members_sha256     bytea  NOT NULL,
    input_sha256       bytea  NOT NULL,
    -- The pass that first found it (no foreign key: a reference would lock
    -- the runs a running pass writes).
    first_build_run_id bigint NOT NULL,
    CONSTRAINT author_entity_merge_candidate_hash_check
        CHECK (octet_length(digest) = 32 AND octet_length(members_sha256) = 32 AND octet_length(input_sha256) = 32),
    CONSTRAINT author_entity_merge_candidate_kind_check
        CHECK (kind IN ('bucket', 'swap', 'initials', 'translit')),
    CONSTRAINT author_entity_merge_candidate_script_check CHECK (script IN ('Cyrl', 'Latn', 'Cyrl-Latn')),
    CONSTRAINT author_entity_merge_candidate_item_check
        CHECK ((item_kind = 'book') = (kind = 'bucket') AND item_kind IN ('entity', 'book')
               AND (item_kind = 'book') = (credit_id IS NOT NULL)
               AND (credit_id IS NULL) = (credit_ordinal IS NULL) AND (credit_ordinal IS NULL OR credit_ordinal >= 0)),
    CONSTRAINT author_entity_merge_candidate_sides_check CHECK (side_a_entity <> side_b_entity)
);

CREATE TABLE public.author_entity_merge_candidate_member (
    digest             bytea    NOT NULL REFERENCES public.author_entity_merge_candidate (digest) ON DELETE CASCADE,
    side               text     NOT NULL,
    source_fingerprint bytea    NOT NULL,
    extractor_version  text     NOT NULL,
    ordinal            smallint NOT NULL,
    CONSTRAINT author_entity_merge_candidate_member_pkey
        PRIMARY KEY (digest, side, source_fingerprint, extractor_version, ordinal),
    CONSTRAINT author_entity_merge_candidate_member_side_check CHECK (side IN ('a', 'b')),
    CONSTRAINT author_entity_merge_candidate_member_ordinal_check CHECK (ordinal >= 0)
);

CREATE TABLE public.author_entity_merge_candidate_sample (
    -- The pass that wrote the row.
    build_run_id   bigint NOT NULL,
    kind           text   NOT NULL,
    script         text   NOT NULL,
    -- 'candidate' or the reason the pair is none.
    reason         text   NOT NULL,
    members_sha256 bytea  NOT NULL,
    entity_a       uuid   NOT NULL,
    entity_b       uuid   NOT NULL,
    credit_id      bigint,
    go_keys_a      text[] NOT NULL,
    go_keys_b      text[] NOT NULL,
    -- The candidate's digest; NULL for an excluded pair.
    digest         bytea,
    CONSTRAINT author_entity_merge_candidate_sample_pkey PRIMARY KEY (kind, script, reason, members_sha256),
    CONSTRAINT author_entity_merge_candidate_sample_codes_check
        CHECK (kind IN ('bucket', 'swap', 'initials', 'translit') AND script ~ '^[A-Za-z][A-Za-z-]{0,15}$'
               AND reason ~ '^[a-z][a-z0-9_]{0,63}$'),
    CONSTRAINT author_entity_merge_candidate_sample_digest_check
        CHECK ((reason = 'candidate') = (digest IS NOT NULL) AND (digest IS NULL OR octet_length(digest) = 32)
               AND octet_length(members_sha256) = 32)
);

-- A row trigger's mark: its WHEN already decided the row changed an input.
CREATE FUNCTION public.author_entity_input_mark() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO public.author_autopilot_mark (xact) VALUES (pg_current_xact_id()) ON CONFLICT (xact) DO NOTHING;
    RETURN NULL;
END
$$;

-- An assignment statement marks only when it inserted, deleted or changed a
-- manual row: the build's own rows are never manual.
CREATE FUNCTION public.author_entity_manual_mark() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    manual_changed boolean;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        manual_changed := EXISTS (SELECT * FROM changed WHERE method = 'manual' EXCEPT SELECT * FROM previous)
            OR EXISTS (SELECT * FROM previous WHERE method = 'manual' EXCEPT SELECT * FROM changed);
    ELSE
        manual_changed := EXISTS (SELECT 1 FROM changed WHERE method = 'manual');
    END IF;
    IF manual_changed THEN
        INSERT INTO public.author_autopilot_mark (xact) VALUES (pg_current_xact_id()) ON CONFLICT (xact) DO NOTHING;
    END IF;
    RETURN NULL;
END
$$;

-- Members' and co-authors' names (the result rows themselves; production
-- writes them with their selections, which mark already).
CREATE TRIGGER contributor_normalization_result_entity_input_insert
    AFTER INSERT ON public.contributor_normalization_result
    REFERENCING NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_autopilot_mark();
CREATE TRIGGER contributor_normalization_result_entity_input_update
    AFTER UPDATE ON public.contributor_normalization_result
    REFERENCING OLD TABLE AS previous NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_autopilot_mark();
CREATE TRIGGER contributor_normalization_result_entity_input_delete
    AFTER DELETE ON public.contributor_normalization_result
    REFERENCING OLD TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_autopilot_mark();

-- Which credits are current, the source year.
CREATE TRIGGER book_metadata_snapshot_entity_input_insert
    AFTER INSERT ON public.book_metadata_snapshot
    REFERENCING NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_autopilot_mark();
CREATE TRIGGER book_metadata_snapshot_entity_input_update
    AFTER UPDATE OF is_current, source_year ON public.book_metadata_snapshot
    FOR EACH ROW WHEN (OLD.is_current IS DISTINCT FROM NEW.is_current OR OLD.source_year IS DISTINCT FROM NEW.source_year)
    EXECUTE FUNCTION public.author_entity_input_mark();
CREATE TRIGGER book_metadata_snapshot_entity_input_delete
    AFTER DELETE ON public.book_metadata_snapshot
    REFERENCING OLD TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_autopilot_mark();

-- The sample (book set and order by id) and a book's card columns.
CREATE TRIGGER opds_catalog_book_entity_input_insert
    AFTER INSERT ON public.opds_catalog_book
    REFERENCING NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_autopilot_mark();
CREATE TRIGGER opds_catalog_book_entity_input_update
    AFTER UPDATE OF id, title, annotation, docdate, lang ON public.opds_catalog_book
    FOR EACH ROW WHEN (OLD.id IS DISTINCT FROM NEW.id OR OLD.title IS DISTINCT FROM NEW.title
        OR OLD.annotation IS DISTINCT FROM NEW.annotation OR OLD.docdate IS DISTINCT FROM NEW.docdate
        OR OLD.lang IS DISTINCT FROM NEW.lang)
    EXECUTE FUNCTION public.author_entity_input_mark();
CREATE TRIGGER opds_catalog_book_entity_input_delete
    AFTER DELETE ON public.opds_catalog_book
    REFERENCING OLD TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_autopilot_mark();

-- Genre links and codes (never the titles, which are not on the card).
CREATE TRIGGER opds_catalog_bgenre_entity_input_insert
    AFTER INSERT ON public.opds_catalog_bgenre
    REFERENCING NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_autopilot_mark();
CREATE TRIGGER opds_catalog_bgenre_entity_input_update
    AFTER UPDATE ON public.opds_catalog_bgenre
    REFERENCING OLD TABLE AS previous NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_autopilot_mark();
CREATE TRIGGER opds_catalog_bgenre_entity_input_delete
    AFTER DELETE ON public.opds_catalog_bgenre
    REFERENCING OLD TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_autopilot_mark();
CREATE TRIGGER opds_catalog_genre_entity_input_insert
    AFTER INSERT ON public.opds_catalog_genre
    REFERENCING NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_autopilot_mark();
CREATE TRIGGER opds_catalog_genre_entity_input_update
    AFTER UPDATE OF id, genre ON public.opds_catalog_genre
    FOR EACH ROW WHEN (OLD.id IS DISTINCT FROM NEW.id OR OLD.genre IS DISTINCT FROM NEW.genre)
    EXECUTE FUNCTION public.author_entity_input_mark();
CREATE TRIGGER opds_catalog_genre_entity_input_delete
    AFTER DELETE ON public.opds_catalog_genre
    REFERENCING OLD TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_autopilot_mark();

-- Series links and names.
CREATE TRIGGER opds_catalog_bseries_entity_input_insert
    AFTER INSERT ON public.opds_catalog_bseries
    REFERENCING NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_autopilot_mark();
CREATE TRIGGER opds_catalog_bseries_entity_input_update
    AFTER UPDATE ON public.opds_catalog_bseries
    REFERENCING OLD TABLE AS previous NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_autopilot_mark();
CREATE TRIGGER opds_catalog_bseries_entity_input_delete
    AFTER DELETE ON public.opds_catalog_bseries
    REFERENCING OLD TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_autopilot_mark();
CREATE TRIGGER opds_catalog_series_entity_input_insert
    AFTER INSERT ON public.opds_catalog_series
    REFERENCING NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_autopilot_mark();
CREATE TRIGGER opds_catalog_series_entity_input_update
    AFTER UPDATE OF id, ser ON public.opds_catalog_series
    FOR EACH ROW WHEN (OLD.id IS DISTINCT FROM NEW.id OR OLD.ser IS DISTINCT FROM NEW.ser)
    EXECUTE FUNCTION public.author_entity_input_mark();
CREATE TRIGGER opds_catalog_series_entity_input_delete
    AFTER DELETE ON public.opds_catalog_series
    REFERENCING OLD TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_autopilot_mark();

-- manual_side and manual precedence: only manual rows mark.
CREATE TRIGGER author_entity_assignment_entity_input_insert
    AFTER INSERT ON public.author_entity_assignment
    REFERENCING NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_entity_manual_mark();
CREATE TRIGGER author_entity_assignment_entity_input_update
    AFTER UPDATE ON public.author_entity_assignment
    REFERENCING OLD TABLE AS previous NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_entity_manual_mark();
CREATE TRIGGER author_entity_assignment_entity_input_delete
    AFTER DELETE ON public.author_entity_assignment
    REFERENCING OLD TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_entity_manual_mark();
CREATE TRIGGER author_entity_credit_assignment_entity_input_insert
    AFTER INSERT ON public.author_entity_credit_assignment
    REFERENCING NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_entity_manual_mark();
CREATE TRIGGER author_entity_credit_assignment_entity_input_update
    AFTER UPDATE ON public.author_entity_credit_assignment
    REFERENCING OLD TABLE AS previous NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_entity_manual_mark();
CREATE TRIGGER author_entity_credit_assignment_entity_input_delete
    AFTER DELETE ON public.author_entity_credit_assignment
    REFERENCING OLD TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_entity_manual_mark();

-- Level 0's Wikidata merge and the pseudonym keys: the published extract.
CREATE TRIGGER wikidata_extract_entity_input_update
    AFTER UPDATE OF status ON public.wikidata_extract
    FOR EACH ROW WHEN ((OLD.status = 'published') IS DISTINCT FROM (NEW.status = 'published'))
    EXECUTE FUNCTION public.author_entity_input_mark();
CREATE TRIGGER wikidata_extract_entity_input_delete
    AFTER DELETE ON public.wikidata_extract
    FOR EACH ROW WHEN (OLD.status = 'published')
    EXECUTE FUNCTION public.author_entity_input_mark();
