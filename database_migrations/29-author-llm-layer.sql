-- The LLM author layer (design K, phase 2): the state of the in-service LLM
-- runs and of the synchronous worker that calls the configured OpenAI-compatible
-- endpoint.
--
--   author_llm_config / author_llm_participant — the immutable configuration
--       identity (endpoint without key, four participants, prompt, schema and
--       versions); its hash is the configuration version.
--   author_llm_run — eval / resolve / swap runs, one active at a time.
--   author_llm_job — one job per participant and input: a review fingerprint
--       or a frozen eval item.
--   author_llm_call — one HTTP request (the leased unit; a batch carries up
--       to 20 jobs), with its reserved and settled tokens.
--   author_llm_attempt — one job's part of one call: its parsed reply and
--       validator class. Immutable once closed.
--   author_llm_token_tally — signed token deltas, maintained by triggers on
--       the calls, read by the budget guard.
--   author_llm_provider_state — per participant: AIMD concurrency, pauses,
--       fingerprint check bookkeeping. Shared by every replica.
--   author_llm_context — the transient book excerpt.
--   author_llm_eval_set / author_llm_eval_item / author_llm_eval_report —
--       the frozen eval sets and the immutable reports.
--   author_llm_verdict — the comparison of the production pair on one input.
--   author_llm_canary_run — the canary set checks.
--
-- And author_acceptance_class gets the source llm_eval: a pair registered by
-- the service from an eval report (see the end of the file).
--
-- Locks: the only existing table this file alters is author_acceptance_class;
-- the foreign keys onto auth_user and contributor_normalization_result take a
-- SHARE ROW EXCLUSIVE lock on those. All three are taken up front, NOWAIT, as
-- in migration 28: acquiring them never waits. A conflict on any of them fails
-- the file at once; a lock already taken by the first statement is released
-- by the rollback, and the next start retries the file whole. While the file
-- runs, readers of the acceptance table wait for it as well (it is small).

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.author_acceptance_class IN ACCESS EXCLUSIVE MODE NOWAIT;
LOCK TABLE public.auth_user, public.contributor_normalization_result IN SHARE ROW EXCLUSIVE MODE NOWAIT;

