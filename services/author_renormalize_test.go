package services

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/logging"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	logrustest "github.com/sirupsen/logrus/hooks/test" //nolint:depguard // tests assert on the structured log entries
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The release of a new normalizer version on a scratch database: inputs the
// previous version left waiting are read again only where the new version
// reads them differently, and a book scanned after the release is selected
// by the shipped policy end to end.

// previousReadings are the results authornorm-local-v1 gave the inputs v2
// reads differently (its golden table): the class, kind, status and flags.
// v1 kept the whole source as display and filled no field for these kinds.
var previousReadings = map[string]struct {
	class authornorm.DecisionClass
	kind  authornorm.Kind
	flags []authornorm.QualityFlag
}{
	"Микал Хем Неизвестный автор":    placeholderFragment,
	"Поль Констан Неизвестный автор": placeholderFragment,
	"Мэри Роуч Неизвестный автор":    placeholderFragment,
	"Бото Штраус Неизвестный автор":  placeholderFragment,
	"Коллектив авторов": {authornorm.ClassCollectiveCandidate, authornorm.KindCollective,
		[]authornorm.QualityFlag{authornorm.FlagUnstructured, authornorm.FlagCollectiveMarker}},
	"Сборник статей": {authornorm.ClassCollectiveCandidate, authornorm.KindCollective,
		[]authornorm.QualityFlag{authornorm.FlagUnstructured, authornorm.FlagCollectiveMarker}},
	"Сборник": {authornorm.ClassCollectiveCandidate, authornorm.KindCollective,
		[]authornorm.QualityFlag{authornorm.FlagSingleToken, authornorm.FlagCollectiveMarker}},
	"Виктор И Черкашин": {authornorm.ClassCollectiveCandidate, authornorm.KindCollective,
		[]authornorm.QualityFlag{authornorm.FlagInitials, authornorm.FlagCollectiveMarker}},
}

var placeholderFragment = struct {
	class authornorm.DecisionClass
	kind  authornorm.Kind
	flags []authornorm.QualityFlag
}{authornorm.ClassPlaceholderFragment, authornorm.KindUnknown,
	[]authornorm.QualityFlag{authornorm.FlagNickname, authornorm.FlagPlaceholderFragment}}

// previousNormalizer stands in for the previous version: its pinned reading
// where v2 differs, the shared reading everywhere else, both under the
// previous version and key.
func previousNormalizer(v authornorm.SourceValue, extractorVersion string) (authornorm.Result, error) {
	r, err := versioned(authornorm.PreviousNormalizerVersion)(v, extractorVersion)
	if err != nil {
		return r, err
	}
	old, ok := previousReadings[v.DisplayName()]
	if !ok {
		return r, nil
	}
	display := strings.Join(strings.Fields(v.DisplayName()), " ")
	return authornorm.Result{
		DisplayName: display, SearchKey: authornorm.SearchKey(display), Script: r.Script,
		Kind: old.kind, Status: authornorm.StatusUnresolved, Method: authornorm.MethodRules,
		DecisionClass: old.class, QualityFlags: old.flags,
		SourceFingerprint: r.SourceFingerprint, NormalizationKey: r.NormalizationKey,
		ExtractorVersion: r.ExtractorVersion, NormalizerVersion: r.NormalizerVersion, SchemaVersion: r.SchemaVersion,
	}, nil
}

