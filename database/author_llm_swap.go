package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-pg/pg/v10"
)

// The swap run's storage (design K, phase 7; migration 41): the catalog's
// role statistics, one snapshot per run that computes them, and the
// automatically selected structured_person inputs the swap candidates are
// found among. The candidate rule itself lives in services.

// AuthorTokenRoleCounts are one token's counts: the structured_person author
// credits that hold it in the first, the middle and the last field.
type AuthorTokenRoleCounts struct {
	First  int `json:"first" pg:"first_count,use_zero"`
	Middle int `json:"middle" pg:"middle_count,use_zero"`
	Last   int `json:"last" pg:"last_count,use_zero"`
}

// AuthorTokenRoleSource is one distinct source value among the current
// author credits selected on a structured_person result, with how many
// credits carry it.
type AuthorTokenRoleSource struct {
	First    *string `pg:"source_first_name"`
	Middle   *string `pg:"source_middle_name"`
	Last     *string `pg:"source_last_name"`
	Nickname *string `pg:"source_nickname"`
	Credits  int     `pg:"credits"`
}

// AuthorTokenRoleSources lists the source values the role statistics are
// counted over: the current author credits selected on a structured_person
// result. A credit selected on an LLM result (llm.swap) is not one of them,
// so a corrected swap stops counting the wrong way round.
func AuthorTokenRoleSources(ctx context.Context, db pg.DBI) ([]AuthorTokenRoleSource, error) {
	var rows []AuthorTokenRoleSource
	if _, err := db.QueryContext(ctx, &rows, `
		SELECT c.source_first_name, c.source_middle_name, c.source_last_name, c.source_nickname, count(*) AS credits
		FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
		JOIN book_contributor_credit_selection sel ON sel.credit_id = c.id AND sel.state = 'selected'
		JOIN contributor_normalization_result r ON r.id = sel.result_id AND r.decision_class = 'structured_person'
		WHERE c.role = 'author'
		GROUP BY 1, 2, 3, 4`); err != nil {
		return nil, fmt.Errorf("reading the role statistics' sources: %w", err)
	}
	return rows, nil
}

// authorTokenRoleChunk is how many token rows one insert writes.
const authorTokenRoleChunk = 5000

// StoreAuthorTokenRoleSnapshot writes a run's role statistics — the snapshot
// and every token's counts — in one transaction and returns its id. A run
// has one snapshot: a second store returns the first one's id and writes
// nothing.
func StoreAuthorTokenRoleSnapshot(
	ctx context.Context, db *pg.DB, runID int64, credits int, stats map[string]AuthorTokenRoleCounts,
) (int64, error) {
	var id int64
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		_, err := tx.QueryOneContext(ctx, pg.Scan(&id), `
			INSERT INTO author_token_role_snapshot (run_id, credits, tokens) VALUES (?0, ?1, ?2)
			ON CONFLICT (run_id) DO NOTHING RETURNING id`, runID, credits, len(stats))
		if errors.Is(err, pg.ErrNoRows) {
			_, err = tx.QueryOneContext(ctx, pg.Scan(&id), `SELECT id FROM author_token_role_snapshot WHERE run_id = ?`, runID)
			return err
		}
		if err != nil {
			return err
		}
		type row struct {
			Token string `json:"token"`
			AuthorTokenRoleCounts
		}
		rows := make([]row, 0, min(len(stats), authorTokenRoleChunk))
		flush := func() error {
			if len(rows) == 0 {
				return nil
			}
			payload, marshalErr := json.Marshal(rows)
			if marshalErr != nil {
				return marshalErr
			}
			rows = rows[:0]
			_, execErr := tx.ExecContext(ctx, `
				INSERT INTO author_token_role_stats (snapshot_id, token, first_count, middle_count, last_count)
				SELECT ?0, r.token, r.first, r.middle, r.last
				FROM jsonb_to_recordset(?1::jsonb) AS r(token text, first int, middle int, last int)`, id, string(payload))
			return execErr
		}
		for token, c := range stats {
			rows = append(rows, row{Token: token, AuthorTokenRoleCounts: c})
			if len(rows) == authorTokenRoleChunk {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		return flush()
	})
	if err != nil {
		return 0, fmt.Errorf("storing the role statistics: %w", err)
	}
	return id, nil
}