-- The configuration identity. version is 'llm1-' and the hex SHA-256 of the
-- canonical identity JSON (llmreq.Identity); identity is that JSON. The base
-- URL is stored without user info, query or fragment, so no key can ride in
-- it; the key itself is never stored.
CREATE TABLE public.author_llm_config (
    version           TEXT PRIMARY KEY,
    base_url          TEXT NOT NULL,
    identity          JSONB NOT NULL,
    prompt_sha256     TEXT NOT NULL,
    schema_sha256     TEXT NOT NULL,
    validator_version TEXT NOT NULL,
    tokenizer_version TEXT NOT NULL,
    context_version   TEXT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT author_llm_config_version_check CHECK (version ~ '^llm1-[0-9a-f]{64}$'),
    CONSTRAINT author_llm_config_base_url_check
        CHECK (base_url ~ '^https?://[^/@?#[:space:]]+(/[^?#@[:space:]]*)?$'),
    CONSTRAINT author_llm_config_identity_check CHECK (jsonb_typeof(identity) = 'object'),
    CONSTRAINT author_llm_config_digests_check
        CHECK (prompt_sha256 ~ '^[0-9a-f]{64}$' AND schema_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT author_llm_config_versions_check
        CHECK (validator_version ~ '^[^[:space:]](.*[^[:space:]])?$'
               AND tokenizer_version ~ '^[^[:space:]](.*[^[:space:]])?$'
               AND context_version ~ '^[^[:space:]](.*[^[:space:]])?$')
);

CREATE TRIGGER author_llm_config_immutable
    BEFORE UPDATE OR DELETE ON public.author_llm_config
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation();

-- The four participants of a configuration.
CREATE TABLE public.author_llm_participant (
    config_version TEXT NOT NULL REFERENCES public.author_llm_config (version) ON DELETE RESTRICT,
    slot           TEXT NOT NULL,
    model          TEXT NOT NULL,
    params         JSONB NOT NULL DEFAULT '{}'::jsonb,

    PRIMARY KEY (config_version, slot),
    CONSTRAINT author_llm_participant_model_key UNIQUE (config_version, model),
    CONSTRAINT author_llm_participant_slot_check
        CHECK (slot IN ('production_a', 'production_b', 'judge_a', 'judge_b')),
    CONSTRAINT author_llm_participant_model_check CHECK (model ~ '^[^[:space:]](.*[^[:space:]])?$'),
    CONSTRAINT author_llm_participant_params_check CHECK (jsonb_typeof(params) = 'object')
);

CREATE TRIGGER author_llm_participant_immutable
    BEFORE UPDATE OR DELETE ON public.author_llm_participant
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation();

-- The endpoint's own state, shared by every configuration that calls it: an
-- exhausted quota (402) is the endpoint's and its key's, not one
-- configuration's. Keyed by the normalized base URL.
CREATE TABLE public.author_llm_endpoint_state (
    base_url      TEXT PRIMARY KEY,
    paused_reason TEXT,
    paused_at     TIMESTAMPTZ,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT author_llm_endpoint_state_pause_check
        CHECK ((paused_reason IS NULL) = (paused_at IS NULL)
               AND (paused_reason IS NULL OR paused_reason = 'quota_exhausted'))
);

CREATE TRIGGER author_llm_endpoint_state_updated_at
    BEFORE UPDATE ON public.author_llm_endpoint_state
    FOR EACH ROW EXECUTE FUNCTION public.update_updated_at_column();

-- A run. eval measures a configuration on the frozen sets; resolve works the
-- review queue; swap checks swapped names among the selected. One active run
-- (pending, running or paused) at a time, independent of the metadata runs.
-- What a run learned or inherited from its eval report rides on it:
-- expected_models (slot -> the model ids V0 accepts; NULL while learning)
-- and reference (slot -> {"prompt_tokens": n, "reasoning_floor": n}, the
-- fingerprint check; NULL means no check for that run).
CREATE TABLE public.author_llm_run (
    id                 BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    kind               TEXT NOT NULL,
    mode               TEXT NOT NULL,
    status             TEXT NOT NULL DEFAULT 'pending',
    config_version     TEXT NOT NULL REFERENCES public.author_llm_config (version) ON DELETE RESTRICT,
    stage              TEXT,
    estimate_tokens    BIGINT NOT NULL DEFAULT 0,
    expected_models    JSONB,
    reference          JSONB,
    paused_reason      TEXT,
    last_error_class   TEXT,
    created_by_user_id BIGINT REFERENCES public.auth_user (id) ON DELETE SET NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at         TIMESTAMPTZ,
    finished_at        TIMESTAMPTZ,

    CONSTRAINT author_llm_run_kind_check CHECK (kind IN ('eval', 'resolve', 'swap')),
    CONSTRAINT author_llm_run_mode_check
        CHECK (CASE kind WHEN 'eval' THEN mode IN ('dev', 'pilot', 'full') ELSE mode IN ('pilot', 'full') END),
    CONSTRAINT author_llm_run_status_check
        CHECK (status IN ('pending', 'running', 'paused', 'completed', 'failed_systemic')),
    CONSTRAINT author_llm_run_stage_check CHECK (stage IS NULL OR stage ~ '^[a-z][a-z_]{0,31}$'),
    CONSTRAINT author_llm_run_estimate_check CHECK (estimate_tokens >= 0),
    CONSTRAINT author_llm_run_expected_check
        CHECK (expected_models IS NULL OR jsonb_typeof(expected_models) = 'object'),
    CONSTRAINT author_llm_run_reference_check CHECK (reference IS NULL OR jsonb_typeof(reference) = 'object'),
    -- A paused run says why; nothing else carries a pause reason.
    CONSTRAINT author_llm_run_paused_check
        CHECK ((status = 'paused') = (paused_reason IS NOT NULL)
               AND (paused_reason IS NULL OR paused_reason IN ('admin', 'budget'))),
    CONSTRAINT author_llm_run_error_class_check
        CHECK (last_error_class IS NULL OR last_error_class ~ '^[a-z][a-z0-9_]{0,63}$'),
    CONSTRAINT author_llm_run_finished_check
        CHECK ((status IN ('completed', 'failed_systemic')) = (finished_at IS NOT NULL)),
    -- Only an eval run may learn. A production run (resolve, swap) carries
    -- what its eval report measured for the production pair: the model ids
    -- V0 accepts and the fingerprint references the checks compare with.
    CONSTRAINT author_llm_run_evidence_check
        CHECK (kind = 'eval'
               OR coalesce(jsonb_typeof(expected_models -> 'production_a') = 'array'
                   AND jsonb_array_length(expected_models -> 'production_a') > 0
                   AND jsonb_typeof(expected_models -> 'production_b') = 'array'
                   AND jsonb_array_length(expected_models -> 'production_b') > 0
                   AND jsonb_typeof(reference -> 'production_a' -> 'prompt_tokens') = 'number'
                   AND jsonb_typeof(reference -> 'production_b' -> 'prompt_tokens') = 'number', false))
);

CREATE UNIQUE INDEX author_llm_run_one_active
    ON public.author_llm_run ((true))
    WHERE status IN ('pending', 'running', 'paused');

CREATE TRIGGER author_llm_run_updated_at
    BEFORE UPDATE ON public.author_llm_run
    FOR EACH ROW EXECUTE FUNCTION public.update_updated_at_column();

-- A run keeps what it is about; its progress changes.
CREATE TRIGGER author_llm_run_identity
    BEFORE UPDATE OR DELETE ON public.author_llm_run
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation(
        'status', 'stage', 'estimate_tokens', 'expected_models', 'reference', 'paused_reason',
        'last_error_class', 'updated_at', 'started_at', 'finished_at');

-- A frozen eval set version: the SHA-256 of its canonical export.
CREATE TABLE public.author_llm_eval_set (
    set_version      TEXT PRIMARY KEY,
    sha256           BYTEA NOT NULL,
    items            INTEGER NOT NULL,
    created_by_run_id BIGINT NOT NULL REFERENCES public.author_llm_run (id) ON DELETE RESTRICT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT author_llm_eval_set_version_check CHECK (set_version ~ '^[a-z0-9][a-z0-9_.-]{0,63}$'),
    CONSTRAINT author_llm_eval_set_sha_check CHECK (octet_length(sha256) = 32),
    CONSTRAINT author_llm_eval_set_items_check CHECK (items >= 0)
);

CREATE TRIGGER author_llm_eval_set_immutable
    BEFORE UPDATE OR DELETE ON public.author_llm_eval_set
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation();

-- One frozen eval item: a copy of the input (the review queue empties once it
-- is resolved), its stratum and weight (the number of credits), the excerpt's
-- book and digest, and for the anchor set the expected answer.
CREATE TABLE public.author_llm_eval_item (
    id                 BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    set_version        TEXT NOT NULL REFERENCES public.author_llm_eval_set (set_version) ON DELETE RESTRICT,
    item_set           TEXT NOT NULL,
    stratum            TEXT NOT NULL,
    source_fingerprint BYTEA NOT NULL,
    extractor_version  TEXT NOT NULL,
    input              JSONB NOT NULL,
    weight             INTEGER NOT NULL,
    context_book_id    BIGINT,
    book_md5           TEXT,
    context_sha256     BYTEA,
    expected           JSONB,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT author_llm_eval_item_key UNIQUE (set_version, item_set, source_fingerprint, extractor_version),
    CONSTRAINT author_llm_eval_item_set_check CHECK (item_set IN ('holdout', 'dev', 'anchor', 'canary')),
    CONSTRAINT author_llm_eval_item_stratum_check CHECK (stratum ~ '^[a-z0-9][a-z0-9_.:-]{0,95}$'),
    CONSTRAINT author_llm_eval_item_fingerprint_check CHECK (octet_length(source_fingerprint) = 32),
    CONSTRAINT author_llm_eval_item_input_check CHECK (jsonb_typeof(input) = 'object'),
    CONSTRAINT author_llm_eval_item_weight_check CHECK (weight >= 1),
    CONSTRAINT author_llm_eval_item_context_check
        CHECK (num_nonnulls(context_book_id, book_md5) IN (0, 2)
               AND (context_sha256 IS NULL OR octet_length(context_sha256) = 32)),
    CONSTRAINT author_llm_eval_item_expected_check
        CHECK ((item_set = 'anchor') = (expected IS NOT NULL)
               AND (expected IS NULL OR jsonb_typeof(expected) = 'object'))
);

CREATE TRIGGER author_llm_eval_item_immutable
    BEFORE UPDATE OR DELETE ON public.author_llm_eval_item
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation();

-- The immutable result of an eval run.
CREATE TABLE public.author_llm_eval_report (
    id             BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    run_id         BIGINT NOT NULL REFERENCES public.author_llm_run (id) ON DELETE RESTRICT,
    config_version TEXT NOT NULL REFERENCES public.author_llm_config (version) ON DELETE RESTRICT,
    report         JSONB NOT NULL,
    report_sha256  BYTEA NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT author_llm_eval_report_run_key UNIQUE (run_id),
    -- Target of the acceptance registration's foreign key: a registration
    -- names the report together with its digest.
    CONSTRAINT author_llm_eval_report_id_sha_key UNIQUE (id, report_sha256),
    CONSTRAINT author_llm_eval_report_report_check CHECK (jsonb_typeof(report) = 'object'),
    CONSTRAINT author_llm_eval_report_sha_check CHECK (octet_length(report_sha256) = 32)
);

CREATE TRIGGER author_llm_eval_report_immutable
    BEFORE UPDATE OR DELETE ON public.author_llm_eval_report
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation();

-- A book excerpt (llmres.BuildContext), keyed by the book file and the reader
-- version. The text is transient: it is dropped (payload NULL, purged_at set)
-- once no verdict needs it, and the digest stays for the audit. No foreign key
-- onto the book: deleting a book must not wait on an excerpt.
CREATE TABLE public.author_llm_context (
    id              BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    book_id         BIGINT NOT NULL,
    book_md5        TEXT NOT NULL,
    context_version TEXT NOT NULL,
    -- SHA-256 of the credit display name the excerpt leaves out of its other
    -- credits: two authors of one book get two excerpts.
    exclude_sha256  BYTEA NOT NULL,
    payload         JSONB,
    sha256          BYTEA NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    purged_at       TIMESTAMPTZ,

    CONSTRAINT author_llm_context_key UNIQUE (book_id, book_md5, context_version, exclude_sha256),
    CONSTRAINT author_llm_context_exclude_check CHECK (octet_length(exclude_sha256) = 32),
    CONSTRAINT author_llm_context_book_check CHECK (book_id > 0 AND book_md5 <> ''),
    CONSTRAINT author_llm_context_sha_check CHECK (octet_length(sha256) = 32),
    CONSTRAINT author_llm_context_payload_check
        CHECK ((payload IS NULL) = (purged_at IS NOT NULL)
               AND (payload IS NULL OR jsonb_typeof(payload) = 'object'))
);

CREATE INDEX author_llm_context_sha_idx ON public.author_llm_context (sha256);

-- Only the purge changes an excerpt: the payload may go, never change.
CREATE FUNCTION public.author_llm_context_purge_only() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.payload IS NOT NULL AND NEW.payload IS DISTINCT FROM OLD.payload THEN
        RAISE EXCEPTION 'an excerpt''s text can only be purged, never changed'
            USING ERRCODE = 'restrict_violation';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER author_llm_context_identity
    BEFORE UPDATE OR DELETE ON public.author_llm_context
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation('payload', 'purged_at');

CREATE TRIGGER author_llm_context_purge
    BEFORE UPDATE ON public.author_llm_context
    FOR EACH ROW EXECUTE FUNCTION public.author_llm_context_purge_only();

-- One HTTP request to the endpoint: the leased unit of the worker. It is
-- written in the claim transaction, before the request is sent, with the
-- reserved tokens (an upper estimate); the settle closes it with the reported
-- usage. A call whose lease ran out is closed as abandoned by the next claim
-- and keeps its reserve as spent. A closed call does not change again.
CREATE TABLE public.author_llm_call (
    id                BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    config_version    TEXT NOT NULL,
    slot              TEXT NOT NULL,
    run_id            BIGINT NOT NULL REFERENCES public.author_llm_run (id) ON DELETE RESTRICT,
    kind              TEXT NOT NULL,
    items             SMALLINT NOT NULL,
    output_mode       TEXT NOT NULL,
    request_sha256    BYTEA,
    -- The hard bound of the request: at most prompt_bound_bytes bytes sent
    -- (every token covers at least one byte, plus a fixed template allowance)
    -- and at most output_ceiling completion tokens, which the request itself
    -- carries. reserved_tokens is that bound, held until the settle.
    prompt_bound_bytes INTEGER NOT NULL,
    output_ceiling    INTEGER NOT NULL,
    reserved_tokens   BIGINT NOT NULL,
    settled_tokens    BIGINT,
    -- A fingerprint check's sequence number among the participant's checks.
    check_seq         BIGINT,
    lease_owner       UUID,
    lease_expires_at  TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at       TIMESTAMPTZ,
    outcome           TEXT,
    error_class       TEXT,
    http_status       INTEGER,
    latency_ms        INTEGER,
    response_model    TEXT,
    usage             JSONB,
    prompt_tokens     BIGINT,
    completion_tokens BIGINT,
    reasoning_tokens  BIGINT,
    -- The reply as the endpoint sent it (content or joined tool arguments),
    -- capped; kept for the audit of an invalid reply.
    response_content  TEXT,

    CONSTRAINT author_llm_call_participant_fkey
        FOREIGN KEY (config_version, slot)
        REFERENCES public.author_llm_participant (config_version, slot) ON DELETE RESTRICT,
    CONSTRAINT author_llm_call_kind_check CHECK (kind IN ('jobs', 'fingerprint')),
    CONSTRAINT author_llm_call_items_check CHECK (items BETWEEN 1 AND 20),
    CONSTRAINT author_llm_call_mode_check CHECK (output_mode IN ('json_schema', 'json_object', 'tool')),
    CONSTRAINT author_llm_call_request_sha_check CHECK (request_sha256 IS NULL OR octet_length(request_sha256) = 32),
    CONSTRAINT author_llm_call_tokens_check
        CHECK (reserved_tokens > 0 AND (settled_tokens IS NULL OR settled_tokens >= 0)),
    CONSTRAINT author_llm_call_bound_check
        CHECK (prompt_bound_bytes > 0 AND output_ceiling > 0 AND reserved_tokens > output_ceiling),
    CONSTRAINT author_llm_call_check_seq_check CHECK ((kind = 'fingerprint') = (check_seq IS NOT NULL)),
    CONSTRAINT author_llm_call_outcome_check
        CHECK (outcome IS NULL
               OR outcome IN ('answered', 'http_error', 'timeout', 'transport_error', 'malformed', 'abandoned')),
    CONSTRAINT author_llm_call_error_class_check
        CHECK (error_class IS NULL OR error_class ~ '^[a-z][a-z0-9_]{0,63}$'),
    CONSTRAINT author_llm_call_content_check CHECK (response_content IS NULL OR length(response_content) <= 262144),
    -- Open: leased, nothing settled. Closed: an outcome, settled tokens, no
    -- lease.
    CONSTRAINT author_llm_call_open_check
        CHECK (CASE WHEN finished_at IS NULL
                    THEN outcome IS NULL AND settled_tokens IS NULL
                         AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL
                    ELSE outcome IS NOT NULL AND settled_tokens IS NOT NULL
                         AND lease_owner IS NULL AND lease_expires_at IS NULL AND finished_at >= created_at
               END)
);

CREATE INDEX author_llm_call_open_idx ON public.author_llm_call (config_version, slot)
    WHERE finished_at IS NULL;
CREATE INDEX author_llm_call_lease_idx ON public.author_llm_call (lease_expires_at)
    WHERE finished_at IS NULL;
CREATE INDEX author_llm_call_recent_idx ON public.author_llm_call (config_version, slot, id);
CREATE INDEX author_llm_call_run_idx ON public.author_llm_call (run_id);

CREATE TRIGGER author_llm_call_identity
    BEFORE UPDATE OR DELETE ON public.author_llm_call
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation(
        'request_sha256', 'settled_tokens', 'lease_owner', 'lease_expires_at', 'finished_at', 'outcome',
        'error_class', 'http_status', 'latency_ms', 'response_model', 'usage', 'prompt_tokens', 'completion_tokens',
        'reasoning_tokens', 'response_content');

CREATE TRIGGER author_llm_call_closed
    BEFORE UPDATE ON public.author_llm_call
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_closed_attempt();

-- What the calls spent, as signed deltas: a call counts its reserve while
-- open and its settled tokens once closed. Statement triggers append the
-- deltas in the writer's own transaction (migration 28's tally pattern), so the
-- budget guard sums a few rows instead of the call history and no writer
-- waits on another's lock; the worker folds them now and then.
CREATE TABLE public.author_llm_token_tally (
    run_id BIGINT NOT NULL,
    tokens BIGINT NOT NULL
);
CREATE INDEX author_llm_token_tally_run_idx ON public.author_llm_token_tally (run_id);