func TestAuthorRenormalizePass(t *testing.T) {
	s := localWorkerDB(t)
	resetPipeline(t, s)
	f := &workerFixture{t: t, db: s}
	ctx := context.Background()
	hook := logrustest.NewLocal(logging.GetLogger())
	defer hook.Reset()

	// Before the release: every input read by the previous version, nothing
	// registered for it, so the placeholder waits unresolved and the rest is
	// in review.
	placeholderNick := source(t, "first", "Микал", "last", "Хем", "nickname", "Неизвестный автор")
	collective := source(t, "last", "Коллектив авторов")
	capitalI := source(t, "first", "Виктор", "middle", "И", "last", "Черкашин")
	placeholder := unknownAuthor(t)
	untouchedInitials := initialed(t)
	decided := source(t, "last", "Сборник")
	ids := f.oldBinaryScan(author(placeholderNick), author(collective),
		author(capitalI), author(placeholder), author(untouchedInitials), author(decided))
	f.beforeRelease()
	f.oldBinaryWork()
	nick, coll, capI, ph, ini, dec := ids[0], ids[1], ids[2], ids[3], ids[4], ids[5]
	for _, id := range []int64{nick, coll, capI, ini, dec} {
		require.Equal(t, models.CreditSelectionReview, f.selection(id).State, "credit %d", id)
	}
	require.Equal(t, models.CreditSelectionUnresolved, f.selection(ph).State)
	// An administrator already left one targeted input unresolved: the
	// release must not take that decision back.
	var decidedItem int64
	_, err := s.QueryOne(pg.Scan(&decidedItem), `SELECT id FROM contributor_review_item
		WHERE scope_fingerprint = ? AND status = 'open'`, fingerprintOf(decided))
	require.NoError(t, err)
	require.NoError(t, s.RunInTransaction(ctx, func(tx *pg.Tx) error {
		_, applyErr := database.ApplyReviewAction(ctx, tx, decidedItem, 1,
			database.ReviewDecision{Action: database.ReviewLeaveUnresolved})
		return applyErr
	}))

	// A book scanned just before the release: its job of the previous
	// version still waits, and the new worker never claims it.
	late := f.oldBinaryScan(author(chekhov(t)))
	f.beforeRelease()

	// The release: the new version's pairs, then the start-up pass and the
	// worker under the new version.
	f.release()
	require.NoError(t, NewAuthorAcceptancePass(s).Run(ctx))
	assert.Equal(t, 5, f.count(`SELECT count(*) FROM contributor_normalization_job WHERE normalizer_version = ?
		AND status = 'queued'`, authornorm.NormalizerVersion),
		"only the four re-read inputs and the unfinished one are queued")
	assert.Zero(t, f.retriedItems(), "the pass only queues: no item closes before its input is read again")
	f.drain(f.worker(nil))
	assert.Equal(t, 3, f.retriedItems(), "the three re-read review items close, without an actor, when read again")

	for name, id := range map[string]int64{"placeholder nickname": nick, "collective": coll, "placeholder": ph,
		"unfinished": late[0]} {
		sel := f.selection(id)
		require.Equal(t, models.CreditSelectionSelected, sel.State, name)
		assert.Equal(t, models.CreditSelectionAutomatic, *sel.Basis, name)
		var r models.ContributorNormalizationResult
		require.NoError(t, s.Model(&r).Where("id = ?", *sel.ResultID).Select())
		assert.Equal(t, authornorm.NormalizerVersion, *r.NormalizerVersion, name)
	}
	assert.Equal(t, 0, f.openReviewItems(authornorm.SourceFingerprint(placeholderNick)))
	assert.Equal(t, 0, f.openReviewItems(authornorm.SourceFingerprint(collective)))
	// The capital И is an initial now: back in review, as initials, on a
	// proposal of the new version.
	assert.Equal(t, models.CreditSelectionReview, f.selection(capI).State)
	var reopened struct {
		DecisionClass     string
		NormalizerVersion string
	}
	_, err = s.QueryOne(&reopened, `SELECT i.decision_class, r.normalizer_version FROM contributor_review_item i
		JOIN contributor_normalization_result r ON r.id = i.proposal_result_id
		WHERE i.scope_fingerprint = ? AND i.status = 'open'`, fingerprintOf(capitalI))
	require.NoError(t, err)
	assert.Equal(t, string(authornorm.ClassInitials), reopened.DecisionClass)
	assert.Equal(t, authornorm.NormalizerVersion, reopened.NormalizerVersion)

	// Classes the new version reads alike, and an administrator's decision,
	// are left exactly as they were.
	assert.Equal(t, models.CreditSelectionReview, f.selection(ini).State)
	assert.Equal(t, 1, f.openReviewItems(authornorm.SourceFingerprint(untouchedInitials)))
	sel := f.selection(dec)
	assert.Equal(t, models.CreditSelectionUnresolved, sel.State)
	assert.Equal(t, models.UnresolvedReviewLeftUnresolved, *sel.UnresolvedReason)
	for _, v := range []authornorm.SourceValue{untouchedInitials, decided} {
		assert.Zero(t, f.count(`SELECT count(*) FROM contributor_normalization_job
			WHERE source_fingerprint = ? AND normalizer_version = ?`, fingerprintOf(v), authornorm.NormalizerVersion))
	}

	// Nothing is listed again: the unfinished v1 job still waits, but its
	// input has a job of the new version.
	target := database.RenormalizeTarget{Previous: authornorm.PreviousNormalizerVersion,
		Current: authornorm.NormalizerVersion, Classes: authornorm.RereadClasses(), Since: time.Now().Add(-time.Hour)}
	for _, list := range database.RenormalizeLists() {
		listed, listErr := database.RenormalizeInputs(ctx, s, list, &target, nil, 10)
		require.NoError(t, listErr)
		assert.Empty(t, listed, "list %d", list)
	}

	// A second start queues nothing and changes nothing.
	audits := map[int64]int{}
	for _, id := range append(append([]int64{}, ids...), late...) {
		audits[id] = f.audits(id)
	}
	queued, err := RenormalizeRereadInputs(ctx, s, acceptanceApplyBatch)
	require.NoError(t, err)
	assert.Zero(t, queued)
	require.NoError(t, NewAuthorAcceptancePass(s).Run(ctx))
	f.drain(f.worker(nil))
	for id, n := range audits {
		assert.Equal(t, n, f.audits(id), "a second start changed credit %d", id)
	}

	events := eventsNamed(hook, AuthorMetadataEventRenormalizeQueued)
	require.Len(t, events, 1, "only the pass that queued something is logged")
	assert.Equal(t, 5, events[0].Data["count"])
	assert.Equal(t, "acceptance", events[0].Data["stage"])
	for _, e := range hook.AllEntries() {
		line, _ := e.String()
		for _, name := range []string{"Микал", "Хем", "Коллектив", "Черкашин", "Сборник", "Неизвестный"} {
			assert.False(t, strings.Contains(line, name), "a name reached the log: %s", e.Message)
		}
	}
}

