package database

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	migrations "gopds-api/database_migrations"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/migrate"
	"gopds-api/internal/testdb"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Migration 25: the acceptance policy keyed by (decision class, script), each
// registration citing the immutable sample it was switched on from, under a
// cumulative integer policy version.

const registrationInsert = `INSERT INTO author_acceptance_class
	(policy_version, decision_class, script, config_version, evidence_report_sha256, evidence_ref, source,
	 registered_by_user_id)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

// fixtureEvidence stands in for a shipped evidence report: a path and a hash.
const fixtureEvidenceRef = "internal/authornorm/policy_evidence/fixture.json"

// registerPair writes one shipped registration of a Cyrillic pair.
func (f *authorSchemaFixture) registerPair(version, class, config string) {
	f.t.Helper()
	f.exec(registrationInsert, version, class, "Cyrl", config, fingerprint(32, 0xee), fixtureEvidenceRef, "shipped", nil)
}

// The schema's closed set of scripts is exactly the normalizer's.
func TestAcceptanceScriptsMatchTheNormalizer(t *testing.T) {
	f := withAuthorSchemaTx(t)
	var stored []string
	_, err := f.tx.QueryOne(pg.Scan(pg.Array(&stored)), `SELECT public.author_metadata_scripts()`)
	require.NoError(t, err)
	want := make([]string, 0, len(authornorm.Scripts()))
	for _, s := range authornorm.Scripts() {
		want = append(want, string(s))
	}
	slices.Sort(stored)
	assert.Equal(t, want, stored)
}

// A registration is keyed by script and numbered by an integer version above
// the empty policy; it cites its evidence report by path and hash, and names
// an actor exactly when an administrator made it.
func TestAcceptanceClassIsKeyedByScript(t *testing.T) {
	f := withAuthorSchemaTx(t)
	actor := f.user()
	sum := fingerprint(32, 7)
	row := func(version, script, config, source string, by interface{}) []interface{} {
		return []interface{}{version, "plain", script, config, sum, fixtureEvidenceRef, source, by}
	}

	f.exec(registrationInsert, row("2", "Cyrl", "normalizer-v1", "shipped", nil)...)
	// The other script of the same class is a registration of its own, in the
	// same version or a later one.
	f.accept(registrationInsert, row("2", "Latn", "normalizer-v1", "shipped", nil)...)
	f.accept(registrationInsert, row("3", "Latn", "normalizer-v1", "admin", actor)...)
	// A repeat of the pair under the same configuration is refused whatever
	// version it claims.
	f.reject(sqlstateUnique, "author_acceptance_class_pair_key", registrationInsert, row("3", "Cyrl", "normalizer-v1", "shipped", nil)...)
	f.reject(sqlstateUnique, "author_acceptance_class_key", registrationInsert, row("2", "Cyrl", "normalizer-v2", "shipped", nil)...)

	for _, version := range []string{"1", "0", "01", "policy-2", "", "1234567890"} {
		f.reject(sqlstateCheck, "author_acceptance_class_policy_version_check",
			registrationInsert, row(version, "Grek", "normalizer-v2", "shipped", nil)...)
	}
	for _, script := range []string{"Abcd", "cyrl", "CYRL", ""} {
		f.reject(sqlstateCheck, "author_acceptance_class_script_check",
			registrationInsert, row("5", script, "normalizer-v2", "shipped", nil)...)
	}
	f.reject("23502", "", registrationInsert, "5", "plain", nil, "normalizer-v2", sum, fixtureEvidenceRef, "shipped", nil)

	// Who registered it: nobody for a shipped row, an administrator otherwise.
	f.reject(sqlstateCheck, "author_acceptance_class_actor_check", registrationInsert, row("5", "Grek", "normalizer-v2", "shipped", actor)...)
	f.reject(sqlstateCheck, "author_acceptance_class_actor_check", registrationInsert, row("5", "Grek", "normalizer-v2", "admin", nil)...)
	f.reject(sqlstateCheck, "author_acceptance_class_actor_check", registrationInsert, row("5", "Grek", "normalizer-v2", "admin", 0)...)
	f.reject(sqlstateCheck, "author_acceptance_class_source_check", registrationInsert, row("5", "Grek", "normalizer-v2", "migration", nil)...)

	for _, ref := range []string{"", "evidence.txt", "../outside.json", "a/../../b.json", "Evidence/X.json", "with space.json"} {
		f.reject(sqlstateCheck, "author_acceptance_class_evidence_ref_check", registrationInsert,
			"5", "plain", "Grek", "normalizer-v2", sum, ref, "shipped", nil)
	}
}

// An automatic selection rests on a registration of the result's own class
// and script, made in the selection's policy version or an earlier one.
func TestAutomaticSelectionNeedsTheScriptRegisteredByItsVersion(t *testing.T) {
	f := withAuthorSchemaTx(t)
	fp := fingerprint(32, 0x5c)
	credit := f.authorCredit(fp)
	cyrillic := f.result(autoRow(fp, "structured_person", "normalizer-v1"))
	latinRow := autoRow(fp, "structured_person", "normalizer-v1")
	latinRow.key = keyFor(string(fp), "extractor-v1", "normalizer-v1", "latin")
	latinRow.script = ptr("Latn")
	latin := f.result(latinRow)
	// Versions 2 and 3 are the shipped registrations (migrations 25 and 59).
	f.registerPair("4", "structured_person", "normalizer-v1")

	automatic := func(result int64, version string) []interface{} {
		return []interface{}{credit, fp, "selected", result, "automatic", nil, version, nil, nil}
	}
	f.accept(selectionInsert, automatic(cyrillic, "4")...)
	f.accept(selectionInsert, automatic(cyrillic, "7")...)
	f.reject(sqlstateCheck, "not registered", selectionInsert, automatic(cyrillic, "3")...)
	f.reject(sqlstateCheck, "not registered", selectionInsert, automatic(latin, "4")...)
	f.reject(sqlstateCheck, "integer policy version", selectionInsert, automatic(cyrillic, "policy-4")...)
}

// Migration 25 refuses to guess a script for a registration made before it:
// it stops, and the ledger stays where it was.
func TestAcceptanceMigrationRefusesRegistrationsWithoutAScript(t *testing.T) {
	requireDatabase(t)
	cfg, _ := testdb.Configured()
	name := fmt.Sprintf("acceptance_migration_test_%d", time.Now().UnixNano())
	_, err := db.Exec("CREATE DATABASE " + name)
	require.NoError(t, err)
	t.Cleanup(func() {
		if _, dropErr := db.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)"); dropErr != nil {
			t.Errorf("dropping the scratch database %s: %v", name, dropErr)
		}
	})
	scratch := pg.Connect(&pg.Options{Addr: cfg.Host, User: cfg.User, Password: cfg.Password, Database: name})
	t.Cleanup(func() { _ = scratch.Close() })

	const last24 = "24-author-normalization-pipeline.sql"
	ctx := context.Background()
	_, err = migrate.Run(ctx, scratch, filesUpTo(t, last24), migrations.Dir, migrate.AppBaseline())
	require.NoError(t, err)
	_, err = scratch.Exec(`INSERT INTO author_acceptance_class
		(policy_version, decision_class, config_version, evidence_report_sha256, registered_by_user_id)
		VALUES ('1', 'structured_person', ?, decode(repeat('ee', 32), 'hex'), 1)`, authornorm.NormalizerVersion)
	require.NoError(t, err)

	_, err = migrate.Run(ctx, scratch, os.DirFS(".."), "database_migrations", migrate.AppBaseline())
	var failed *migrate.FileError
	require.ErrorAs(t, err, &failed)
	assert.Equal(t, "25-author-acceptance-by-script.sql", failed.Name)
	var pgErr pg.Error
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, sqlstateRestrict, pgErr.Field('C'))

	var newest string
	_, err = scratch.QueryOne(pg.Scan(&newest), `SELECT max(version) FROM schema_migrations`)
	require.NoError(t, err)
	assert.Equal(t, last24, newest)
	var evidenceTable bool
	_, err = scratch.QueryOne(pg.Scan(&evidenceTable), `SELECT to_regclass('author_acceptance_evidence') IS NOT NULL`)
	require.NoError(t, err)
	assert.False(t, evidenceTable, "a refused migration left its objects behind")
}

// filesUpTo is the embedded migration set cut after last.
func filesUpTo(t *testing.T, last string) fs.FS {
	t.Helper()
	entries, err := fs.ReadDir(migrations.FS(), migrations.Dir)
	require.NoError(t, err)
	cut := fstest.MapFS{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") || e.Name() > last {
			continue
		}
		body, readErr := fs.ReadFile(migrations.FS(), e.Name())
		require.NoError(t, readErr)
		cut[e.Name()] = &fstest.MapFile{Data: body}
	}
	return cut
}

// The policy ships with the code: every migrated database holds exactly the
// shipped registrations of migrations 25 and 59, and their evidence hash is
// the SHA-256 of the frozen report checked in at evidence_ref. Migration 59's
// report names exactly the pairs the migration registers, for the normalizer
// version in force, and every check in it passed.
func TestShippedPolicyIsTheEvidenceFile(t *testing.T) {
	f := withAuthorSchemaTx(t)
	var rows []models.AuthorAcceptanceClass
	require.NoError(t, f.tx.Model(&rows).Where("source = 'shipped'").Order("config_version", "decision_class", "script").Select())
	require.Len(t, rows, 7)

	const v1Ref = "internal/authornorm/policy_evidence/structured-person-v2.json"
	v1Report, err := os.ReadFile("../" + v1Ref)
	require.NoError(t, err)
	v1Sum := sha256.Sum256(v1Report)
	for i, script := range []string{"Cyrl", "Latn"} {
		r := rows[i]
		assert.Equal(t, "2", r.PolicyVersion)
		assert.Equal(t, "structured_person", r.DecisionClass)
		assert.Equal(t, script, r.Script)
		assert.Equal(t, authornorm.PreviousNormalizerVersion, r.ConfigVersion)
		assert.Equal(t, v1Ref, r.EvidenceRef)
		assert.Equal(t, v1Sum[:], r.EvidenceReportSHA256, "the hash is of the checked-in report")
		assert.Nil(t, r.RegisteredByUserID)
	}

	const v2Ref = "internal/authornorm/policy_evidence/local-v2.json"
	v2Report, err := os.ReadFile("../" + v2Ref)
	require.NoError(t, err)
	v2Sum := sha256.Sum256(v2Report)
	var evidence struct {
		NormalizerVersion string `json:"normalizer_version"`
		Pairs             []struct {
			DecisionClass string `json:"decision_class"`
			Script        string `json:"script"`
		} `json:"pairs"`
		Strata []struct {
			DecisionClass string            `json:"decision_class"`
			Script        string            `json:"script"`
			Passed        string            `json:"passed"`
			Rows          []json.RawMessage `json:"rows"`
		} `json:"strata"`
	}
	require.NoError(t, json.Unmarshal(v2Report, &evidence))
	assert.Equal(t, authornorm.NormalizerVersion, evidence.NormalizerVersion)
	registered := map[string]bool{}
	for _, r := range rows[2:] {
		assert.Equal(t, "3", r.PolicyVersion, "the next version after migration 25's on a fresh database")
		assert.Equal(t, authornorm.NormalizerVersion, r.ConfigVersion)
		assert.Equal(t, v2Ref, r.EvidenceRef)
		assert.Equal(t, v2Sum[:], r.EvidenceReportSHA256, "the hash is of the checked-in report")
		assert.Nil(t, r.RegisteredByUserID)
		assert.True(t, authornorm.DecisionClass(r.DecisionClass).Selectable(), r.DecisionClass)
		registered[r.DecisionClass+"/"+r.Script] = true
	}
	named, sampled := map[string]bool{}, map[string]bool{}
	for _, p := range evidence.Pairs {
		named[p.DecisionClass+"/"+p.Script] = true
	}
	for _, s := range evidence.Strata {
		sampled[s.DecisionClass+"/"+s.Script] = true
		assert.Equal(t, fmt.Sprintf("%d of %d", len(s.Rows), len(s.Rows)), s.Passed, "%s/%s", s.DecisionClass, s.Script)
		assert.NotEmpty(t, s.Rows)
	}
	assert.Equal(t, registered, named, "the report names exactly the registered pairs")
	assert.Equal(t, registered, sampled, "every registered pair has a checked sample")

	p, err := LoadCurrentAcceptancePolicy(context.Background(), f.tx, authornorm.NormalizerVersion)
	require.NoError(t, err)
	assert.Equal(t, 3, p.Version())
	assert.False(t, p.Empty())
}