CREATE FUNCTION public.author_llm_token_tally_count() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO public.author_llm_token_tally (run_id, tokens)
        SELECT run_id, sum(coalesce(settled_tokens, reserved_tokens))
        FROM new_calls GROUP BY run_id;
    ELSE
        INSERT INTO public.author_llm_token_tally (run_id, tokens)
        SELECT run_id, sum(delta) FROM (
            SELECT run_id, coalesce(settled_tokens, reserved_tokens) AS delta FROM new_calls
            UNION ALL
            SELECT run_id, -coalesce(settled_tokens, reserved_tokens) FROM old_calls) changed
        GROUP BY run_id
        HAVING sum(delta) <> 0;
    END IF;
    RETURN NULL;
END
$$;

CREATE TRIGGER author_llm_token_tally_insert AFTER INSERT ON public.author_llm_call
    REFERENCING NEW TABLE AS new_calls
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_llm_token_tally_count();
CREATE TRIGGER author_llm_token_tally_update AFTER UPDATE ON public.author_llm_call
    REFERENCING OLD TABLE AS old_calls NEW TABLE AS new_calls
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_llm_token_tally_count();

-- One job: one participant's answer for one input. Review jobs name a source
-- fingerprint under an extractor version; eval jobs name a frozen eval item
-- and belong to their run. input is the stored llmreq.ItemInput (source fields,
-- script, flags); the request tokens are derived from it.
CREATE TABLE public.author_llm_job (
    id                 BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    config_version     TEXT NOT NULL,
    slot               TEXT NOT NULL,
    -- Every job belongs to a run: there is no run-less path to an answer.
    run_id             BIGINT NOT NULL REFERENCES public.author_llm_run (id) ON DELETE RESTRICT,
    purpose            TEXT NOT NULL,
    source_fingerprint BYTEA,
    extractor_version  TEXT,
    eval_item_id       BIGINT REFERENCES public.author_llm_eval_item (id) ON DELETE RESTRICT,
    arm                TEXT,
    repeat_no          SMALLINT NOT NULL DEFAULT 0,
    batch_size         SMALLINT NOT NULL DEFAULT 1,
    output_mode        TEXT NOT NULL DEFAULT 'json_schema',
    input              JSONB NOT NULL,
    with_context       BOOLEAN NOT NULL DEFAULT false,
    context_state      TEXT NOT NULL DEFAULT 'none',
    context_book_id    BIGINT,
    context_sha256     BYTEA,
    status             TEXT NOT NULL DEFAULT 'pending',
    call_id            BIGINT REFERENCES public.author_llm_call (id) ON DELETE RESTRICT,
    attempt_count      INTEGER NOT NULL DEFAULT 0,
    failure_count      INTEGER NOT NULL DEFAULT 0,
    invalid_count      INTEGER NOT NULL DEFAULT 0,
    retry_single       BOOLEAN NOT NULL DEFAULT false,
    next_attempt_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error_class   TEXT,
    answer_attempt_id  BIGINT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at        TIMESTAMPTZ,

    CONSTRAINT author_llm_job_participant_fkey
        FOREIGN KEY (config_version, slot)
        REFERENCES public.author_llm_participant (config_version, slot) ON DELETE RESTRICT,
    CONSTRAINT author_llm_job_purpose_check
        CHECK (purpose IN ('review', 'swap_check', 'eval_judge', 'eval_candidate', 'eval_repeat',
                           'eval_anchor', 'canary')),
    -- Review jobs are about a fingerprint; eval jobs about a frozen item of
    -- their run.
    CONSTRAINT author_llm_job_shape_check
        CHECK (CASE WHEN purpose IN ('review', 'swap_check')
                    THEN octet_length(source_fingerprint) = 32
                         AND extractor_version ~ '^[^[:space:]](.*[^[:space:]])?$'
                         AND eval_item_id IS NULL AND arm IS NULL AND repeat_no = 0
                    ELSE source_fingerprint IS NULL AND extractor_version IS NULL
                         AND eval_item_id IS NOT NULL
               END),
    CONSTRAINT author_llm_job_arm_check CHECK (arm IS NULL OR arm IN ('A', 'B')),
    CONSTRAINT author_llm_job_repeat_check CHECK (repeat_no >= 0),
    CONSTRAINT author_llm_job_batch_check CHECK (batch_size BETWEEN 1 AND 20),
    CONSTRAINT author_llm_job_mode_check CHECK (output_mode IN ('json_schema', 'json_object', 'tool')),
    CONSTRAINT author_llm_job_input_check CHECK (jsonb_typeof(input) = 'object'),
    CONSTRAINT author_llm_job_context_check
        CHECK (context_state IN ('none', 'attached', 'irrelevant', 'unavailable')
               AND (context_state = 'attached') = (context_sha256 IS NOT NULL)
               AND (context_state <> 'attached' OR context_book_id IS NOT NULL)
               AND (context_sha256 IS NULL OR octet_length(context_sha256) = 32)
               AND (with_context OR context_state = 'none')),
    CONSTRAINT author_llm_job_status_check
        CHECK (status IN ('pending', 'claimed', 'answered', 'invalid', 'failed')),
    CONSTRAINT author_llm_job_claimed_check CHECK ((status = 'claimed') = (call_id IS NOT NULL)),
    CONSTRAINT author_llm_job_answer_check CHECK ((status = 'answered') = (answer_attempt_id IS NOT NULL)),
    CONSTRAINT author_llm_job_finished_check
        CHECK ((status IN ('answered', 'invalid', 'failed')) = (finished_at IS NOT NULL)),
    CONSTRAINT author_llm_job_counts_check
        CHECK (attempt_count >= 0 AND failure_count >= 0 AND invalid_count >= 0),
    CONSTRAINT author_llm_job_error_class_check
        CHECK (last_error_class IS NULL OR last_error_class ~ '^[a-z][a-z0-9_]{0,63}$')
);