// oldBinaryScan persists a book the way the v1 binary does: its credits'
// jobs are of the previous version and wait as pending, the only waiting
// state that binary writes and claims.
func (f *workerFixture) oldBinaryScan(credits ...database.ExtractionCredit) []int64 {
	f.t.Helper()
	_, ids := f.persist(authornorm.PreviousNormalizerVersion, credits...)
	f.exec(`UPDATE contributor_normalization_job SET status = 'pending'
		WHERE normalizer_version = ? AND status = 'queued'`, authornorm.PreviousNormalizerVersion)
	return ids
}

// oldBinaryWork runs the v1 binary's local stream until its pending jobs are
// done. It stands in for that binary with this code under the previous
// version and readings: the jobs it takes are exactly those the v1 claim
// takes (v1LocalClaimSQL, pinned by TestTheOldBinaryCannotSeeTheNewQueue),
// and at that version nothing of the new one runs — no sweep, no take-over.
func (f *workerFixture) oldBinaryWork() {
	f.t.Helper()
	f.exec(`UPDATE contributor_normalization_job SET status = 'queued'
		WHERE normalizer_version = ? AND status = 'pending'`, authornorm.PreviousNormalizerVersion)
	w := f.worker(func(c *AuthorMetadataLocalWorkerConfig) {
		c.NormalizerVersion, c.Normalize = authornorm.PreviousNormalizerVersion, previousNormalizer
	})
	for i := 0; i < 200; i++ {
		report, err := w.RunOnce(context.Background())
		require.NoError(f.t, err)
		if report.Claimed == 0 && f.count(`SELECT count(*) FROM contributor_normalization_job
			WHERE normalizer_version = ? AND status = 'queued'`, authornorm.PreviousNormalizerVersion) == 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	f.t.Fatal("the old binary's jobs are still waiting after the drain limit")
}

// release registers the new version's Cyrillic pairs, as migration 59 does.
func (f *workerFixture) release() {
	f.t.Helper()
	f.registerPair("2", authornorm.ClassStructuredPerson, authornorm.ScriptCyrillic)
	f.registerPair("2", authornorm.ClassPlaceholder, authornorm.ScriptCyrillic)
	f.registerPair("2", authornorm.ClassCollective, authornorm.ScriptCyrillic)
}

func (f *workerFixture) retriedItems() int {
	return f.count(`SELECT count(*) FROM contributor_review_item
		WHERE status = 'closed' AND resolution = 'retried' AND resolved_by_user_id IS NULL`)
}

// resultVersion is the normalizer version of the result a credit rests on.
func (f *workerFixture) resultVersion(credit int64) string {
	f.t.Helper()
	var v string
	_, err := f.db.QueryOne(pg.Scan(&v), `SELECT r.normalizer_version FROM book_contributor_credit_selection s
		JOIN contributor_normalization_result r ON r.id = s.result_id WHERE s.credit_id = ?`, credit)
	require.NoError(f.t, err)
	return v
}

// stranded counts current author credits that nothing accounts for: no
// resolution, or review without an open item.
func (f *workerFixture) stranded() int {
	return f.count(`SELECT count(*) FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
		LEFT JOIN book_contributor_credit_selection sel ON sel.credit_id = c.id
		WHERE c.role = 'author' AND (sel.credit_id IS NULL OR (sel.state = 'review'
			AND NOT EXISTS (SELECT 1 FROM contributor_review_item i
				WHERE i.status = 'open' AND i.scope_fingerprint = c.source_fingerprint)))`)
}

// A rolling update: the v1 binary keeps scanning after the new one started.
// A late scan whose job still waits, one the old binary read itself, and an
// input the old binary took back after the new version decided it are all
// read again by the new worker's own batches — no restart.
func TestALateOldScanIsReadAgainByTheNewWorker(t *testing.T) {
	s := localWorkerDB(t)
	resetPipeline(t, s)
	f := &workerFixture{t: t, db: s}
	ctx := context.Background()

	placeholderNick := source(t, "first", "Микал", "last", "Хем", "nickname", "Неизвестный автор")
	capitalI := source(t, "first", "Виктор", "middle", "И", "last", "Черкашин")
	collective := source(t, "last", "Коллектив авторов")
	before := f.oldBinaryScan(author(placeholderNick), author(capitalI), author(collective))
	taken, inReview, untouched := before[:1], before[1], before[2]
	f.beforeRelease()
	f.oldBinaryWork()
	f.release()
	require.NoError(t, NewAuthorAcceptancePass(s).Run(ctx))
	f.drain(f.worker(nil))
	require.Equal(t, authornorm.NormalizerVersion, f.resultVersion(taken[0]))
	require.Equal(t, authornorm.NormalizerVersion, f.resultVersion(inReview))
	v2Item := f.openItemOf(capitalI)

	// After the pass: a scan the old binary reads itself, one more book of
	// the input the new version already decided — the old binary's
	// reconciliation applies its stored v1 result to every credit of it —
	// and a scan it leaves waiting.
	read := f.oldBinaryScan(author(source(t, "last", "Сборник статей")))
	plain := f.oldBinaryScan(author(chekhov(t)))
	again := f.oldBinaryScan(author(placeholderNick), author(capitalI))
	f.oldBinaryWork()
	// The old binary takes back only the inputs it got a credit of, and
	// closes no item of the new reading: it never ran the take-over.
	assert.Equal(t, authornorm.NormalizerVersion, f.resultVersion(untouched), "an input the old binary did not touch")
	assert.Equal(t, authornorm.PreviousNormalizerVersion, f.resultVersion(inReview))
	assert.Equal(t, v2Item, f.openItemOf(capitalI), "the new reading's item stays open under the old binary")
	waiting := f.oldBinaryScan(author(source(t, "first", "Поль", "last", "Констан", "nickname", "Неизвестный автор")))
	require.Equal(t, authornorm.PreviousNormalizerVersion, f.resultVersion(read[0]), "the old binary read it")
	require.Equal(t, authornorm.PreviousNormalizerVersion, f.resultVersion(taken[0]),
		"the old binary took the decided input back")

	f.drain(f.worker(nil))
	for name, id := range map[string]int64{"waiting": waiting[0], "read by the old binary": read[0],
		"taken back": taken[0], "the old binary's late credit": again[0], "an old binary's plain late scan": plain[0]} {
		sel := f.selection(id)
		require.NotNil(t, sel, name)
		assert.Equal(t, models.CreditSelectionSelected, sel.State, name)
		assert.Equal(t, authornorm.NormalizerVersion, f.resultVersion(id), name)
	}
	// The capital И input is back on the new reading, in review on its own
	// item, which never closed.
	assert.Equal(t, authornorm.NormalizerVersion, f.resultVersion(inReview))
	assert.Equal(t, authornorm.NormalizerVersion, f.resultVersion(again[1]))
	assert.Equal(t, v2Item, f.openItemOf(capitalI))
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_review_item WHERE status = 'open'`))
	assert.Zero(t, f.stranded())
}

// openItemOf is the ID of the fingerprint's open review item.
func (f *workerFixture) openItemOf(v authornorm.SourceValue) int64 {
	f.t.Helper()
	var id int64
	_, err := f.db.QueryOne(pg.Scan(&id), `SELECT id FROM contributor_review_item
		WHERE scope_fingerprint = ? AND status = 'open'`, fingerprintOf(v))
	require.NoError(f.t, err)
	return id
}

// v1Claim runs the v1 binary's claim (budget 5, ten rows, a minute's lease)
// and returns the IDs it took.
func v1Claim(t *testing.T, s *pg.DB) []int64 {
	t.Helper()
	var claims []struct {
		ID             int64
		AttemptNo      int
		LeaseExpiresAt time.Time
	}
	_, err := s.Query(&claims, v1LocalClaimSQL, 5, 10, database.NewLeaseOwner(), time.Minute.Microseconds(), nil)
	require.NoError(t, err)
	ids := make([]int64, 0, len(claims))
	for _, c := range claims {
		ids = append(ids, c.ID)
	}
	return ids
}

// beforeRelease dates the waiting jobs of the previous version a day back:
// written by the v1 binary before the release, not late. A job not yet
// tried is written again with that date; the identity trigger keeps
// created_at of an existing row.
func (f *workerFixture) beforeRelease() {
	f.t.Helper()
	f.exec(`WITH gone AS (
			DELETE FROM contributor_normalization_job
			WHERE normalizer_version = ? AND status IN ('pending', 'queued') AND attempt_count = 0
			RETURNING normalization_key, source_fingerprint, extractor_version, normalizer_version, status)
		INSERT INTO contributor_normalization_job
			(normalization_key, source_fingerprint, extractor_version, normalizer_version, status, created_at)
		SELECT normalization_key, source_fingerprint, extractor_version, normalizer_version, status,
			now() - interval '1 day' FROM gone`, authornorm.PreviousNormalizerVersion)
}

func (f *workerFixture) previousKey(v authornorm.SourceValue) []byte {
	key, err := authornorm.NormalizationKey(authornorm.SourceFingerprint(v), workerExtractor,
		authornorm.PreviousNormalizerVersion)
	require.NoError(f.t, err)
	return key[:]
}

// The v1 binary's own claim and exhaustion sweep, run verbatim, take only its
// own pending jobs: never a job of the new queue, waiting or out of attempts.
func TestTheOldBinaryCannotSeeTheNewQueue(t *testing.T) {
	s := localWorkerDB(t)
	resetPipeline(t, s)
	f := &workerFixture{t: t, db: s}
	f.persistBook(author(tolstoy(t)), author(chekhov(t)))
	old := f.oldBinaryScan(author(initialed(t)))
	// One new job as if its attempts were spent and its lease ran out.
	f.exec(`UPDATE contributor_normalization_job SET attempt_count = 5
		WHERE source_fingerprint = ? AND normalizer_version = ?`, fingerprintOf(chekhov(t)), authornorm.NormalizerVersion)
	queued := f.count(`SELECT count(*) FROM contributor_normalization_job WHERE status = 'queued'`)
	require.Equal(t, 2, queued)

	_, err := s.Exec(v1LocalExhaustSQL, 1, nil)
	require.NoError(t, err)
	claimed := v1Claim(t, s)

	var oldJob int64
	_, err = s.QueryOne(pg.Scan(&oldJob), `SELECT id FROM contributor_normalization_job WHERE source_fingerprint = ?`,
		fingerprintOf(initialed(t)))
	require.NoError(t, err)
	assert.Equal(t, []int64{oldJob}, claimed, "the old claim takes its own pending job only")
	assert.Equal(t, queued, f.count(`SELECT count(*) FROM contributor_normalization_job
		WHERE status = 'queued' AND attempt_count IN (0, 5) AND lease_owner IS NULL`),
		"the new queue is untouched: not claimed, not ended, no attempt spent")
	assert.Zero(t, f.count(`SELECT count(*) FROM contributor_normalization_job_attempt a
		JOIN contributor_normalization_job j ON j.id = a.job_id WHERE j.status = 'queued'`))
	_ = old
}

// A rollback right after the release: the new pod queued its re-reads and is
// gone before running them. The v1 binary goes on: it cannot take the new
// jobs, and nothing waits on them — every re-read input keeps its item open
// and its credits, and the v1 binary's own scans run as before.
func TestARollbackStrandsNothing(t *testing.T) {
	s := localWorkerDB(t)
	resetPipeline(t, s)
	f := &workerFixture{t: t, db: s}
	ctx := context.Background()

	ids := f.oldBinaryScan(author(source(t, "first", "Микал", "last", "Хем", "nickname", "Неизвестный автор")),
		author(source(t, "last", "Коллектив авторов")), author(unknownAuthor(t)))
	f.beforeRelease()
	f.oldBinaryWork()
	f.release()
	require.NoError(t, NewAuthorAcceptancePass(s).Run(ctx))
	require.Equal(t, 3, f.count(`SELECT count(*) FROM contributor_normalization_job WHERE status = 'queued'`))
	before := map[int64]int{}
	for _, id := range ids {
		before[id] = f.audits(id)
	}

	// The v1 binary alone from here: its claim sees none of the new jobs,
	// and a book it scans now is read and resolved by it as before.
	assert.Empty(t, v1Claim(t, s))
	fresh := f.oldBinaryScan(author(chekhov(t)))
	f.oldBinaryWork()
	require.NotNil(t, f.selection(fresh[0]))

	for _, id := range ids {
		assert.Equal(t, before[id], f.audits(id), "credit %d changed under the old binary", id)
	}
	assert.Equal(t, 2, f.count(`SELECT count(*) FROM contributor_review_item WHERE status = 'open'`),
		"the re-read inputs keep their review items")
	assert.Zero(t, f.stranded())
	assert.Equal(t, 3, f.count(`SELECT count(*) FROM contributor_normalization_job WHERE status = 'queued'`),
		"the new jobs wait for a new pod")

	// Forward again: the new worker reads them as if nothing happened.
	f.drain(f.worker(nil))
	for _, id := range ids {
		assert.Equal(t, models.CreditSelectionSelected, f.selection(id).State)
	}
	assert.Zero(t, f.stranded())
}

// One input a sweep cannot queue is skipped, logged and passed over at every
// start, and a settled job of the new version — out of attempts at a budget
// of one — is applied again instead of retried: neither stops the inputs
// after it.
func TestTheSweepPassesOverWhatItCannotQueue(t *testing.T) {
	s := localWorkerDB(t)
	resetPipeline(t, s)
	f := &workerFixture{t: t, db: s}
	ctx := context.Background()
	hook := logrustest.NewLocal(logging.GetLogger())
	defer hook.Reset()

	sources := []authornorm.SourceValue{
		source(t, "first", "Микал", "last", "Хем", "nickname", "Неизвестный автор"),
		source(t, "first", "Поль", "last", "Констан", "nickname", "Неизвестный автор"),
		source(t, "first", "Мэри", "last", "Роуч", "nickname", "Неизвестный автор"),
		source(t, "first", "Бото", "last", "Штраус", "nickname", "Неизвестный автор"),
	}
	credits := make([]database.ExtractionCredit, 0, len(sources))
	for _, v := range sources {
		credits = append(credits, author(v))
	}
	ids := f.oldBinaryScan(credits...)
	f.beforeRelease()
	f.oldBinaryWork()
	f.release()
	budgetOfOne := func(c *AuthorMetadataLocalWorkerConfig) { c.Retry.MaxAttempts = 1 }

	// One input cannot be queued at all; the worker's own sweeps meet it too.
	broken := fingerprintOf(sources[2])
	f.exec(`CREATE FUNCTION renormalize_refuse() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.source_fingerprint = '\x` + hex.EncodeToString(broken) + `'::bytea AND NEW.normalizer_version = '` +
		authornorm.NormalizerVersion + `' THEN
				RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'refused';
			END IF;
			RETURN NEW;
		END $$`)
	f.exec(`CREATE TRIGGER renormalize_refuse BEFORE INSERT ON contributor_normalization_job
		FOR EACH ROW EXECUTE FUNCTION renormalize_refuse()`)
	t.Cleanup(func() {
		_, _ = s.Exec(`DROP TRIGGER IF EXISTS renormalize_refuse ON contributor_normalization_job`)
	})

	// The first input was read under the new version before the release's
	// pass and then taken back by the old binary: its new job is settled,
	// with the one attempt the budget allows.
	settled := sources[0]
	f.persistBook(author(settled))
	f.drain(f.worker(budgetOfOne))
	require.Equal(t, authornorm.NormalizerVersion, f.resultVersion(ids[0]))
	f.exec(`UPDATE book_contributor_credit_selection SET result_id = (SELECT id FROM contributor_normalization_result
			WHERE normalization_key = ?), state = 'review', basis = NULL, policy_version = NULL
		WHERE credit_id = ?`, f.previousKey(settled), ids[0])
	f.exec(`INSERT INTO contributor_review_item (scope_fingerprint, source_fingerprint, reason, decision_class,
			proposal_result_id)
		SELECT ?0, ?0, 'ambiguous_decision', 'placeholder_fragment', id FROM contributor_normalization_result
		WHERE normalization_key = ?1`, fingerprintOf(settled), f.previousKey(settled))

	for start := 0; start < 2; start++ {
		require.NoError(t, NewAuthorAcceptancePass(s).Run(ctx), "start %d", start)
	}
	f.drain(f.worker(budgetOfOne))

	for i, id := range ids {
		if i == 2 {
			continue
		}
		assert.Equal(t, models.CreditSelectionSelected, f.selection(id).State, "input %d", i)
		assert.Equal(t, authornorm.NormalizerVersion, f.resultVersion(id), "input %d", i)
	}
	assert.Equal(t, models.CreditSelectionReview, f.selection(ids[2]).State, "the refused input keeps its reading")
	assert.Equal(t, 1, f.openReviewItems(authornorm.SourceFingerprint(sources[2])), "and its open item")
	assert.Equal(t, 0, f.openReviewItems(authornorm.SourceFingerprint(settled)))
	assert.NotEmpty(t, eventsNamed(hook, AuthorMetadataEventRenormalizeSkipped))
	assert.Zero(t, f.stranded())
}

