package services_test

import (
	"context"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmreq"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/require"
)

// registerFixtureKind registers kind for a fixture merge configuration —
// the configuration, an eval run and its report written by hand — and
// returns the configuration's version and the registration's id. A kind
// registered already is returned as it is.
func (c *entityCatalog) registerFixtureKind(kind string) (version string, registration int64) {
	c.t.Helper()
	ctx := context.Background()
	identity, err := llmreq.NewMergeIdentity("http://fixture.invalid/v1", testParticipants, testOutput)
	require.NoError(c.t, err)
	version, err = database.EnsureAuthorLLMConfig(ctx, c.db, &identity, 4)
	require.NoError(c.t, err)
	if _, err = c.db.Query(pg.Scan(&registration), `SELECT id FROM author_entity_merge_registration
		WHERE config_version = ? AND kind = ?`, version, kind); err != nil || registration != 0 {
		require.NoError(c.t, err)
		return version, registration
	}
	var run, report int64
	_, err = c.db.QueryOne(pg.Scan(&run), `INSERT INTO author_llm_run (kind, mode, status, config_version, finished_at)
		VALUES ('eval', 'pilot', 'completed', ?, now()) RETURNING id`, version)
	require.NoError(c.t, err)
	_, err = c.db.QueryOne(pg.Scan(&report), `INSERT INTO author_llm_eval_report (run_id, config_version, report,
			report_sha256)
		VALUES (?, ?, '{}', sha256('fixture'::bytea)) RETURNING id`, run, version)
	require.NoError(c.t, err)
	_, err = c.db.QueryOne(pg.Scan(&registration), `INSERT INTO author_entity_merge_registration (config_version, kind,
			evidence_report_id, evidence_report_sha256, gate)
		VALUES (?, ?, ?, sha256('fixture'::bytea), 'judge_consensus') RETURNING id`, version, kind, report)
	require.NoError(c.t, err)
	return version, registration
}