CREATE UNIQUE INDEX author_llm_job_review_key
    ON public.author_llm_job (source_fingerprint, extractor_version, purpose, config_version, slot)
    WHERE eval_item_id IS NULL;
CREATE UNIQUE INDEX author_llm_job_eval_key
    ON public.author_llm_job (run_id, eval_item_id, purpose, coalesce(arm, ''), repeat_no, batch_size,
                              output_mode, config_version, slot)
    WHERE eval_item_id IS NOT NULL;
CREATE INDEX author_llm_job_claim_idx
    ON public.author_llm_job (config_version, slot, next_attempt_at, id)
    WHERE status = 'pending';
CREATE INDEX author_llm_job_call_idx ON public.author_llm_job (call_id) WHERE call_id IS NOT NULL;
CREATE INDEX author_llm_job_run_idx ON public.author_llm_job (run_id, status);

-- A job's purpose belongs to its run's kind: review jobs to a resolve run,
-- swap checks to a swap run, eval purposes to an eval run. A production
-- answer can therefore only come under a run that carries its evidence.
CREATE FUNCTION public.author_llm_job_run_kind_check() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    run_kind text;
    wanted   text := CASE NEW.purpose WHEN 'review' THEN 'resolve' WHEN 'swap_check' THEN 'swap' ELSE 'eval' END;
