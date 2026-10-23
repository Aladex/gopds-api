package database

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gopds-api/internal/authorentity"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// The author entities in the shadow (migration 56, design E phase E1): what
// a build pass reads — the selected author credits of the current snapshots,
// their books, what the previous pass stored — and how it writes a plan's
// units, each one whole or skipped alone.

// entityCreditRow is one selected author credit with its result.
type entityCreditRow struct {
	ID               int64    `pg:"id"`
	BookID           int64    `pg:"book_id"`
	Fingerprint      []byte   `pg:"fingerprint"`
	ExtractorVersion string   `pg:"extractor_version"`
	ResultID         int64    `pg:"result_id"`
	Kind             string   `pg:"kind"`
	Script           string   `pg:"script"`
	Family           string   `pg:"family"`
	Given            string   `pg:"given"`
	Additional       []string `pg:"additional,array"`
	Suffix           string   `pg:"suffix"`
	Nickname         string   `pg:"nickname"`
	DisplayName      string   `pg:"display_name"`
	SortName         string   `pg:"sort_name"`
	Tag              string   `pg:"tag"`
}

// The selected author credits of the current snapshots. The namesake tag
// "(N)" is read from the source fields here, so only its number leaves the
// database.
//
// #nosec G101 -- a query naming name columns, not a credential
const entityCreditsQuery = `
SELECT c.id, s.book_id, c.source_fingerprint AS fingerprint, s.extractor_version, r.id AS result_id, r.kind,
	coalesce(r.script, '') AS script, coalesce(r.family_name, '') AS family, coalesce(r.given_name, '') AS given,
	r.additional_names AS additional, coalesce(r.suffix, '') AS suffix, coalesce(r.nickname, '') AS nickname,
	coalesce(r.display_name, '') AS display_name, coalesce(r.sort_name, '') AS sort_name,
	coalesce(substring(concat_ws(' ', c.source_first_name, c.source_middle_name, c.source_last_name,
		c.source_nickname, c.source_display_name) FROM '\((\d{1,3})\)'), '') AS tag
FROM public.book_contributor_credit c
JOIN public.book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
JOIN public.book_contributor_credit_selection sel ON sel.credit_id = c.id AND sel.state = 'selected'
JOIN public.contributor_normalization_result r ON r.id = sel.result_id
WHERE c.role = 'author' AND r.kind IN ('person', 'collective') AND c.id > ?
ORDER BY c.id
LIMIT ?`

// entityBookRow is a book the evidence reads.
type entityBookRow struct {
	ID     int64   `pg:"id"`
	Title  string  `pg:"title"`
	Series []int64 `pg:"series,array"`
}

const entityBooksQuery = `
SELECT b.id, b.title, coalesce(array_agg(bs.ser_id ORDER BY bs.ser_id) FILTER (WHERE bs.ser_id IS NOT NULL), '{}') AS series
FROM public.opds_catalog_book b
LEFT JOIN public.opds_catalog_bseries bs ON bs.book_id = b.id
WHERE b.id IN (SELECT book_id FROM public.book_metadata_snapshot WHERE is_current) AND b.id > ?
GROUP BY b.id
ORDER BY b.id
LIMIT ?`

// LoadAuthorEntityInput reads what a build pass reads: the selected author
// credits, their books, and the legacy author each credit's read-model row
// is matched to (for the comparison report).
func LoadAuthorEntityInput(ctx context.Context, db pg.DBI) (*authorentity.Input, error) {
	var total int
	if _, err := db.QueryOneContext(ctx, pg.Scan(&total), `
		SELECT count(*) FROM public.book_contributor_credit_selection WHERE state = 'selected'`); err != nil {
		return nil, fmt.Errorf("counting the selected author credits: %w", err)
	}
	in := &authorentity.Input{Credits: make([]authorentity.Credit, 0, total),
		Books: map[int64]authorentity.Book{}, Legacy: map[int64]int64{}}
	if err := loadEntityCredits(ctx, db, in); err != nil {
		return nil, err
	}
	if err := loadEntityBooks(ctx, db, in); err != nil {
		return nil, err
	}
	var legacy []struct {
		CreditID int64 `pg:"credit_id"`
		LegacyID int64 `pg:"legacy_author_id"`
	}
	if _, err := db.QueryContext(ctx, &legacy, `
		SELECT credit_id, legacy_author_id FROM public.book_author_display
		WHERE source = 'layer' AND credit_id IS NOT NULL AND legacy_author_id IS NOT NULL`); err != nil {
		return nil, fmt.Errorf("reading the legacy matches: %w", err)
	}
	for _, l := range legacy {
		in.Legacy[l.CreditID] = l.LegacyID
	}
	return in, nil
}

