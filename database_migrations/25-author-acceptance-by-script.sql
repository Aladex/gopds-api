-- Automatic acceptance per (decision class, script), shipped with the code.
--
-- A registration names its script as well as its decision class: a class that
-- reads well in Cyrillic says nothing about the same class in another script.
-- Registrations ship with the code: each is backed by a frozen evidence report
-- checked into the repository (evidence_ref), whose SHA-256 is the
-- registration's evidence hash. Adding a pair later is a code change — a new
-- evidence file and a migration adding it under the next policy version — not
-- an administrator's action.
--
-- Policy versions are cumulative integers. Version 1 is the empty policy
-- (authornorm.BasePolicyVersion); each shipped change adds its pairs under the
-- next version, and the policy of version N is every pair registered in
-- versions 1..N for the normalizer configuration in force. A selection names
-- the version it was made under, so an older selection keeps meaning what its
-- version said.

SET LOCAL lock_timeout = '5s';

-- Rows registered before this migration carry no script, and guessing one
-- would switch on automatic selection nobody looked at. There are none in any
-- environment; if there ever were, stop here and let them be registered again.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.author_acceptance_class) THEN
        RAISE EXCEPTION 'author_acceptance_class has rows without a script; register them again after this migration'
            USING ERRCODE = 'restrict_violation';
    END IF;
END
$$;

-- The closed set of scripts the normalizer emits: every ISO 15924 code of a
-- Unicode script, and mixed (authornorm.Scripts). A test compares the two.
CREATE FUNCTION public.author_metadata_scripts() RETURNS text[]
    LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT ARRAY[
         'Adlm', 'Aghb', 'Ahom', 'Arab', 'Armi', 'Armn', 'Avst', 'Bali', 'Bamu', 'Bass', 'Batk',
         'Beng', 'Bhks', 'Bopo', 'Brah', 'Brai', 'Bugi', 'Buhd', 'Cakm', 'Cans', 'Cari', 'Cham',
         'Cher', 'Chrs', 'Copt', 'Cpmn', 'Cprt', 'Cyrl', 'Deva', 'Diak', 'Dogr', 'Dsrt', 'Dupl',
         'Egyp', 'Elba', 'Elym', 'Ethi', 'Geor', 'Glag', 'Gong', 'Gonm', 'Goth', 'Gran', 'Grek',
         'Gujr', 'Guru', 'Hang', 'Hani', 'Hano', 'Hatr', 'Hebr', 'Hira', 'Hluw', 'Hmng', 'Hmnp',
         'Hung', 'Ital', 'Java', 'Kali', 'Kana', 'Kawi', 'Khar', 'Khmr', 'Khoj', 'Kits', 'Knda',
         'Kthi', 'Lana', 'Laoo', 'Latn', 'Lepc', 'Limb', 'Lina', 'Linb', 'Lisu', 'Lyci', 'Lydi',
         'Mahj', 'Maka', 'Mand', 'Mani', 'Marc', 'Medf', 'Mend', 'Merc', 'Mero', 'Mlym', 'Modi',
         'Mong', 'Mroo', 'Mtei', 'Mult', 'Mymr', 'Nagm', 'Nand', 'Narb', 'Nbat', 'Newa', 'Nkoo',
         'Nshu', 'Ogam', 'Olck', 'Orkh', 'Orya', 'Osge', 'Osma', 'Ougr', 'Palm', 'Pauc', 'Perm',
         'Phag', 'Phli', 'Phlp', 'Phnx', 'Plrd', 'Prti', 'Rjng', 'Rohg', 'Runr', 'Samr', 'Sarb',
         'Saur', 'Sgnw', 'Shaw', 'Shrd', 'Sidd', 'Sind', 'Sinh', 'Sogd', 'Sogo', 'Sora', 'Soyo',
         'Sund', 'Sylo', 'Syrc', 'Tagb', 'Takr', 'Tale', 'Talu', 'Taml', 'Tang', 'Tavt', 'Telu',
         'Tfng', 'Tglg', 'Thaa', 'Thai', 'Tibt', 'Tirh', 'Tnsa', 'Toto', 'Ugar', 'Vaii', 'Vith',
         'Wara', 'Wcho', 'Xpeo', 'Xsux', 'Yezi', 'Yiii', 'Zanb', 'Zinh', 'Zyyy', 'mixed'
    ]::text[]