BEGIN
    SELECT kind INTO run_kind FROM public.author_llm_run WHERE id = NEW.run_id;
    IF run_kind IS DISTINCT FROM wanted THEN
        RAISE EXCEPTION 'author_llm_job: purpose does not belong to the run''s kind'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER author_llm_job_run_kind
    BEFORE INSERT ON public.author_llm_job
    FOR EACH ROW EXECUTE FUNCTION public.author_llm_job_run_kind_check();

CREATE TRIGGER author_llm_job_updated_at
    BEFORE UPDATE ON public.author_llm_job
    FOR EACH ROW EXECUTE FUNCTION public.update_updated_at_column();

CREATE TRIGGER author_llm_job_identity
    BEFORE UPDATE OR DELETE ON public.author_llm_job
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation(
        'context_state', 'context_book_id', 'context_sha256', 'status', 'call_id', 'attempt_count',
        'failure_count', 'invalid_count', 'retry_single', 'next_attempt_at', 'last_error_class',
        'answer_attempt_id', 'updated_at', 'finished_at');

-- One job's part of one call. Written with the call, before the request;
-- closed by the settle with the item's outcome, its strictly parsed reply
-- (canonical form) and the validator class of a refused reply. Immutable once
-- closed.
CREATE TABLE public.author_llm_attempt (
    id               BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    job_id           BIGINT NOT NULL REFERENCES public.author_llm_job (id) ON DELETE RESTRICT,
    attempt_no       INTEGER NOT NULL,
    call_id          BIGINT NOT NULL REFERENCES public.author_llm_call (id) ON DELETE RESTRICT,
    item_key         TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at      TIMESTAMPTZ,
    outcome          TEXT,
    validator_class  TEXT,
    reply            JSONB,
    canonical_sha256 BYTEA,
    tier             TEXT,
    refused          BOOLEAN,

    CONSTRAINT author_llm_attempt_no_key UNIQUE (job_id, attempt_no),
    CONSTRAINT author_llm_attempt_item_key UNIQUE (call_id, item_key),
    CONSTRAINT author_llm_attempt_no_check CHECK (attempt_no >= 1),
    CONSTRAINT author_llm_attempt_item_check CHECK (item_key ~ '^[0-9]{1,2}$'),
    CONSTRAINT author_llm_attempt_outcome_check
        CHECK (outcome IS NULL OR outcome IN ('answered', 'invalid', 'model_mismatch', 'http_error', 'timeout',
                                              'transport_error', 'malformed', 'abandoned')),
    CONSTRAINT author_llm_attempt_validator_check
        CHECK (validator_class IS NULL OR validator_class ~ '^[a-z][a-z0-9_]{0,63}$'),
    CONSTRAINT author_llm_attempt_tier_check
        CHECK (tier IS NULL OR tier IN ('confirm', 'restructure', 'reclassify', 'case')),
    -- An answered attempt carries its reply and digest; a refusal has no tier.
    CONSTRAINT author_llm_attempt_answer_check
        CHECK (CASE WHEN outcome = 'answered'
                    THEN jsonb_typeof(reply) = 'object' AND octet_length(canonical_sha256) = 32
                         AND refused IS NOT NULL AND (tier IS NULL) = refused
                    ELSE canonical_sha256 IS NULL AND tier IS NULL AND refused IS NULL
               END),
    CONSTRAINT author_llm_attempt_finished_check
        CHECK ((finished_at IS NULL) = (outcome IS NULL) AND (finished_at IS NULL OR finished_at >= created_at))
);

