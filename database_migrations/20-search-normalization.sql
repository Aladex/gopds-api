-- Canonical search normalization for every search query and search index.
--
-- Changing this function requires rebuilding every expression index that uses
-- it: the index stores the old normalized form and the query side would look
-- up the new one, silently missing rows. Any change here means a follow-up
-- migration that drops and recreates the search expression indexes.
CREATE OR REPLACE FUNCTION public.search_normalize(input text)
RETURNS text
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
STRICT
AS $$
    SELECT trim(
        regexp_replace(
            regexp_replace(
                replace(lower(normalize(input, NFC)), 'ё', 'е'),
                '[^[:alnum:]]+',
                ' ',
                'g'
            ),
            '[[:space:]]+',
            ' ',
            'g'
        )
    )
$$;

COMMENT ON FUNCTION public.search_normalize(text) IS
    'Canonical search normalization (NFC, lower, ё→е, punctuation→space, collapsed whitespace). Rebuild every expression index using it after any change.';