// AuthorTokenRoleSnapshotOf is the id of a run's role statistics; 0 when the
// run has none yet.
func AuthorTokenRoleSnapshotOf(ctx context.Context, db pg.DBI, runID int64) (int64, error) {
	var id int64
	_, err := db.QueryOneContext(ctx, pg.Scan(&id), `SELECT id FROM author_token_role_snapshot WHERE run_id = ?`, runID)
	if errors.Is(err, pg.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reading a run's role statistics: %w", err)
	}
	return id, nil
}

// AuthorTokenRoleStats reads a snapshot's counts: of the tokens given, or of
// every token when tokens is nil.
func AuthorTokenRoleStats(ctx context.Context, db pg.DBI, snapshotID int64, tokens []string) (map[string]AuthorTokenRoleCounts, error) {
	var rows []struct {
		Token string `pg:"token"`
		AuthorTokenRoleCounts
	}
	query := `SELECT token, first_count, middle_count, last_count FROM author_token_role_stats WHERE snapshot_id = ?0`
	params := []interface{}{snapshotID}
	if tokens != nil {
		if len(tokens) == 0 {
			return map[string]AuthorTokenRoleCounts{}, nil
		}
		query += ` AND token IN (?1)`
		params = append(params, pg.In(tokens))
	}
	if _, err := db.QueryContext(ctx, &rows, query, params...); err != nil {
		return nil, fmt.Errorf("reading the role statistics: %w", err)
	}
	out := make(map[string]AuthorTokenRoleCounts, len(rows))
	for _, r := range rows {
		out[r.Token] = r.AuthorTokenRoleCounts
	}
	return out, nil
}

// AuthorLLMSwapSource is one input the swap candidates are found among: a
// fingerprint every current author credit of which is selected
// automatically on the same structured_person result, with no manual
// decision anywhere on it — its population row (class swap) and that result.
type AuthorLLMSwapSource struct {
	AuthorLLMPopulationRow
	ResultID int64 `pg:"result_id"`
}

// AuthorLLMSwapSources lists the inputs a swap candidate may be: every
// current author credit of the fingerprint selected automatically on one
// structured_person result, and no override, no admin decision and no other
// selection on the fingerprint (a manual decision is never touched).
func AuthorLLMSwapSources(ctx context.Context, db pg.DBI) ([]AuthorLLMSwapSource, error) {
	var rows []AuthorLLMSwapSource
	err := withoutNestedLoops(ctx, db, func(tx *pg.Tx) error {
		_, err := tx.QueryContext(ctx, &rows, `
			WITH cur AS (
				SELECT c.id, c.source_fingerprint, s.extractor_version, c.source_first_name, c.source_middle_name,
					c.source_last_name, c.source_nickname
				FROM book_contributor_credit c
				JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
				WHERE c.role = 'author'
			), per_input AS (
				SELECT cur.source_fingerprint, cur.extractor_version, min(cur.id) AS credit_id, count(*) AS weight,
					min(sel.result_id) AS result_id,
					bool_and(sel.state = 'selected' AND sel.basis = 'automatic' AND sel.override_id IS NULL
						AND sel.decided_by_user_id IS NULL AND r.decision_class = 'structured_person') AS automatic,
					count(DISTINCT sel.result_id) AS results
				FROM cur
				JOIN book_contributor_credit_selection sel ON sel.credit_id = cur.id
				JOIN contributor_normalization_result r ON r.id = sel.result_id
				GROUP BY 1, 2
			)
			SELECT p.source_fingerprint, 'swap' AS decision_class, r.script, r.quality_flags, p.extractor_version,
				cur.source_first_name, cur.source_middle_name, cur.source_last_name, cur.source_nickname, p.weight,
				p.result_id
			FROM per_input p
			JOIN cur ON cur.id = p.credit_id
			JOIN contributor_normalization_result r ON r.id = p.result_id
			WHERE p.automatic AND p.results = 1
				AND NOT EXISTS (SELECT 1 FROM book_contributor_credit_selection o
					WHERE o.source_fingerprint = p.source_fingerprint
						AND (o.override_id IS NOT NULL OR o.decided_by_user_id IS NOT NULL))
				AND NOT EXISTS (SELECT 1 FROM contributor_manual_override m
					WHERE m.source_fingerprint = p.source_fingerprint)
			ORDER BY p.source_fingerprint, p.extractor_version`)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("reading the swap sources: %w", err)
	}
	return rows, nil
}