CREATE INDEX author_llm_attempt_call_idx ON public.author_llm_attempt (call_id);

ALTER TABLE public.author_llm_job
    ADD CONSTRAINT author_llm_job_answer_fkey
        FOREIGN KEY (answer_attempt_id) REFERENCES public.author_llm_attempt (id) ON DELETE RESTRICT;

CREATE TRIGGER author_llm_attempt_identity
    BEFORE UPDATE OR DELETE ON public.author_llm_attempt
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation(
        'finished_at', 'outcome', 'validator_class', 'reply', 'canonical_sha256', 'tier', 'refused');

CREATE TRIGGER author_llm_attempt_closed
    BEFORE UPDATE ON public.author_llm_attempt
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_closed_attempt();

-- Per participant: the AIMD window, a throttle from Retry-After, the pause,
-- and the fingerprint check bookkeeping (the run whose start check passed, the
-- answers since the last check, when the last check passed). Every replica
-- reads and writes these under the claim lock.
CREATE TABLE public.author_llm_provider_state (
    config_version        TEXT NOT NULL,
    slot                  TEXT NOT NULL,
    concurrency           INTEGER NOT NULL,
    success_streak        INTEGER NOT NULL DEFAULT 0,
    throttled_until       TIMESTAMPTZ,
    paused_reason         TEXT,
    paused_at             TIMESTAMPTZ,
    answered_since_check  INTEGER NOT NULL DEFAULT 0,
    check_run_id          BIGINT REFERENCES public.author_llm_run (id) ON DELETE RESTRICT,
    -- When the last passed check was sent: it covers the answers finished
    -- before that instant, never the ones finished while it was in flight.
    last_check_passed_at  TIMESTAMPTZ,
    -- The participant's checks are numbered; at most one is in flight, and
    -- only the in-flight one's result is applied.
    check_seq             BIGINT NOT NULL DEFAULT 0,
    check_call_id         BIGINT REFERENCES public.author_llm_call (id) ON DELETE RESTRICT,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (config_version, slot),
    CONSTRAINT author_llm_provider_state_participant_fkey
        FOREIGN KEY (config_version, slot)
        REFERENCES public.author_llm_participant (config_version, slot) ON DELETE RESTRICT,
    CONSTRAINT author_llm_provider_state_concurrency_check CHECK (concurrency >= 1),
    CONSTRAINT author_llm_provider_state_counts_check CHECK (success_streak >= 0 AND answered_since_check >= 0),
    CONSTRAINT author_llm_provider_state_pause_check
        CHECK ((paused_reason IS NULL) = (paused_at IS NULL)
               AND (paused_reason IS NULL
                    OR paused_reason IN ('auth', 'model_mismatch', 'bad_request', 'drift', 'over_reserve')))
);

