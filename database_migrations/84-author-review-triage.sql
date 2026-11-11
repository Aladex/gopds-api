-- 84: review triage (design/review-triage). The open review queue is decided
-- in admin-triggered batches by a versioned rule set, and each batch can be
-- undone. Undo needs a manual override that can stop applying without being
-- deleted, and the batches need a log.
--
--   * contributor_manual_override: revoked_at and revoked_by_user_id. A
--     revoked override is history; it selects nothing, and the newest
--     unrevoked override of a scope is the active one. The two columns are the
--     only change the immutability trigger lets through, and a revoke happens
--     once: it is never taken back or moved (contributor_manual_override_
--     revoke_once). The layer deletion of a book still removes its overrides.
--   * book_contributor_credit_selection_check: the body of migration 25, plus
--     a refusal of any selection made through a revoked override.
--   * author_review_triage_batch: one dry run of the rule set and what became
--     of it (planned, applied, undone). At most one batch plans, applies or
--     undoes at a time.
--   * author_review_triage_item: one row per open review item the dry run saw:
--     the step and the planned decision, or why it was left or excluded, and
--     then the outcome of the apply and of an undo. No foreign key reaches the
--     author layer: deleting a book removes its review items and overrides,
--     and the log must never block that.
--
-- No row is rewritten: the new columns are NULL, and their check is
-- satisfied by every existing override.
--
-- Locks: contributor_manual_override is taken up front, NOWAIT, like 28-83. A
-- reviewer or a resolver holding it makes the file fail at once; startup
-- retries it whole with backoff until its deadline. Replacing the selection
-- check function compiles its body, which reads the row type of
-- contributor_normalization_result (ACCESS SHARE): no writer of the results
-- conflicts with that. The triage tables are new.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.contributor_manual_override IN ACCESS EXCLUSIVE MODE NOWAIT;

ALTER TABLE public.contributor_manual_override
    ADD COLUMN revoked_at timestamptz,
    ADD COLUMN revoked_by_user_id bigint,
    ADD CONSTRAINT contributor_manual_override_revoke_check
        CHECK ((revoked_at IS NULL) = (revoked_by_user_id IS NULL)
               AND (revoked_by_user_id IS NULL OR revoked_by_user_id > 0));

DROP TRIGGER contributor_manual_override_immutable ON public.contributor_manual_override;
CREATE TRIGGER contributor_manual_override_immutable
    BEFORE UPDATE OR DELETE ON public.contributor_manual_override
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation('revoked_at', 'revoked_by_user_id');

-- A revoke is final: once revoked_at is set, no UPDATE of the row passes,
-- not even one that would clear or move it.
CREATE FUNCTION public.contributor_manual_override_revoke_once() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.revoked_at IS NOT NULL THEN
        RAISE EXCEPTION 'contributor_manual_override: override % is already revoked', OLD.id
            USING ERRCODE = 'restrict_violation';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER contributor_manual_override_revoke_once
    BEFORE UPDATE ON public.contributor_manual_override
    FOR EACH ROW EXECUTE FUNCTION public.contributor_manual_override_revoke_once();

-- The selection consistency trigger of migration 25, unchanged except for the
-- revoked override check in the override branch.
CREATE OR REPLACE FUNCTION public.book_contributor_credit_selection_check() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    res public.contributor_normalization_result%ROWTYPE;
    ovr public.contributor_manual_override%ROWTYPE;
    credit_extractor text;
BEGIN
    IF NEW.result_id IS NULL THEN
        RETURN NULL;
    END IF;
    SELECT * INTO res FROM public.contributor_normalization_result WHERE id = NEW.result_id;

    IF NEW.state = 'invalid' AND res.status <> 'invalid' THEN
        RAISE EXCEPTION 'book_contributor_credit_selection: an invalid credit must rest on an invalid result'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.state = 'selected' AND res.status = 'invalid' THEN
        RAISE EXCEPTION 'book_contributor_credit_selection: an invalid result cannot be selected'
            USING ERRCODE = 'check_violation';
    END IF;

    -- An automatic result is about one normalization input: the credit's own
    -- extractor version, not merely the same fingerprint.
    IF res.method <> 'manual' THEN
        SELECT s.extractor_version INTO credit_extractor
        FROM public.book_contributor_credit c
        JOIN public.book_metadata_snapshot s ON s.id = c.snapshot_id
        WHERE c.id = NEW.credit_id;
        IF credit_extractor IS DISTINCT FROM res.extractor_version THEN
            RAISE EXCEPTION 'book_contributor_credit_selection: result extractor version differs from the credit''s snapshot'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF NEW.basis = 'automatic' THEN
        IF res.method = 'manual' THEN
            RAISE EXCEPTION 'book_contributor_credit_selection: a manual result is selected through its override'
                USING ERRCODE = 'check_violation';
        END IF;
        -- Checked on its own first: the cast below must never see anything
        -- but a version number.
        IF NEW.policy_version !~ '^[1-9][0-9]{0,8}$' THEN
            RAISE EXCEPTION 'book_contributor_credit_selection: an automatic selection names an integer policy version'
                USING ERRCODE = 'check_violation';
        END IF;
        IF NOT EXISTS (
            SELECT 1 FROM public.author_acceptance_class a
            WHERE a.decision_class = res.decision_class
              AND a.script = res.script
              AND a.config_version = res.normalizer_version
              AND a.policy_version::int <= NEW.policy_version::int) THEN
            RAISE EXCEPTION 'book_contributor_credit_selection: decision class is not registered in the acceptance policy'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.basis IS NOT NULL THEN
        SELECT * INTO ovr FROM public.contributor_manual_override WHERE id = NEW.override_id;
        IF ovr.revoked_at IS NOT NULL THEN
            RAISE EXCEPTION 'book_contributor_credit_selection: a revoked override selects nothing'
                USING ERRCODE = 'check_violation';
        END IF;
        IF NEW.basis = 'credit_override' AND ovr.scope_credit_id IS DISTINCT FROM NEW.credit_id THEN
            RAISE EXCEPTION 'book_contributor_credit_selection: a credit override applies only to its own credit'
                USING ERRCODE = 'check_violation';
        END IF;
        IF NEW.basis = 'fingerprint_override' AND ovr.scope_fingerprint IS NULL THEN
            RAISE EXCEPTION 'book_contributor_credit_selection: basis fingerprint_override needs a fingerprint-scoped override'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN NULL;