func fingerprintOf(v authornorm.SourceValue) []byte {
	fp := authornorm.SourceFingerprint(v)
	return fp[:]
}

// shippedFB2 is a book with one author of every class the shipped policy
// selects, and the shapes v2 reads differently.
const shippedFB2 = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description><title-info>
<author><first-name>Лев</first-name><last-name>Толстой</last-name></author>
<author><first-name>Arthur</first-name><last-name>Doyle</last-name></author>
<author><first-name>Микал</first-name><last-name>Хем</last-name><nickname>Неизвестный автор</nickname></author>
<author><last-name>Коллектив авторов</last-name></author>
<author><last-name>Автор неизвестен</last-name></author>
<author><nickname>Anonymous</nickname></author>
<book-title>Сборник</book-title>
<lang>ru</lang>
</title-info></description><body><p>текст</p></body></FictionBook>`

// A book scanned after the release goes through the live scan's writer, the
// local job and the shipped policy of migration 59 — no registration seeded
// by the test — and every credit is selected automatically.
func TestScannedBookIsSelectedByTheShippedPolicy(t *testing.T) {
	s := localWorkerDB(t)
	f := &workerFixture{t: t, db: s}
	ctx := context.Background()

	writer := NewAuthorMetadataSourceWriter()
	md5 := strings.Repeat("c", 32)
	prepared, err := writer.Prepare([]byte(shippedFB2), "new.zip", "new.fb2", md5)
	require.NoError(t, err)
	require.True(t, prepared.Extracted())
	var book int64
	_, err = s.QueryOne(pg.Scan(&book), `INSERT INTO opds_catalog_book
		(filename, path, format, registerdate, docdate, lang, title, annotation, md5)
		VALUES ('new.fb2', 'new.zip', 'fb2', now(), '', 'ru', 'catalog', '', ?) RETURNING id`, md5)
	require.NoError(t, err)
	require.NoError(t, s.RunInTransaction(ctx, func(tx *pg.Tx) error { return writer.Persist(tx, book, prepared) }))
	f.drain(f.worker(nil))

	var credits []struct {
		Position      int
		State         string
		Basis         *string
		PolicyVersion *string
		DecisionClass string
		Normalizer    string
	}
	_, err = s.Query(&credits, `SELECT c.position, sel.state, sel.basis, sel.policy_version,
			r.decision_class, r.normalizer_version AS normalizer
		FROM book_contributor_credit c
		JOIN book_metadata_snapshot sn ON sn.id = c.snapshot_id AND sn.is_current
		JOIN book_contributor_credit_selection sel ON sel.credit_id = c.id
		JOIN contributor_normalization_result r ON r.id = sel.result_id
		WHERE sn.book_id = ? AND c.role = 'author' ORDER BY c.position`, book)
	require.NoError(t, err)
	want := []authornorm.DecisionClass{
		authornorm.ClassStructuredPerson, authornorm.ClassStructuredPerson, authornorm.ClassStructuredPerson,
		authornorm.ClassCollective, authornorm.ClassPlaceholder, authornorm.ClassPlaceholder,
	}
	require.Len(t, credits, len(want))
	for i, c := range credits {
		assert.Equal(t, string(models.CreditSelectionSelected), c.State, "author %d", i)
		require.NotNil(t, c.Basis, "author %d", i)
		assert.Equal(t, string(models.CreditSelectionAutomatic), *c.Basis, "author %d", i)
		require.NotNil(t, c.PolicyVersion, "author %d", i)
		assert.Equal(t, "3", *c.PolicyVersion, "author %d is selected under migration 59's version", i)
		assert.Equal(t, string(want[i]), c.DecisionClass, "author %d", i)
		assert.Equal(t, authornorm.NormalizerVersion, c.Normalizer, "author %d", i)
	}
}

// tolstoyFB2 is a book with one plain author.
const tolstoyFB2 = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description><title-info>
<author><first-name>Лев</first-name><last-name>Толстой</last-name></author>
<book-title>Война и мир</book-title><lang>ru</lang>
</title-info></description><body><p>текст</p></body></FictionBook>`