CREATE TRIGGER author_llm_provider_state_updated_at
    BEFORE UPDATE ON public.author_llm_provider_state
    FOR EACH ROW EXECUTE FUNCTION public.update_updated_at_column();

-- The production pair's comparison on one review input under one
-- configuration. Conditional (confirmed_at NULL) until the fingerprint checks
-- of both participants passed after its answers; a failed check marks it
-- held_drift and its jobs go back to the queue. One current verdict per input:
-- a held_drift verdict is history.
CREATE TABLE public.author_llm_verdict (
    id                 BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    config_version     TEXT NOT NULL REFERENCES public.author_llm_config (version) ON DELETE RESTRICT,
    run_id             BIGINT NOT NULL REFERENCES public.author_llm_run (id) ON DELETE RESTRICT,
    source_fingerprint BYTEA NOT NULL,
    extractor_version  TEXT NOT NULL,
    purpose            TEXT NOT NULL,
    verdict            TEXT NOT NULL,
    tier               TEXT,
    context_state      TEXT NOT NULL,
    job_a_id           BIGINT NOT NULL REFERENCES public.author_llm_job (id) ON DELETE RESTRICT,
    job_b_id           BIGINT NOT NULL REFERENCES public.author_llm_job (id) ON DELETE RESTRICT,
    attempt_a_id       BIGINT REFERENCES public.author_llm_attempt (id) ON DELETE RESTRICT,
    attempt_b_id       BIGINT REFERENCES public.author_llm_attempt (id) ON DELETE RESTRICT,
    answered_a_at      TIMESTAMPTZ,
    answered_b_at      TIMESTAMPTZ,
    confirmed_at       TIMESTAMPTZ,
    result_id          BIGINT REFERENCES public.contributor_normalization_result (id) ON DELETE RESTRICT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT author_llm_verdict_fingerprint_check CHECK (octet_length(source_fingerprint) = 32),
    CONSTRAINT author_llm_verdict_purpose_check CHECK (purpose IN ('review', 'swap_check')),
    CONSTRAINT author_llm_verdict_verdict_check
        CHECK (verdict IN ('agree', 'disagree', 'invalid', 'refused', 'failed', 'held_drift')),
    CONSTRAINT author_llm_verdict_tier_check
        CHECK ((verdict = 'agree') = (tier IS NOT NULL) OR verdict = 'held_drift'),
    CONSTRAINT author_llm_verdict_context_check
        CHECK (context_state IN ('none', 'attached', 'irrelevant', 'unavailable')),
    CONSTRAINT author_llm_verdict_confirmed_check CHECK (verdict <> 'held_drift' OR confirmed_at IS NULL)
);

