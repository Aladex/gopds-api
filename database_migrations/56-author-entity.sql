-- 56: author entities in the shadow (design E, phase E1). One entity per real
-- person or collective, built deterministically from the selected author
-- credits by a background pass an administrator starts from the «Сканирование»
-- card. Nothing a reader sees reads these tables yet: E2 links the read model
-- to them, E3 switches the surfaces.
--
--   * author_entity — the identity: a UUIDv7 made in Go (PostgreSQL 15 has
--     no uuidv7()), the kind, the name shown on the author page (one of its
--     credits' names, or pinned by hand) and merged_into. An ID is never
--     reused; a merged entity stays as a row for the rebuild and the audit,
--     never for a redirect.
--   * author_entity_assignment — a fingerprint (with its extractor version
--     and the person's ordinal in the credit) to its entity: D1, D2, the
--     patronymic typo merge. Automatic rows are rebuilt; manual ones (E5)
--     never are.
--   * author_entity_credit_assignment — a decision for one credit, over its
--     fingerprint's: a short-name book moved by evidence (D3), later a model
--     answer or a manual decision; entity_id NULL is "no entity". A credit
--     deleted with its book takes its decisions with it.
--   * author_entity_name — the name variants search will read: every credit
--     name and nickname (D4) of an entity with its books.
--   * author_entity_merge_log — every merge, undone by a split (E5).
--   * author_entity_build_run — the background pass: progress, resume, the
--     stored report.
--
-- author_entity_relation (pseudonym_of) is deferred to E5: only an
-- administrator writes it and nothing references it before then.
--
-- No backfill here: the first pass is started by an administrator.
--
-- Locks: the new foreign keys take SHARE ROW EXCLUSIVE on the credits and
-- the users, up front, NOWAIT, like 28-55. A scan writing credits makes the
-- file fail at once; the next start applies it whole.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.book_contributor_credit, public.auth_user IN SHARE ROW EXCLUSIVE MODE NOWAIT;

CREATE TABLE public.author_entity (
    id           uuid PRIMARY KEY,
    kind         text NOT NULL,
    display_name text NOT NULL,
    sort_name    text NOT NULL,
    search_key   text NOT NULL,
    name_pinned  boolean NOT NULL DEFAULT false,
    merged_into  uuid REFERENCES public.author_entity (id) ON DELETE RESTRICT,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT author_entity_kind_check CHECK (kind IN ('person', 'collective')),
    CONSTRAINT author_entity_name_check CHECK (display_name <> '' AND search_key <> ''),
    CONSTRAINT author_entity_merged_check CHECK (merged_into IS DISTINCT FROM id)
);

CREATE INDEX author_entity_merged_into_idx ON public.author_entity (merged_into) WHERE merged_into IS NOT NULL;

CREATE TRIGGER author_entity_updated_at
    BEFORE UPDATE ON public.author_entity
    FOR EACH ROW EXECUTE FUNCTION public.update_updated_at_column();

CREATE TABLE public.author_entity_assignment (
    source_fingerprint bytea    NOT NULL,
    extractor_version  text     NOT NULL,
    ordinal            smallint NOT NULL DEFAULT 0,
    entity_id          uuid     NOT NULL REFERENCES public.author_entity (id) ON DELETE RESTRICT,
    method             text     NOT NULL,
    rule               text     NOT NULL,
    -- The model's report an assignment of method model cites (E4).
    evidence_report_id bigint,
    created_by_user_id bigint REFERENCES public.auth_user (id) ON DELETE SET NULL,
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT author_entity_assignment_pkey PRIMARY KEY (source_fingerprint, extractor_version, ordinal),
    CONSTRAINT author_entity_assignment_fingerprint_check CHECK (octet_length(source_fingerprint) = 32),
    CONSTRAINT author_entity_assignment_ordinal_check CHECK (ordinal >= 0),
    CONSTRAINT author_entity_assignment_method_check CHECK (method IN ('deterministic', 'model', 'manual')),
    CONSTRAINT author_entity_assignment_rule_check CHECK (rule ~ '^[A-Za-z0-9:+_]{1,64}$')
);

CREATE INDEX author_entity_assignment_entity_idx ON public.author_entity_assignment (entity_id);

CREATE TABLE public.author_entity_credit_assignment (
    credit_id          bigint   NOT NULL REFERENCES public.book_contributor_credit (id) ON DELETE CASCADE,
    ordinal            smallint NOT NULL DEFAULT 0,
    entity_id          uuid REFERENCES public.author_entity (id) ON DELETE RESTRICT,
    method             text     NOT NULL,
    rule               text     NOT NULL,
    evidence_report_id bigint,
    created_by_user_id bigint REFERENCES public.auth_user (id) ON DELETE SET NULL,
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT author_entity_credit_assignment_pkey PRIMARY KEY (credit_id, ordinal),
    CONSTRAINT author_entity_credit_assignment_ordinal_check CHECK (ordinal >= 0),
    CONSTRAINT author_entity_credit_assignment_method_check CHECK (method IN ('evidence', 'model', 'manual')),
    CONSTRAINT author_entity_credit_assignment_rule_check CHECK (rule ~ '^[A-Za-z0-9:+_]{1,64}$'),
    -- Only a person's decision may say "no entity" for a credit.
    CONSTRAINT author_entity_credit_assignment_none_check CHECK (entity_id IS NOT NULL OR method = 'manual')
);

CREATE INDEX author_entity_credit_assignment_entity_idx ON public.author_entity_credit_assignment (entity_id);

CREATE TABLE public.author_entity_name (
    entity_id  uuid    NOT NULL REFERENCES public.author_entity (id) ON DELETE CASCADE,
    search_key text    NOT NULL,
    books      integer NOT NULL,
    CONSTRAINT author_entity_name_pkey PRIMARY KEY (entity_id, search_key),
    CONSTRAINT author_entity_name_search_key_check CHECK (search_key <> ''),
    CONSTRAINT author_entity_name_books_check CHECK (books >= 0)
);

CREATE TABLE public.author_entity_merge_log (
    id         bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    from_id    uuid NOT NULL REFERENCES public.author_entity (id) ON DELETE RESTRICT,
    to_id      uuid NOT NULL REFERENCES public.author_entity (id) ON DELETE RESTRICT,
    method     text NOT NULL,
    -- Counts and IDs only: the build run, the shared books.
    evidence   jsonb NOT NULL DEFAULT '{}'::jsonb,
    at         timestamptz NOT NULL DEFAULT now(),
    by_user_id bigint REFERENCES public.auth_user (id) ON DELETE SET NULL,
    CONSTRAINT author_entity_merge_log_method_check CHECK (method IN ('deterministic', 'model', 'manual')),
    CONSTRAINT author_entity_merge_log_distinct_check CHECK (from_id <> to_id),
    CONSTRAINT author_entity_merge_log_evidence_check CHECK (jsonb_typeof(evidence) = 'object')
);

CREATE INDEX author_entity_merge_log_from_idx ON public.author_entity_merge_log (from_id);

CREATE TABLE public.author_entity_build_run (
    id                 bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    status             text NOT NULL DEFAULT 'pending',
    stage              text,
    units_total        integer NOT NULL DEFAULT 0,
    units_done         integer NOT NULL DEFAULT 0,
    units_skipped      integer NOT NULL DEFAULT 0,
    changes            bigint  NOT NULL DEFAULT 0,
    report             jsonb,
    last_error_class   text,
    lease_owner        uuid,
    lease_expires_at   timestamptz,
    created_by_user_id bigint REFERENCES public.auth_user (id) ON DELETE SET NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    started_at         timestamptz,
    finished_at        timestamptz,
    CONSTRAINT author_entity_build_run_status_check
        CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    CONSTRAINT author_entity_build_run_stage_check
        CHECK (stage IS NULL OR stage IN ('plan', 'apply', 'report')),
    CONSTRAINT author_entity_build_run_counters_check
        CHECK (units_total >= 0 AND units_done >= 0 AND units_skipped >= 0 AND changes >= 0),
    CONSTRAINT author_entity_build_run_error_class_check
        CHECK (last_error_class IS NULL OR last_error_class ~ '^[a-z][a-z0-9_]{0,63}$'),
    CONSTRAINT author_entity_build_run_finished_check
        CHECK ((status IN ('completed', 'failed')) = (finished_at IS NOT NULL)),
    CONSTRAINT author_entity_build_run_report_check
        CHECK (status <> 'completed' OR jsonb_typeof(report) = 'object'),
    CONSTRAINT author_entity_build_run_lease_check
        CHECK ((lease_owner IS NULL) = (lease_expires_at IS NULL))
);

-- At most one pass at a time.
CREATE UNIQUE INDEX author_entity_build_run_one_active
    ON public.author_entity_build_run ((true))
    WHERE status IN ('pending', 'running');

CREATE TRIGGER author_entity_build_run_updated_at
    BEFORE UPDATE ON public.author_entity_build_run
    FOR EACH ROW EXECUTE FUNCTION public.update_updated_at_column();