// A rollback with a run of the new version: the v1 binary's run-admin retry,
// run verbatim, reopens the run's failed job as pending — the waiting state
// of its own claim. The row is written as queued instead, so the old claim
// never takes it, and the new worker's next batch runs it to a resolution.
// A v1 binary's own pending job stays pending.
func TestTheOldRunRetryReopensANewJobIntoTheNewQueue(t *testing.T) {
	s := localWorkerDB(t)
	resetPipeline(t, s)
	f := &workerFixture{t: t, db: s}
	ctx := context.Background()
	f.release()

	dir := t.TempDir()
	privacyArchive(t, dir, "tolstoy.zip", map[string]string{"tolstoy.fb2": tolstoyFB2})
	run := f.privacyRun("tolstoy.zip", "tolstoy.fb2")
	require.Equal(t, authornorm.NormalizerVersion, run.NormalizerVersion)
	drainExtraction(t, ctx, privacyExtraction(t, s, dir, ZipArchiveSource{}, productionExtractor()))
	failing := func(authornorm.SourceValue, string) (authornorm.Result, error) {
		return authornorm.Result{}, errCanary
	}
	f.drain(f.worker(func(c *AuthorMetadataLocalWorkerConfig) { c.Normalize, c.Retry.MaxAttempts = failing, 2 }))
	var job struct {
		ID     int64
		Status string
	}
	_, err := s.QueryOne(&job, `SELECT id, status FROM contributor_normalization_job WHERE normalizer_version = ?`,
		authornorm.NormalizerVersion)
	require.NoError(t, err)
	require.Equal(t, "failed", job.Status)
	var credit int64
	_, err = s.QueryOne(pg.Scan(&credit), `SELECT c.id FROM book_contributor_credit c
		JOIN book_metadata_snapshot sn ON sn.id = c.snapshot_id AND sn.is_current WHERE c.role = 'author'`)
	require.NoError(t, err)
	require.Equal(t, models.CreditSelectionUnresolved, f.selection(credit).State)

	// The rollback: the v1 binary's retry of the run's failed local jobs.
	res, err := s.Exec(v1ReopenLocalSQL, run.ID, string(AuthorMetadataErrorNormalizerFailed), 5)
	require.NoError(t, err)
	require.Equal(t, 1, res.RowsAffected(), "the old retry reopens the job")
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_normalization_job WHERE id = ? AND status = 'queued'`,
		job.ID), "written as queued, not pending")
	assert.Empty(t, v1Claim(t, s), "the old claim does not see it")
	attempts := f.count(`SELECT count(*) FROM contributor_normalization_job_attempt WHERE job_id = ?`, job.ID)

	// Forward again: the new worker's batch runs it.
	f.drain(f.worker(nil))
	sel := f.selection(credit)
	assert.Equal(t, models.CreditSelectionSelected, sel.State)
	assert.Equal(t, authornorm.NormalizerVersion, f.resultVersion(credit))
	assert.Equal(t, attempts+1, f.count(`SELECT count(*) FROM contributor_normalization_job_attempt WHERE job_id = ?`,
		job.ID), "one more attempt, appended to the history")

	// The v1 binary's own writes are untouched: its pending insert stays
	// pending for its claim.
	old := f.oldBinaryScan(author(chekhov(t)))
	require.NotEmpty(t, old)
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_normalization_job
		WHERE normalizer_version = ? AND status = 'pending'`, authornorm.PreviousNormalizerVersion))
	assert.Len(t, v1Claim(t, s), 1)
}