END
$$;

-- One dry run of a triage rule version and its life: planning (the worker
-- builds the plan), planned, applying, applied, undoing (back to applied when
-- the undo is through), failed, or superseded by a newer plan.
CREATE TABLE public.author_review_triage_batch (
    id                         bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    rule_version               text        NOT NULL,
    status                     text        NOT NULL DEFAULT 'planning',
    created_by_user_id         bigint      NOT NULL,
    created_at                 timestamptz NOT NULL DEFAULT now(),
    planned_at                 timestamptz,
    apply_requested_by_user_id bigint,
    apply_requested_at         timestamptz,
    applied_steps              smallint[]  NOT NULL DEFAULT '{}',
    -- The audit log the apply was approved under (authortriage.AuditLog()).
    audit                      jsonb,
    undo_requested_by_user_id  bigint,
    undo_requested_at          timestamptz,
    counts                     jsonb,
    items_total                integer     NOT NULL DEFAULT 0,
    items_done                 integer     NOT NULL DEFAULT 0,
    finished_at                timestamptz,
    failure                    text,
    lease_owner                uuid,
    lease_until                timestamptz,

    CONSTRAINT author_review_triage_batch_status_check
        CHECK (status IN ('planning', 'planned', 'applying', 'applied', 'undoing', 'failed', 'superseded')),
    CONSTRAINT author_review_triage_batch_actor_check
        CHECK (created_by_user_id > 0)
);

-- One batch works at a time: the worker takes it, and a second dry run, apply
-- or undo waits for it to settle.
CREATE UNIQUE INDEX author_review_triage_batch_one_active
    ON public.author_review_triage_batch ((true))
    WHERE status IN ('planning', 'applying', 'undoing');

CREATE TRIGGER author_review_triage_batch_immutable
    BEFORE UPDATE OR DELETE ON public.author_review_triage_batch
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation(
        'status', 'planned_at', 'apply_requested_by_user_id', 'apply_requested_at', 'applied_steps', 'audit',
        'undo_requested_by_user_id', 'undo_requested_at', 'counts', 'items_total', 'items_done', 'finished_at',
        'failure', 'lease_owner', 'lease_until');

-- One open review item as a batch saw it. The plan (step, rule, action,
-- planned, evidence, exclusion) is fixed when the dry run stores it; the
-- outcome of the apply and of an undo is written later.
CREATE TABLE public.author_review_triage_item (
    id                 bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    batch_id           bigint      NOT NULL
        REFERENCES public.author_review_triage_batch (id) ON DELETE RESTRICT,
    review_item_id     bigint      NOT NULL,
    source_fingerprint bytea       NOT NULL,
    decision_class     text        NOT NULL,
    credits            integer     NOT NULL,
    proposal_result_id bigint,
    step               smallint,
    rule               text,
    action             text,
    planned            jsonb,
    evidence           jsonb,
    exclusion          text,
    outcome            text        NOT NULL,
    result_id          bigint,
    override_id        bigint,
    sqlstate           text,
    decided_at         timestamptz,
    undo_requested_at  timestamptz,
    revoked_at         timestamptz,
    revoked_by_user_id bigint,
    undo_outcome       text,

    CONSTRAINT author_review_triage_item_fingerprint_check
        CHECK (octet_length(source_fingerprint) = 32),
    CONSTRAINT author_review_triage_item_step_check
        CHECK (step BETWEEN 1 AND 8),
    CONSTRAINT author_review_triage_item_action_check
        CHECK (action IN ('accepted', 'edited', 'classified')),
    CONSTRAINT author_review_triage_item_outcome_check
        CHECK (outcome IN ('planned', 'left', 'excluded', 'not_audited', 'applied', 'skipped_changed', 'conflict',
                           'failed')),
    CONSTRAINT author_review_triage_item_undo_outcome_check
        CHECK (undo_outcome IN ('restored', 'superseded', 'blocked', 'gone', 'failed')),
    CONSTRAINT author_review_triage_item_step_action_check
        CHECK ((step IS NULL) = (action IS NULL)),
    CONSTRAINT author_review_triage_item_exclusion_check
        CHECK ((exclusion IS NOT NULL) = (outcome = 'excluded')),
    CONSTRAINT author_review_triage_item_batch_review_item_key
        UNIQUE (batch_id, review_item_id)
);

CREATE INDEX author_review_triage_item_batch_outcome_idx
    ON public.author_review_triage_item (batch_id, outcome, step, id);

-- The next dry run asks whether a fingerprint was undone before.
CREATE INDEX author_review_triage_item_revoked_fingerprint_idx
    ON public.author_review_triage_item (source_fingerprint)
    WHERE revoked_at IS NOT NULL;

CREATE TRIGGER author_review_triage_item_immutable
    BEFORE UPDATE OR DELETE ON public.author_review_triage_item
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation(
        'outcome', 'result_id', 'override_id', 'sqlstate', 'decided_at', 'undo_requested_at', 'revoked_at',
        'revoked_by_user_id', 'undo_outcome');