// entityStrings shares the result and the strings the credits of one result
// or fingerprint carry: the catalog has four credits per result.
type entityStrings struct {
	results      map[int64]*authorentity.Result
	fingerprints map[string]string
	versions     map[string]string
}

func (e *entityStrings) credit(r *entityCreditRow) authorentity.Credit {
	tag := 0
	if r.Tag != "" {
		tag, _ = strconv.Atoi(r.Tag)
	}
	res := e.results[r.ResultID]
	if res == nil {
		res = &authorentity.Result{ResultID: r.ResultID, Kind: r.Kind, Script: r.Script, Family: r.Family,
			Given: r.Given, Additional: r.Additional, Suffix: r.Suffix, Nickname: r.Nickname,
			DisplayName: r.DisplayName, SortName: r.SortName}
		e.results[r.ResultID] = res
	}
	fp, ok := e.fingerprints[string(r.Fingerprint)]
	if !ok {
		fp = string(r.Fingerprint)
		e.fingerprints[fp] = fp
	}
	version, ok := e.versions[r.ExtractorVersion]
	if !ok {
		version = r.ExtractorVersion
		e.versions[version] = version
	}
	return authorentity.Credit{ID: r.ID, BookID: r.BookID, Fingerprint: fp, ExtractorVersion: version, Tag: tag,
		Result: res}
}

// loadEntityCredits reads the credits a page at a time: the catalog's 600k
// credits never sit in the database driver's rows and the input at once.
func loadEntityCredits(ctx context.Context, db pg.DBI, in *authorentity.Input) error {
	shared := entityStrings{results: map[int64]*authorentity.Result{}, fingerprints: map[string]string{},
		versions: map[string]string{}}
	for after := int64(0); ; {
		var page []entityCreditRow
		if _, err := db.QueryContext(ctx, &page, entityCreditsQuery, after, entityLoadPage); err != nil {
			return fmt.Errorf("reading the selected author credits: %w", err)
		}
		for i := range page {
			in.Credits = append(in.Credits, shared.credit(&page[i]))
		}
		if len(page) < entityLoadPage {
			return nil
		}
		after = page[len(page)-1].ID
	}
}

// loadEntityBooks reads the books of the credits a page at a time.
func loadEntityBooks(ctx context.Context, db pg.DBI, in *authorentity.Input) error {
	for after := int64(0); ; {
		var page []entityBookRow
		if _, err := db.QueryContext(ctx, &page, entityBooksQuery, after, entityLoadPage); err != nil {
			return fmt.Errorf("reading the books of the credits: %w", err)
		}
		for i := range page {
			in.Books[page[i].ID] = authorentity.Book{Title: page[i].Title, Series: page[i].Series}
		}
		if len(page) < entityLoadPage {
			return nil
		}
		after = page[len(page)-1].ID
	}
}

// entityLoadPage is how many credits or books one read takes.
const entityLoadPage = 20000

// CountUnselectedAuthorCredits counts the author credits of current
// snapshots without a selected result: E1 gives them no entity (deferred
// until the review or the model decides them).
func CountUnselectedAuthorCredits(ctx context.Context, db pg.DBI) (int, error) {
	var n int
	if _, err := db.QueryOneContext(ctx, pg.Scan(&n), `
		SELECT count(*) FROM public.book_contributor_credit c
		JOIN public.book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
		WHERE c.role = 'author' AND NOT EXISTS (
			SELECT 1 FROM public.book_contributor_credit_selection sel
			WHERE sel.credit_id = c.id AND sel.state = 'selected')`); err != nil {
		return 0, fmt.Errorf("counting the unselected author credits: %w", err)
	}
	return n, nil
}