$$;

-- The registration: keyed by script, numbered as a cumulative integer
-- version, citing its evidence report. A shipped registration has no actor:
-- nobody registered it at run time, so registered_by_user_id is NULL and
-- source says where it came from; an administrator's registration, should one
-- ever exist, names its actor. The immutability trigger of migration 24 stays.
ALTER TABLE public.author_acceptance_class
    ADD COLUMN script TEXT NOT NULL,
    ADD COLUMN source TEXT NOT NULL,
    -- Repository path of the frozen evidence report the hash is of.
    ADD COLUMN evidence_ref TEXT NOT NULL,
    ALTER COLUMN registered_by_user_id DROP NOT NULL,
    DROP CONSTRAINT author_acceptance_class_key,
    ADD CONSTRAINT author_acceptance_class_key
        UNIQUE (policy_version, decision_class, script),
    -- A pair is registered once per normalizer configuration; a second
    -- registration of it is a repeat, not a new version.
    ADD CONSTRAINT author_acceptance_class_pair_key
        UNIQUE (config_version, decision_class, script),
    DROP CONSTRAINT author_acceptance_class_policy_version_check,
    -- 1 is the empty policy; at most nine digits so it casts to int.
    ADD CONSTRAINT author_acceptance_class_policy_version_check
        CHECK (policy_version ~ '^[1-9][0-9]{0,8}$' AND policy_version <> '1'),
    ADD CONSTRAINT author_acceptance_class_script_check
        CHECK (script = ANY (public.author_metadata_scripts())),
    ADD CONSTRAINT author_acceptance_class_evidence_ref_check
        CHECK (evidence_ref ~ '^[a-z0-9_./-]+\.json$' AND evidence_ref !~ '(^|/)\.\.(/|$)'),
    ADD CONSTRAINT author_acceptance_class_source_check
        CHECK (source IN ('shipped', 'admin')),
    DROP CONSTRAINT author_acceptance_class_actor_check,
    ADD CONSTRAINT author_acceptance_class_actor_check
        CHECK (coalesce(CASE source
                   WHEN 'shipped' THEN registered_by_user_id IS NULL
                   WHEN 'admin' THEN registered_by_user_id > 0
                   ELSE true -- an unknown source is the source check's to refuse
               END, false));

-- The selection consistency trigger of migration 24, with the automatic
-- branch now asking for a registration of the result's own script, made in
-- the selection's policy version or an earlier one.
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

-- The shipped policy, version 2: structured_person in Cyrillic and in Latin
-- script for the local normalizer authornorm-local-v1, backed by one frozen
-- report of 25 random current credits per script. A test pins the hash to the
-- file's bytes.
INSERT INTO public.author_acceptance_class
    (policy_version, decision_class, script, config_version, evidence_report_sha256, evidence_ref, source,
     registered_by_user_id)
VALUES
    ('2', 'structured_person', 'Cyrl', 'authornorm-local-v1',
     decode('bc7f7672dab5d62405261814dff1fc39762df581f53a0048e9edf9b1d049eac7', 'hex'),
     'internal/authornorm/policy_evidence/structured-person-v2.json', 'shipped', NULL),
    ('2', 'structured_person', 'Latn', 'authornorm-local-v1',
     decode('bc7f7672dab5d62405261814dff1fc39762df581f53a0048e9edf9b1d049eac7', 'hex'),
     'internal/authornorm/policy_evidence/structured-person-v2.json', 'shipped', NULL);