CREATE UNIQUE INDEX author_llm_verdict_current
    ON public.author_llm_verdict (source_fingerprint, extractor_version, purpose, config_version)
    WHERE verdict <> 'held_drift';
CREATE INDEX author_llm_verdict_unconfirmed_idx ON public.author_llm_verdict (config_version)
    WHERE confirmed_at IS NULL AND verdict <> 'held_drift';

CREATE TRIGGER author_llm_verdict_identity
    BEFORE UPDATE OR DELETE ON public.author_llm_verdict
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation('verdict', 'confirmed_at', 'result_id');

-- One check of the canary set: deviations per participant, the reference
-- prompt tokens per participant, and the participants' agreement matrix.
CREATE TABLE public.author_llm_canary_run (
    id                      BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    config_version          TEXT NOT NULL REFERENCES public.author_llm_config (version) ON DELETE RESTRICT,
    run_id                  BIGINT REFERENCES public.author_llm_run (id) ON DELETE RESTRICT,
    deviations              JSONB NOT NULL,
    reference_prompt_tokens JSONB NOT NULL,
    agreement_matrix        JSONB NOT NULL,
    passed                  BOOLEAN NOT NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT author_llm_canary_run_json_check
        CHECK (jsonb_typeof(deviations) = 'object' AND jsonb_typeof(reference_prompt_tokens) = 'object'
               AND jsonb_typeof(agreement_matrix) = 'object')
);

CREATE TRIGGER author_llm_canary_run_immutable
    BEFORE UPDATE OR DELETE ON public.author_llm_canary_run
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation();

-- Registration by the service from an eval report (design K, decision R1).
-- Migration 25 made a registration a code change, backed by an evidence file in
-- the repository. In a self-hosted product the evidence is measured on the
-- installation's own data and models, so the service registers LLM pairs
-- itself, from the latest completed eval report of the configuration in force,
-- in the transaction that starts the resolve run: source llm_eval, the admin
-- who pressed the button, and the report by id and digest. Gate rules,
-- thresholds and the procedure stay in the code; the database contributes
-- only measurements. The shipped and admin sources are unchanged.
ALTER TABLE public.author_acceptance_class
    ADD COLUMN evidence_report_id BIGINT,
    ADD CONSTRAINT author_acceptance_class_report_fkey
        FOREIGN KEY (evidence_report_id, evidence_report_sha256)
        REFERENCES public.author_llm_eval_report (id, report_sha256) ON DELETE RESTRICT,
    DROP CONSTRAINT author_acceptance_class_source_check,
    ADD CONSTRAINT author_acceptance_class_source_check
        CHECK (source IN ('shipped', 'admin', 'llm_eval')),
    DROP CONSTRAINT author_acceptance_class_actor_check,
    ADD CONSTRAINT author_acceptance_class_actor_check
        CHECK (coalesce(CASE source
                   WHEN 'shipped' THEN registered_by_user_id IS NULL AND evidence_report_id IS NULL
                   WHEN 'admin' THEN registered_by_user_id > 0 AND evidence_report_id IS NULL
                   WHEN 'llm_eval' THEN registered_by_user_id > 0 AND evidence_report_id IS NOT NULL
                       AND evidence_ref = 'author_llm_eval_report/' || evidence_report_id || '.json'
                   ELSE true -- an unknown source is the source check's to refuse
               END, false));

COMMENT ON COLUMN public.author_acceptance_class.evidence_ref IS
    'Where the evidence report is: a repository path for shipped and admin registrations (migration 25), '
    'author_llm_eval_report/<id>.json for a registration made by the service from an eval report.';
COMMENT ON COLUMN public.author_acceptance_class.evidence_report_id IS
    'The eval report an llm_eval registration rests on; NULL for every other source.';