// LoadAuthorEntityState reads what is stored in the entity tables.
func LoadAuthorEntityState(ctx context.Context, db pg.DBI) (*authorentity.State, error) {
	s := authorentity.NewState()
	for _, load := range []func(context.Context, pg.DBI, *authorentity.State) error{
		loadStoredEntities, loadStoredFingerprints, loadStoredCredits,
	} {
		if err := load(ctx, db, s); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func loadStoredEntities(ctx context.Context, db pg.DBI, s *authorentity.State) error {
	var entities []struct {
		ID          uuid.UUID  `pg:"id,type:uuid"`
		Kind        string     `pg:"kind"`
		DisplayName string     `pg:"display_name"`
		SortName    string     `pg:"sort_name"`
		SearchKey   string     `pg:"search_key"`
		NamePinned  bool       `pg:"name_pinned,use_zero"`
		MergedInto  *uuid.UUID `pg:"merged_into,type:uuid"`
	}
	if _, err := db.QueryContext(ctx, &entities, `
		SELECT id, kind, display_name, sort_name, search_key, name_pinned, merged_into FROM public.author_entity`); err != nil {
		return fmt.Errorf("reading the entities: %w", err)
	}
	for _, e := range entities {
		s.Entities[e.ID] = authorentity.EntityRow{ID: e.ID, Kind: e.Kind, DisplayName: e.DisplayName,
			SortName: e.SortName, SearchKey: e.SearchKey, NamePinned: e.NamePinned, MergedInto: e.MergedInto}
	}
	return nil
}

func loadStoredFingerprints(ctx context.Context, db pg.DBI, s *authorentity.State) error {
	var fps []struct {
		Fingerprint      []byte    `pg:"source_fingerprint"`
		ExtractorVersion string    `pg:"extractor_version"`
		Ordinal          int       `pg:"ordinal,use_zero"`
		EntityID         uuid.UUID `pg:"entity_id,type:uuid"`
		Method           string    `pg:"method"`
		Rule             string    `pg:"rule"`
	}
	if _, err := db.QueryContext(ctx, &fps, `
		SELECT source_fingerprint, extractor_version, ordinal, entity_id, method, rule
		FROM public.author_entity_assignment`); err != nil {
		return fmt.Errorf("reading the fingerprint assignments: %w", err)
	}
	for _, r := range fps {
		k := authorentity.FPKey{Fingerprint: string(r.Fingerprint), ExtractorVersion: r.ExtractorVersion, Ordinal: r.Ordinal}
		s.Fingerprints[k] = authorentity.AssignmentRow{Key: k, EntityID: r.EntityID, Method: r.Method, Rule: r.Rule}
	}
	return nil
}

func loadStoredCredits(ctx context.Context, db pg.DBI, s *authorentity.State) error {
	var credits []struct {
		CreditID int64      `pg:"credit_id"`
		Ordinal  int        `pg:"ordinal,use_zero"`
		EntityID *uuid.UUID `pg:"entity_id,type:uuid"`
		Method   string     `pg:"method"`
		Rule     string     `pg:"rule"`
	}
	if _, err := db.QueryContext(ctx, &credits, `
		SELECT credit_id, ordinal, entity_id, method, rule FROM public.author_entity_credit_assignment`); err != nil {
		return fmt.Errorf("reading the credit assignments: %w", err)
	}
	for _, r := range credits {
		k := authorentity.CreditKey{CreditID: r.CreditID, Ordinal: r.Ordinal}
		s.Credits[k] = authorentity.CreditRow{Key: k, EntityID: r.EntityID, Method: r.Method, Rule: r.Rule}
	}
	return nil
}

// AuthorEntityUnitSkip is a unit the database refused, by an identifier and
// the SQLSTATE: the entity's ID and its first fingerprint, never a name.
type AuthorEntityUnitSkip struct {
	EntityID    uuid.UUID
	Fingerprint []byte
	SQLState    string
}

// ApplyAuthorEntityUnits writes components of units (Plan.Components) in
// the caller's transaction and returns how many rows it wrote. The batch is
// written under a savepoint; when the database refuses it (unitRefused) it
// is written again a component at a time, each under its own savepoint, and
// a component refused is skipped whole — every unit of it, so a split or a
// merge is never written in part — while the other components land (§1.5).
// Any other error ends the batch.
func ApplyAuthorEntityUnits(
	ctx context.Context, tx pg.DBI, runID int64, components [][]authorentity.Unit,
) (changes int, skipped []AuthorEntityUnitSkip, err error) {
	var units []authorentity.Unit
	for _, comp := range components {
		units = append(units, comp...)
	}
	if len(units) == 0 {
		return 0, nil, nil
	}
	err = underSavepoint(ctx, tx, "author_entity_batch", func() error { return writeEntityUnits(ctx, tx, runID, units) })
	if err == nil {
		for i := range units {
			changes += units[i].Changes()
		}
		return changes, nil, nil
	}
	if !unitRefused(err) {
		return 0, nil, err
	}
	for _, comp := range components {
		err = underSavepoint(ctx, tx, "author_entity_component", func() error { return writeEntityUnits(ctx, tx, runID, comp) })
		switch {
		case err == nil:
			for i := range comp {
				changes += comp[i].Changes()
			}
		case unitRefused(err):
			for i := range comp {
				skipped = append(skipped, unitSkip(&comp[i], sqlState(err)))
			}
		default:
			return 0, nil, err
		}
	}
	return changes, skipped, nil
}

// unitRefused tells a unit the database refused — for its data (SQLSTATE
// class 23), or a row another writer holds past the lock timeout (55P03) —
// from a failure of the pass.
func unitRefused(err error) bool {
	return isIntegrityViolation(err) || sqlState(err) == "55P03"
}

func unitSkip(u *authorentity.Unit, state string) AuthorEntityUnitSkip {
	sk := AuthorEntityUnitSkip{EntityID: u.EntityID, SQLState: state}
	switch {
	case len(u.Fingerprints) > 0:
		sk.Fingerprint = []byte(u.Fingerprints[0].Key.Fingerprint)
	case len(u.DeleteFingerprints) > 0:
		sk.Fingerprint = []byte(u.DeleteFingerprints[0].Fingerprint)
	}
	return sk
}

// entityUnitColumns are a batch of units as column arrays, so each table
// takes one statement per batch.
type entityUnitColumns struct {
	entID, entKind, entDisplay, entSort, entSearch []string
	entPinned                                      []bool

	mergeFrom, mergeTo, mergeMethod []string
	mergeShared                     []int

	fpHex, fpVersion, fpEntity, fpMethod, fpRule []string
	fpOrdinal                                    []int

	delFPHex, delFPVersion []string
	delFPOrdinal           []int

	crID                       []int64
	crOrdinal                  []int
	crEntity, crMethod, crRule []string
	delCrID                    []int64
	delCrOrdinal               []int
}

func (c *entityUnitColumns) add(u *authorentity.Unit) {
	if e := u.Entity; e != nil {
		c.entID = append(c.entID, u.EntityID.String())
		c.entKind = append(c.entKind, e.Kind)
		c.entDisplay = append(c.entDisplay, e.DisplayName)
		c.entSort = append(c.entSort, e.SortName)
		c.entSearch = append(c.entSearch, e.SearchKey)
		c.entPinned = append(c.entPinned, e.NamePinned)
	}
	for _, m := range u.Absorb {
		c.mergeFrom = append(c.mergeFrom, m.From.String())
		c.mergeTo = append(c.mergeTo, m.To.String())
		c.mergeMethod = append(c.mergeMethod, m.Method)
		c.mergeShared = append(c.mergeShared, m.SharedBooks)
	}
	for _, a := range u.Fingerprints {
		c.fpHex = append(c.fpHex, hex.EncodeToString([]byte(a.Key.Fingerprint)))
		c.fpVersion = append(c.fpVersion, a.Key.ExtractorVersion)
		c.fpOrdinal = append(c.fpOrdinal, a.Key.Ordinal)
		c.fpEntity = append(c.fpEntity, a.EntityID.String())
		c.fpMethod = append(c.fpMethod, a.Method)
		c.fpRule = append(c.fpRule, a.Rule)
	}
	for _, k := range u.DeleteFingerprints {
		c.delFPHex = append(c.delFPHex, hex.EncodeToString([]byte(k.Fingerprint)))
		c.delFPVersion = append(c.delFPVersion, k.ExtractorVersion)
		c.delFPOrdinal = append(c.delFPOrdinal, k.Ordinal)
	}
	for _, r := range u.Credits {
		c.crID = append(c.crID, r.Key.CreditID)
		c.crOrdinal = append(c.crOrdinal, r.Key.Ordinal)
		c.crEntity = append(c.crEntity, r.EntityID.String())
		c.crMethod = append(c.crMethod, r.Method)
		c.crRule = append(c.crRule, r.Rule)
	}
	for _, k := range u.DeleteCredits {
		c.delCrID = append(c.delCrID, k.CreditID)
		c.delCrOrdinal = append(c.delCrOrdinal, k.Ordinal)
	}
}

// writeEntityUnits writes a batch: the entities first (the assignments
// reference them), then the merges, the assignments and the names. A manual
// row is never replaced or removed.
func writeEntityUnits(ctx context.Context, tx pg.DBI, runID int64, units []authorentity.Unit) error {
	var c entityUnitColumns
	for i := range units {
		c.add(&units[i])
	}
	for _, s := range c.steps(runID) {
		if s.skip {
			continue
		}
		if _, err := tx.ExecContext(ctx, s.query, s.args...); err != nil {
			return err
		}
	}
	return nil
}

// entityWriteStep is one statement of a batch; skip when it has no rows.
type entityWriteStep struct {
	skip  bool
	query string
	args  []interface{}
}

// steps are a batch's statements in order.
func (c *entityUnitColumns) steps(runID int64) []entityWriteStep {
	return []entityWriteStep{
		{len(c.entID) == 0, `
			INSERT INTO public.author_entity (id, kind, display_name, sort_name, search_key, name_pinned)
			SELECT * FROM unnest(?::uuid[], ?::text[], ?::text[], ?::text[], ?::text[], ?::boolean[])
			ON CONFLICT (id) DO UPDATE SET kind = EXCLUDED.kind, display_name = EXCLUDED.display_name,
				sort_name = EXCLUDED.sort_name, search_key = EXCLUDED.search_key,
				name_pinned = EXCLUDED.name_pinned, merged_into = NULL`,
			[]interface{}{pg.Array(c.entID), pg.Array(c.entKind), pg.Array(c.entDisplay), pg.Array(c.entSort),
				pg.Array(c.entSearch), pg.Array(c.entPinned)}},
		{len(c.mergeFrom) == 0, `
			WITH m AS (SELECT * FROM unnest(?::uuid[], ?::uuid[], ?::text[], ?::int[]) AS m(from_id, to_id, method, shared)),
			marked AS (
				UPDATE public.author_entity e SET merged_into = m.to_id FROM m
				WHERE e.id = m.from_id AND e.merged_into IS NULL
				RETURNING e.id)
			INSERT INTO public.author_entity_merge_log (from_id, to_id, method, evidence)
			SELECT m.from_id, m.to_id, m.method, jsonb_build_object('build_run_id', ?::bigint, 'shared_books', m.shared)
			FROM m WHERE m.from_id IN (SELECT id FROM marked)`,
			[]interface{}{pg.Array(c.mergeFrom), pg.Array(c.mergeTo), pg.Array(c.mergeMethod), pg.Array(c.mergeShared), runID}},
		{len(c.fpHex) == 0, `
			INSERT INTO public.author_entity_assignment (source_fingerprint, extractor_version, ordinal, entity_id, method, rule)
			SELECT decode(f, 'hex'), v, o, e, m, r
			FROM unnest(?::text[], ?::text[], ?::smallint[], ?::uuid[], ?::text[], ?::text[]) AS u(f, v, o, e, m, r)
			ON CONFLICT (source_fingerprint, extractor_version, ordinal) DO UPDATE
			SET entity_id = EXCLUDED.entity_id, method = EXCLUDED.method, rule = EXCLUDED.rule, updated_at = now()
			WHERE author_entity_assignment.method <> 'manual'`,
			[]interface{}{pg.Array(c.fpHex), pg.Array(c.fpVersion), pg.Array(c.fpOrdinal), pg.Array(c.fpEntity),
				pg.Array(c.fpMethod), pg.Array(c.fpRule)}},
		{len(c.delFPHex) == 0, `
			DELETE FROM public.author_entity_assignment a
			USING unnest(?::text[], ?::text[], ?::smallint[]) AS d(f, v, o)
			WHERE a.source_fingerprint = decode(d.f, 'hex') AND a.extractor_version = d.v AND a.ordinal = d.o
				AND a.method <> 'manual'`,
			[]interface{}{pg.Array(c.delFPHex), pg.Array(c.delFPVersion), pg.Array(c.delFPOrdinal)}},
		{len(c.crID) == 0, `
			INSERT INTO public.author_entity_credit_assignment (credit_id, ordinal, entity_id, method, rule)
			SELECT * FROM unnest(?::bigint[], ?::smallint[], ?::uuid[], ?::text[], ?::text[])
			ON CONFLICT (credit_id, ordinal) DO UPDATE
			SET entity_id = EXCLUDED.entity_id, method = EXCLUDED.method, rule = EXCLUDED.rule, updated_at = now()
			WHERE author_entity_credit_assignment.method <> 'manual'`,
			[]interface{}{pg.Array(c.crID), pg.Array(c.crOrdinal), pg.Array(c.crEntity), pg.Array(c.crMethod), pg.Array(c.crRule)}},
		{len(c.delCrID) == 0, `
			DELETE FROM public.author_entity_credit_assignment a
			USING unnest(?::bigint[], ?::smallint[]) AS d(id, o)
			WHERE a.credit_id = d.id AND a.ordinal = d.o AND a.method <> 'manual'`,
			[]interface{}{pg.Array(c.delCrID), pg.Array(c.delCrOrdinal)}},
	}
}

// AuthorEntityBuildRun is one build pass.
type AuthorEntityBuildRun struct {
	ID           int64   `pg:"id" json:"id"`
	Status       string  `pg:"status" json:"status"`
	Stage        *string `pg:"stage" json:"stage,omitempty"`
	UnitsTotal   int     `pg:"units_total,use_zero" json:"units_total"`
	UnitsDone    int     `pg:"units_done,use_zero" json:"units_done"`
	UnitsSkipped int     `pg:"units_skipped,use_zero" json:"units_skipped"`
	Changes      int64   `pg:"changes,use_zero" json:"changes"`
	// Attempts is how many times the pass was planned.
	Attempts        int             `pg:"attempts,use_zero" json:"attempts"`
	Report          json.RawMessage `pg:"report,type:jsonb" json:"report,omitempty" swaggertype:"object"`
	LastErrorClass  *string         `pg:"last_error_class" json:"last_error_class,omitempty"`
	CreatedByUserID *int64          `pg:"created_by_user_id" json:"-"`
	CreatedAt       time.Time       `pg:"created_at" json:"created_at"`
	StartedAt       *time.Time      `pg:"started_at" json:"started_at,omitempty"`
	FinishedAt      *time.Time      `pg:"finished_at" json:"finished_at,omitempty"`
}

const authorEntityRunColumns = `id, status, stage, units_total, units_done, units_skipped, changes, attempts, report,
	last_error_class, created_by_user_id, created_at, started_at, finished_at`

// Build run statuses and stages.
const (
	AuthorEntityRunPending   = "pending"
	AuthorEntityRunRunning   = "running"
	AuthorEntityRunCompleted = "completed"
	AuthorEntityRunFailed    = "failed"

	AuthorEntityStagePlan   = "plan"
	AuthorEntityStageApply  = "apply"
	AuthorEntityStageReport = "report"
)

// ErrAuthorEntityBuildActive refuses a second pass while one is pending or
// running.
var ErrAuthorEntityBuildActive = errors.New("database: an author entity build is already active")

// ErrAuthorEntityLeaseLost is a worker's write after another worker took the
// pass over: the write is not made.
var ErrAuthorEntityLeaseLost = errors.New("database: the author entity build lease was lost")

// StartAuthorEntityBuild records a new pending pass; one already pending or
// running refuses it.
func StartAuthorEntityBuild(ctx context.Context, db pg.DBI, actor int64) (*AuthorEntityBuildRun, error) {
	var by interface{}
	if actor > 0 {
		by = actor
	}
	var runs []AuthorEntityBuildRun
	if _, err := db.QueryContext(ctx, &runs, `
		INSERT INTO public.author_entity_build_run (created_by_user_id) VALUES (?)
		ON CONFLICT ((true)) WHERE status IN ('pending', 'running') DO NOTHING
		RETURNING `+authorEntityRunColumns, by); err != nil {
		return nil, fmt.Errorf("starting an author entity build: %w", err)
	}
	if len(runs) == 0 {
		return nil, ErrAuthorEntityBuildActive
	}
	return &runs[0], nil
}

// ClaimAuthorEntityBuild takes the active pass for owner for lease: a
// pending one, a running one whose lease ran out, or one owner holds
// already. No active pass, or one another worker holds, is nil.
func ClaimAuthorEntityBuild(ctx context.Context, db pg.DBI, owner uuid.UUID, lease time.Duration) (*AuthorEntityBuildRun, error) {
	var runs []AuthorEntityBuildRun
	_, err := db.QueryContext(ctx, &runs, `
		UPDATE public.author_entity_build_run SET status = 'running', started_at = coalesce(started_at, now()),
			lease_owner = ?0, lease_expires_at = now() + ?1 * interval '1 millisecond'
		WHERE id = (
			SELECT id FROM public.author_entity_build_run
			WHERE status IN ('pending', 'running')
				AND (lease_owner IS NULL OR lease_expires_at < now() OR lease_owner = ?0)
			ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING `+authorEntityRunColumns, owner, lease.Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("claiming the author entity build: %w", err)
	}
	if len(runs) == 0 {
		return nil, nil
	}
	return &runs[0], nil
}

// AuthorEntityProgress is a pass's progress.
type AuthorEntityProgress struct {
	Stage        string
	UnitsTotal   int
	UnitsDone    int
	UnitsSkipped int
	Changes      int64
}

// BeginAuthorEntityAttempt starts owner's planning of the pass: one more
// attempt, the plan stage, the counters of an attempt that did not land
// cleared.
// A worker whose lease another took gets ErrAuthorEntityLeaseLost.
func BeginAuthorEntityAttempt(
	ctx context.Context, db pg.DBI, runID int64, owner uuid.UUID, lease time.Duration,
) (*AuthorEntityBuildRun, error) {
	var runs []AuthorEntityBuildRun
	if _, err := db.QueryContext(ctx, &runs, `
		UPDATE public.author_entity_build_run SET attempts = attempts + 1, stage = 'plan', units_total = 0,
			units_done = 0, units_skipped = 0, changes = 0, lease_expires_at = now() + ? * interval '1 millisecond'
		WHERE id = ? AND status = 'running' AND lease_owner = ?
		RETURNING `+authorEntityRunColumns, lease.Milliseconds(), runID, owner); err != nil {
		return nil, fmt.Errorf("beginning an author entity build attempt: %w", err)
	}
	if len(runs) == 0 {
		return nil, ErrAuthorEntityLeaseLost
	}
	return &runs[0], nil
}

// RecordAuthorEntityProgress writes a pass's progress and renews owner's
// lease, in the caller's transaction (with the units it counts). The run row
// is locked: a worker whose lease another took gets ErrAuthorEntityLeaseLost.
func RecordAuthorEntityProgress(
	ctx context.Context, tx pg.DBI, runID int64, owner uuid.UUID, lease time.Duration, p AuthorEntityProgress,
) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE public.author_entity_build_run SET stage = ?, units_total = ?, units_done = ?, units_skipped = ?,
			changes = ?, lease_expires_at = now() + ? * interval '1 millisecond'
		WHERE id = ? AND status = 'running' AND lease_owner = ?`,
		p.Stage, p.UnitsTotal, p.UnitsDone, p.UnitsSkipped, p.Changes, lease.Milliseconds(), runID, owner)
	if err != nil {
		return fmt.Errorf("recording the author entity build progress: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrAuthorEntityLeaseLost
	}
	return nil
}

// LockAuthorEntityBuild locks owner's running pass in the caller's
// transaction, NOWAIT: a worker whose lease another took gets
// ErrAuthorEntityLeaseLost, and while the lock is held no other worker can
// claim the pass (ClaimAuthorEntityBuild skips locked rows). It then takes
// the author layer shared, like a source writer: the pass's assignments
// reference credits, and a book's layer deletion — which takes the layer
// exclusively before it locks any credit — waits for the pass to commit
// instead of deadlocking with it on credit rows. A deletion already running
// is waited for.
func LockAuthorEntityBuild(ctx context.Context, tx pg.DBI, runID int64, owner uuid.UUID) error {
	var ids []int64
	if _, err := tx.QueryContext(ctx, &ids, `
		SELECT id FROM public.author_entity_build_run
		WHERE id = ? AND status = 'running' AND lease_owner = ? FOR UPDATE NOWAIT`, runID, owner); err != nil {
		return fmt.Errorf("locking the author entity build: %w", err)
	}
	if len(ids) == 0 {
		return ErrAuthorEntityLeaseLost
	}
	return lockAuthorLayerShared(ctx, tx)
}

// CompleteAuthorEntityBuild stores the final counters and the report and
// completes owner's pass, in the caller's transaction (the one that wrote
// the pass).
func CompleteAuthorEntityBuild(
	ctx context.Context, tx pg.DBI, runID int64, owner uuid.UUID, p AuthorEntityProgress, report []byte,
) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE public.author_entity_build_run SET status = 'completed', stage = ?, units_total = ?, units_done = ?,
			units_skipped = ?, changes = ?, report = ?::jsonb, finished_at = now(),
			lease_owner = NULL, lease_expires_at = NULL
		WHERE id = ? AND status = 'running' AND lease_owner = ?`,
		p.Stage, p.UnitsTotal, p.UnitsDone, p.UnitsSkipped, p.Changes, string(report), runID, owner)
	if err != nil {
		return fmt.Errorf("completing the author entity build: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrAuthorEntityLeaseLost
	}
	return nil
}

// FailAuthorEntityBuild ends owner's pass as failed with a closed class.
func FailAuthorEntityBuild(ctx context.Context, db pg.DBI, runID int64, owner uuid.UUID, class string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE public.author_entity_build_run SET status = 'failed', last_error_class = ?, finished_at = now(),
			lease_owner = NULL, lease_expires_at = NULL
		WHERE id = ? AND status = 'running' AND lease_owner = ?`, class, runID, owner)
	if err != nil {
		return fmt.Errorf("failing the author entity build: %w", err)
	}
	return nil
}

// LatestAuthorEntityBuilds are the newest pass and the newest completed one
// (the same run when the newest completed); either may be nil.
func LatestAuthorEntityBuilds(ctx context.Context, db pg.DBI) (latest, completed *AuthorEntityBuildRun, err error) {
	var runs []AuthorEntityBuildRun
	if _, err = db.QueryContext(ctx, &runs, `
		(SELECT `+authorEntityRunColumns+` FROM public.author_entity_build_run ORDER BY id DESC LIMIT 1)
		UNION ALL
		(SELECT `+authorEntityRunColumns+` FROM public.author_entity_build_run WHERE status = 'completed'
		 ORDER BY id DESC LIMIT 1)`); err != nil {
		return nil, nil, fmt.Errorf("reading the author entity builds: %w", err)
	}
	for i := range runs {
		r := &runs[i]
		if latest == nil {
			latest = r
			if r.Status == AuthorEntityRunCompleted {
				completed = r
				break
			}
			continue
		}
		completed = r
	}
	return latest, completed, nil
}
