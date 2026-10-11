package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/internal/authornorm/llmres"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The eval run inside the service (design K, section 8). An admin starts it;
// it runs in the background, one stage after another, each resumable after a
// pause or a restart because a stage's work is its jobs in the database:
//
//	freeze      — the first eval run samples the frozen sets (holdout, dev,
//	              anchor, canary, the fingerprint item) and reads their excerpts;
//	              later runs find them frozen and reuse them;
//	fingerprint — the fixed request, three times to every participant: the
//	              model ids V0 accepts from now on, the fingerprint references,
//	              and the check that the labs differ;
//	anchor      — the anchor set to all four; a failure ends the run with a
//	              report and no decision, before the money is spent;
//	judge       — both judges on every item, with the excerpt where relevant;
//	candidates  — the production pair on every item, without the excerpt
//	              (arm A) and with it (arm B), one item per request and ten;
//	repeat      — part of the items once more to every participant;
//	report      — llmeval computes the report and the decision; the report is
//	              stored with its digest and the run completes.
//
// The driver does the stages; the LLM worker answers the jobs.

// Stages of an eval run.
const (
	evalStageFreeze      = "freeze"
	evalStageFingerprint = "fingerprint"
	evalStageAnchor      = "anchor"
	evalStageJudge       = "judge"
	evalStageCandidates  = "candidates"
	evalStageRepeat      = "repeat"
	evalStageReport      = "report"
)

// Set versions of the frozen sets. The salt and the version are the method:
// a new sampling is a new version, never a rewrite.
const (
	authorLLMEvalSalt         = "authornorm-llm-eval-v1"
	authorLLMEvalSetPrefix    = "v1."
	authorLLMEvalStratumSet   = authorLLMEvalSetPrefix + "s."
	authorLLMEvalAnchorSet    = authorLLMEvalSetPrefix + "anchor"
	authorLLMEvalCanarySet    = authorLLMEvalSetPrefix + "canary"
	authorLLMEvalFingerprints = authorLLMEvalSetPrefix + "fingerprint"
	// authorLLMEvalPilotStratum is the one stratum a pilot run freezes and
	// measures.
	authorLLMEvalPilotStratum = "initials.cyrl"
	// authorLLMHomoglyphStratum gathers the items the homoglyph rule would
	// repair, whatever their class.
	authorLLMHomoglyphStratum = "homoglyph"
	// authorLLMNoScript names the script of an input the normalizer gave
	// none.
	authorLLMNoScript = "none"
	// authorLLMEvalTokensPerJob is the token estimate of one eval job, an
	// upper figure from the rehearsal (the judge with reasoning and an
	// excerpt).
	authorLLMEvalTokensPerJob = 4000
)

// AuthorLLMEvalSizes are the sizes of the method (design K, section 8).
type AuthorLLMEvalSizes struct {
	Holdout, Dev, Anchor, Canary, Repeat, FingerprintRepeats int
	// AnchorMinSupport and AnchorMinSharePercent: the catalog statistics
	// that confirm an anchor's fields.
	AnchorMinSupport, AnchorMinSharePercent int
}

// The design's sizes.
const (
	evalHoldoutItems       = 900
	evalDevItems           = 100
	evalAnchorItems        = 300
	evalCanaryItems        = 50
	evalRepeatItems        = 200
	evalFingerprintRepeats = 3
	evalAnchorMinSupport   = 20
	evalAnchorMinShare     = 95
	// evalParticipants is the number of participants of a configuration.
	evalParticipants = 4
	// evalJudges is the number of judges among them.
	evalJudges = 2
	// evalArms is the number of arms of the excerpt ablation.
	evalArms = 2
)

// DefaultAuthorLLMEvalSizes are the design's sizes.
func DefaultAuthorLLMEvalSizes() AuthorLLMEvalSizes {
	return AuthorLLMEvalSizes{Holdout: evalHoldoutItems, Dev: evalDevItems, Anchor: evalAnchorItems,
		Canary: evalCanaryItems, Repeat: evalRepeatItems, FingerprintRepeats: evalFingerprintRepeats,
		AnchorMinSupport: evalAnchorMinSupport, AnchorMinSharePercent: evalAnchorMinShare}
}

// AuthorLLMEvalSettings are what an eval run is started with.
type AuthorLLMEvalSettings struct {
	// OutputModes is per slot the output mode the participant answers in;
	// a missing slot answers in json_schema.
	OutputModes map[string]string `json:"output_modes"`
	// BatchSizes are the items-per-request values the candidates are
	// compared at.
	BatchSizes []int `json:"batch_sizes"`
}

// DefaultAuthorLLMEvalSettings compares one item per request with ten.
func DefaultAuthorLLMEvalSettings() AuthorLLMEvalSettings {
	return AuthorLLMEvalSettings{OutputModes: map[string]string{}, BatchSizes: []int{1, 10}}
}

// mode is the output mode the run's settings name for a slot, or "" when
// they name none.
func (s *AuthorLLMEvalSettings) mode(slot llmreq.Slot) string {
	return s.OutputModes[string(slot)]
}

// mode is the output mode a run asks a participant in: the run's own setting
// when it names one (a dev run started through the API with modes), else the
// mode its fingerprint stage negotiated, else json_schema — the first mode
// the fingerprint stage tries.
func (d *AuthorLLMEvalDriver) mode(run *evalRun, slot llmreq.Slot) string {
	if m := run.Settings.mode(slot); m != "" {
		return m
	}
	if ref, ok := run.Reference[string(slot)]; ok && ref.OutputMode != "" {
		return ref.OutputMode
	}
	return database.AuthorLLMDefaultOutputMode
}

// validate refuses settings no stage could be done with: an unknown slot or
// output mode, or no batch size within 1..20.
func (s *AuthorLLMEvalSettings) validate() error {
	for slot, mode := range s.OutputModes {
		if !llmreq.Slot(slot).Valid() {
			return fmt.Errorf("services: unknown eval participant %q", slot)
		}
		if mode != "" && !slices.Contains(database.AuthorLLMOutputModes(), mode) {
			return fmt.Errorf("services: unknown output mode %q", mode)
		}
	}
	if len(s.BatchSizes) == 0 || slices.ContainsFunc(s.BatchSizes, func(b int) bool { return b < 1 || b > 20 }) {
		return errors.New("services: eval batch sizes must be within 1..20")
	}
	return nil
}

// StartAuthorLLMEval starts an eval run of a configuration; it returns at
// once, the stages run in the background. Nil settings are the defaults;
// invalid settings never become a run.
func StartAuthorLLMEval(
	ctx context.Context, db *pg.DB, version, mode string, settings *AuthorLLMEvalSettings, adminID *int64,
) (int64, error) {
	if mode != models.AuthorLLMEvalModeDev && mode != models.AuthorLLMEvalModePilot && mode != models.AuthorLLMEvalModeFull {
		return 0, fmt.Errorf("services: unknown eval mode %q", mode)
	}
	if settings == nil {
		defaults := DefaultAuthorLLMEvalSettings()
		settings = &defaults
	}
	if err := settings.validate(); err != nil {
		return 0, err
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return 0, err
	}
	return database.CreateAuthorLLMEvalRun(ctx, db, version, mode, raw, adminID)
}

// AuthorLLMEvalDriver drives the running eval run through its stages.
type AuthorLLMEvalDriver struct {
	db          *pg.DB
	identity    llmreq.Identity
	version     string
	sizes       AuthorLLMEvalSizes
	archivesDir string
	archives    ArchiveSource
	poll        time.Duration
}

// NewAuthorLLMEvalDriver builds the driver of a configuration.
func NewAuthorLLMEvalDriver(
	db *pg.DB, identity *llmreq.Identity, version string, sizes AuthorLLMEvalSizes,
	archivesDir string, archives ArchiveSource, poll time.Duration,
) *AuthorLLMEvalDriver {
	if archives == nil {
		archives = ZipArchiveSource{}
	}
	if poll <= 0 {
		poll = authorLLMPollInterval
	}
	return &AuthorLLMEvalDriver{db: db, identity: *identity, version: version, sizes: sizes,
		archivesDir: archivesDir, archives: archives, poll: poll}
}

// EvalDriver is the eval driver of the worker's configuration, with the
// design's sizes and the worker's archives.
func (w *AuthorLLMWorker) EvalDriver() *AuthorLLMEvalDriver {
	return NewAuthorLLMEvalDriver(w.db, &w.cfg.Identity, w.cfg.ConfigVersion, DefaultAuthorLLMEvalSizes(),
		w.cfg.ArchivesDir, w.cfg.Archives, w.cfg.PollInterval)
}

// authorLLMEvalDriverName is the driver's log label.
const authorLLMEvalDriverName = "author_llm_eval"

// Name is the driver's log label.
func (d *AuthorLLMEvalDriver) Name() string { return authorLLMEvalDriverName }

// Run steps the running eval run until ctx is canceled.
func (d *AuthorLLMEvalDriver) Run(ctx context.Context) error {
	return pollLoop(ctx, authorLLMEvalDriverName, d.poll, d.Step)
}

// evalRun is the running eval run as the driver reads it.
type evalRun struct {
	ID       int64
	Mode     string
	Stage    string
	Settings AuthorLLMEvalSettings
	// Reference is what the fingerprint stage learned per participant: its
	// prompt tokens and its negotiated output mode.
	Reference map[string]database.AuthorLLMFingerprintReference
}

// runningEval reads the configuration's running eval run, if any. A paused
// run is not stepped: its stage waits.
func (d *AuthorLLMEvalDriver) runningEval(ctx context.Context) (*evalRun, error) {
	var row struct {
		ID        int64           `pg:"id"`
		Mode      string          `pg:"mode"`
		Stage     *string         `pg:"stage"`
		Settings  json.RawMessage `pg:"settings"`
		Reference json.RawMessage `pg:"reference"`
	}
	_, err := d.db.QueryOneContext(ctx, &row, `
		SELECT id, mode, stage, settings, reference FROM author_llm_run
		WHERE kind = 'eval' AND status = 'running' AND config_version = ?`, d.version)
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the running eval run: %w", err)
	}
	run := &evalRun{ID: row.ID, Mode: row.Mode, Settings: DefaultAuthorLLMEvalSettings()}
	if row.Stage != nil {
		run.Stage = *row.Stage
	}
	if len(row.Settings) > 0 {
		if err := json.Unmarshal(row.Settings, &run.Settings); err != nil {
			return nil, fmt.Errorf("reading an eval run's settings: %w", err)
		}
	}
	if len(row.Reference) > 0 && string(row.Reference) != "null" {
		if err := json.Unmarshal(row.Reference, &run.Reference); err != nil {
			return nil, fmt.Errorf("reading an eval run's reference: %w", err)
		}
	}
	return run, nil
}

// authorLLMEvalStageInvalid is the class of a run ended because its stored
// settings make a stage impossible.
const authorLLMEvalStageInvalid = "stage_invalid"

// Step does the running eval run's current stage as far as it can now, and
// moves it on when the stage is done. It reports whether it moved a stage.
// A stage the database refuses as invalid data (SQLSTATE class 22 or 23)
// would be refused at every poll: the run ends as failed instead of holding
// the one active slot forever.
func (d *AuthorLLMEvalDriver) Step(ctx context.Context) (bool, error) {
	run, err := d.runningEval(ctx)
	if err != nil || run == nil {
		return false, err
	}
	next, err := d.stage(ctx, run)
	if err != nil {
		if !stageImpossible(err) {
			return false, err
		}
		LogAuthorMetadataEvent(AuthorMetadataEventError, &AuthorMetadataEvent{
			Name: AuthorMetadataEventLLMEvalRunFailed, Stage: AuthorMetadataStageRunner, RunID: run.ID,
			Worker: authorLLMEvalDriverName, SQLState: AuthorMetadataSQLState(err)})
		if failErr := database.FailAuthorLLMRun(ctx, d.db, run.ID, authorLLMEvalStageInvalid); failErr != nil {
			return false, errors.Join(err, failErr)
		}
		return false, nil
	}
	if next == "" {
		return false, nil
	}
	return database.SetAuthorLLMRunStage(ctx, d.db, run.ID, run.Stage, next)
}

// stageImpossible reports an error the database raises for the data itself
// (data exception, integrity constraint): retrying the same stage cannot
// succeed.
func stageImpossible(err error) bool {
	state := AuthorMetadataSQLState(err)
	return strings.HasPrefix(state, "22") || strings.HasPrefix(state, "23")
}

// stage does one stage; it returns the next stage once this one is done.
func (d *AuthorLLMEvalDriver) stage(ctx context.Context, run *evalRun) (string, error) {
	switch run.Stage {
	case evalStageFreeze:
		if err := d.freeze(ctx, run); err != nil {
			return "", err
		}
		return evalStageFingerprint, nil
	case evalStageFingerprint:
		return d.fingerprintStage(ctx, run)
	case evalStageAnchor:
		return d.anchorStage(ctx, run)
	case evalStageJudge:
		return d.next(ctx, run, models.AuthorLLMPurposeEvalJudge, d.enqueueJudges, evalStageCandidates)
	case evalStageCandidates:
		return d.next(ctx, run, models.AuthorLLMPurposeEvalCandidate, d.enqueueCandidates, evalStageRepeat)
	case evalStageRepeat:
		return d.next(ctx, run, models.AuthorLLMPurposeEvalRepeat, d.enqueueRepeat, evalStageReport)
	case evalStageReport:
		return "", d.report(ctx, run)
	}
	return "", nil
}

// fingerprintStage measures the fingerprints; a failure goes straight to the
// report.
func (d *AuthorLLMEvalDriver) fingerprintStage(ctx context.Context, run *evalRun) (string, error) {
	done, err := d.awaitStage(ctx, run, models.AuthorLLMPurposeEvalFingerprint, d.enqueueFingerprint)
	if !done || err != nil {
		return "", err
	}
	ok, err := d.learnFingerprint(ctx, run)
	if err != nil {
		return "", err
	}
	if !ok {
		return evalStageReport, nil
	}
	return evalStageAnchor, nil
}

// anchorStage measures the anchor set; a failure goes straight to the
// report, before the judges and the candidates spend anything.
func (d *AuthorLLMEvalDriver) anchorStage(ctx context.Context, run *evalRun) (string, error) {
	done, err := d.awaitStage(ctx, run, models.AuthorLLMPurposeEvalAnchor, d.enqueueAnchor)
	if !done || err != nil {
		return "", err
	}
	data, err := d.collect(ctx, run)
	if err != nil {
		return "", err
	}
	if len(llmeval.AnchorReasons(data.anchor)) > 0 {
		return evalStageReport, nil
	}
	return evalStageJudge, nil
}

func (d *AuthorLLMEvalDriver) next(
	ctx context.Context, run *evalRun, purpose models.AuthorLLMJobPurpose,
	enqueue func(context.Context, *pg.Tx, *evalRun) error, next string,
) (string, error) {
	done, err := d.awaitStage(ctx, run, purpose, enqueue)
	if !done || err != nil {
		return "", err
	}
	return next, nil
}

// awaitStage enqueues a stage's jobs once — all of them in one transaction,
// so a stage either has its whole work or none — and reports whether they
// are all done. The transaction first share-locks the run's row: a pause
// landing mid-enqueue waits for the commit instead of splitting the stage
// (each INSERT checks the status, and a pause between two of them would keep
// the first and drop the rest, for good). A run already paused gets nothing
// and its stage is not done. The lock is short: the enqueue reads only the
// frozen items, never an archive.
func (d *AuthorLLMEvalDriver) awaitStage(
	ctx context.Context, run *evalRun, purpose models.AuthorLLMJobPurpose,
	enqueue func(context.Context, *pg.Tx, *evalRun) error,
) (bool, error) {
	var started bool
	if _, err := d.db.QueryOneContext(ctx, pg.Scan(&started),
		`SELECT EXISTS (SELECT 1 FROM author_llm_job WHERE run_id = ?0 AND purpose = ?1)`, run.ID, string(purpose)); err != nil {
		return false, fmt.Errorf("looking for a stage's jobs: %w", err)
	}
	if !started {
		running := false
		if err := database.RunAuthorLLMTx(ctx, d.db, func(tx *pg.Tx) error {
			var err error
			if running, err = database.LockAuthorLLMRunRunning(ctx, tx, run.ID); err != nil || !running {
				return err
			}
			return enqueue(ctx, tx, run)
		}); err != nil || !running {
			return false, err
		}
	}
	left, err := database.AuthorLLMEvalUnfinished(ctx, d.db, run.ID, purpose)
	return err == nil && left == 0, err
}

// scope lists the set versions and item sets a run measures.
func (d *AuthorLLMEvalDriver) scope(ctx context.Context, run *evalRun) ([]string, []models.AuthorLLMEvalItemSet, error) {
	sets := []models.AuthorLLMEvalItemSet{models.AuthorLLMEvalHoldout, models.AuthorLLMEvalDev}
	if run.Mode == models.AuthorLLMEvalModeDev {
		sets = []models.AuthorLLMEvalItemSet{models.AuthorLLMEvalDev}
	}
	if run.Mode == models.AuthorLLMEvalModePilot {
		return []string{authorLLMEvalStratumSet + authorLLMEvalPilotStratum}, sets, nil
	}
	frozen, err := database.FrozenAuthorLLMEvalSets(ctx, d.db)
	if err != nil {
		return nil, nil, err
	}
	var versions []string
	for v := range frozen {
		if strings.HasPrefix(v, authorLLMEvalStratumSet) {
			versions = append(versions, v)
		}
	}
	slices.Sort(versions)
	return versions, sets, nil
}

func (d *AuthorLLMEvalDriver) scopeItems(ctx context.Context, run *evalRun) ([]database.AuthorLLMEvalItemRow, error) {
	versions, sets, err := d.scope(ctx, run)
	if err != nil {
		return nil, err
	}
	return database.AuthorLLMEvalItems(ctx, d.db, versions, sets)
}

func itemIDs(items []database.AuthorLLMEvalItemRow) []int64 {
	ids := make([]int64, len(items))
	for i := range items {
		ids[i] = items[i].ID
	}
	return ids
}

func (d *AuthorLLMEvalDriver) enqueue(ctx context.Context, tx *pg.Tx, spec *database.AuthorLLMEvalJobSpec) error {
	spec.ConfigVersion = d.version
	if spec.BatchSize == 0 {
		spec.BatchSize = 1
	}
	_, err := database.EnqueueAuthorLLMEvalJobs(ctx, tx, spec)
	return err
}

func (d *AuthorLLMEvalDriver) enqueueFingerprint(ctx context.Context, tx *pg.Tx, run *evalRun) error {
	items, err := database.AuthorLLMEvalItems(ctx, tx, []string{authorLLMEvalFingerprints},
		[]models.AuthorLLMEvalItemSet{models.AuthorLLMEvalFingerprint})
	if err != nil {
		return err
	}
	for _, slot := range llmreq.Slots() {
		for r := range d.sizes.FingerprintRepeats {
			if err := d.enqueue(ctx, tx, &database.AuthorLLMEvalJobSpec{RunID: run.ID, Slot: string(slot),
				Purpose: models.AuthorLLMPurposeEvalFingerprint, RepeatNo: r, OutputMode: d.mode(run, slot),
				ItemIDs: itemIDs(items)}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *AuthorLLMEvalDriver) enqueueAnchor(ctx context.Context, tx *pg.Tx, run *evalRun) error {
	items, err := database.AuthorLLMEvalItems(ctx, tx, []string{authorLLMEvalAnchorSet},
		[]models.AuthorLLMEvalItemSet{models.AuthorLLMEvalAnchor})
	if err != nil {
		return err
	}
	for _, slot := range llmreq.Slots() {
		if err := d.enqueue(ctx, tx, &database.AuthorLLMEvalJobSpec{RunID: run.ID, Slot: string(slot),
			Purpose: models.AuthorLLMPurposeEvalAnchor, OutputMode: d.mode(run, slot),
			ItemIDs: itemIDs(items)}); err != nil {
			return err
		}
	}
	return nil
}

func (d *AuthorLLMEvalDriver) enqueueJudges(ctx context.Context, tx *pg.Tx, run *evalRun) error {
	items, err := d.scopeItems(ctx, run)
	if err != nil {
		return err
	}
	for _, slot := range []llmreq.Slot{llmreq.SlotJudgeA, llmreq.SlotJudgeB} {
		if err := d.enqueue(ctx, tx, &database.AuthorLLMEvalJobSpec{RunID: run.ID, Slot: string(slot),
			Purpose: models.AuthorLLMPurposeEvalJudge, OutputMode: d.mode(run, slot), WithContext: true,
			ItemIDs: itemIDs(items)}); err != nil {
			return err
		}
	}
	return nil
}

// enqueueCandidates asks the production pair for every combo: arm A without
// the excerpt, arm B with it (only items with a relevant excerpt — elsewhere B
// is A), at every batch size of the run.
func (d *AuthorLLMEvalDriver) enqueueCandidates(ctx context.Context, tx *pg.Tx, run *evalRun) error {
	items, err := d.scopeItems(ctx, run)
	if err != nil {
		return err
	}
	var withContext []database.AuthorLLMEvalItemRow
	for i := range items {
		if len(items[i].ContextSHA256) > 0 {
			withContext = append(withContext, items[i])
		}
	}
	for _, slot := range []llmreq.Slot{llmreq.SlotProductionA, llmreq.SlotProductionB} {
		for _, batch := range run.Settings.BatchSizes {
			for _, arm := range []string{llmeval.ArmA, llmeval.ArmB} {
				ids := itemIDs(items)
				if arm == llmeval.ArmB {
					ids = itemIDs(withContext)
				}
				if err := d.enqueue(ctx, tx, &database.AuthorLLMEvalJobSpec{RunID: run.ID, Slot: string(slot),
					Purpose: models.AuthorLLMPurposeEvalCandidate, Arm: arm, BatchSize: batch,
					OutputMode: d.mode(run, slot), WithContext: arm == llmeval.ArmB, ItemIDs: ids}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// repeatItems is the deterministic part of the scope asked twice: the first
// items of the measured set (holdout, or dev in a dev run) in hash order,
// leaving out the items already dropped (their book changed since the
// freeze): their repeats would only be dropped too. The evidence is then
// short of its size, and the report fails the run-wide check rather than
// filling it from other items.
func (d *AuthorLLMEvalDriver) repeatItems(ctx context.Context, run *evalRun) ([]database.AuthorLLMEvalItemRow, error) {
	items, err := d.scopeItems(ctx, run)
	if err != nil {
		return nil, err
	}
	answers, err := d.readEvalAnswers(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	measured := string(models.AuthorLLMEvalHoldout)
	if run.Mode == models.AuthorLLMEvalModeDev {
		measured = string(models.AuthorLLMEvalDev)
	}
	items = slices.DeleteFunc(items, func(it database.AuthorLLMEvalItemRow) bool {
		return it.ItemSet != measured || evalItemDropped(answers.byItem[it.ID])
	})
	sort.SliceStable(items, func(i, j int) bool {
		return bytes.Compare(evalOrder(items[i].SourceFingerprint), evalOrder(items[j].SourceFingerprint)) < 0
	})
	return items[:min(len(items), d.sizes.Repeat)], nil
}

// enqueueRepeat asks the repeated items once more of every participant, the
// same way as the first time: judges with the excerpt, the production pair
// without it, one item per request.
func (d *AuthorLLMEvalDriver) enqueueRepeat(ctx context.Context, tx *pg.Tx, run *evalRun) error {
	items, err := d.repeatItems(ctx, run)
	if err != nil {
		return err
	}
	for _, slot := range llmreq.Slots() {
		judge := slot == llmreq.SlotJudgeA || slot == llmreq.SlotJudgeB
		arm := llmeval.ArmA
		if judge {
			arm = ""
		}
		if err := d.enqueue(ctx, tx, &database.AuthorLLMEvalJobSpec{RunID: run.ID, Slot: string(slot),
			Purpose: models.AuthorLLMPurposeEvalRepeat, Arm: arm, RepeatNo: 1, OutputMode: d.mode(run, slot),
			WithContext: judge, ItemIDs: itemIDs(items)}); err != nil {
			return err
		}
	}
	return nil
}

// evalOrder is the sampling order: SHA-256 of the salt and the fingerprint.
func evalOrder(fingerprint []byte) []byte {
	sum := sha256.Sum256(append([]byte(authorLLMEvalSalt), fingerprint...))
	return sum[:]
}

// learnFingerprint evaluates the fingerprint stage and, when it passed,
// records what was learned on the run: from now on the run's answers are
// checked against the learned ids (V0) and its participants against the
// references (fingerprint checks).
func (d *AuthorLLMEvalDriver) learnFingerprint(ctx context.Context, run *evalRun) (bool, error) {
	data, err := d.collect(ctx, run)
	if err != nil {
		return false, err
	}
	if !data.fingerprintResult.OK {
		return false, nil
	}
	expected := map[string][]string{}
	reference := map[string]database.AuthorLLMFingerprintReference{}
	for _, p := range data.fingerprints {
		expected[p.Participant] = p.Models
		mode := data.modes[p.Participant]
		if mode == "" {
			mode = d.mode(run, llmreq.Slot(p.Participant))
		}
		reference[p.Participant] = database.AuthorLLMFingerprintReference{PromptTokens: p.PromptTokens,
			ReasoningFloor: p.ReasoningFloor, OutputMode: mode}
	}
	return true, database.SetAuthorLLMRunLearned(ctx, d.db, run.ID, expected, reference)
}

// evalData is everything the report reads, collected from the database.
type evalData struct {
	input             llmeval.Input
	anchor            []llmeval.ParticipantCount
	fingerprints      []llmeval.ParticipantFingerprint
	fingerprintResult llmeval.FingerprintResult
	dropped           int
	failed            []evalFailedJob
	// modes is each participant's negotiated output mode: the mode its
	// answered fingerprint jobs were asked in.
	modes map[string]string
}

// evalFailedJob is a job that ended without an answer, as the report shows
// it: identifiers and closed values only.
type evalFailedJob struct {
	EvalItemID  int64  `json:"eval_item_id"`
	Participant string `json:"participant"`
	Purpose     string `json:"purpose"`
	Status      string `json:"status"`
	Class       string `json:"class,omitempty"`
}

func answerOf(a *database.AuthorLLMEvalAnswer) llmeval.Answer {
	if a == nil || a.Status != string(models.AuthorLLMJobAnswered) {
		return llmeval.Answer{}
	}
	out := llmeval.Answer{Valid: true, Canonical: hex.EncodeToString(a.CanonicalSHA256)}
	if a.Refused != nil {
		out.Refused = *a.Refused
	}
	if a.Tier != nil {
		out.Tier = *a.Tier
	}
	return out
}

// evalAnswerKey names one job of an item.
type evalAnswerKey struct {
	purpose, slot, arm string
	batch, repeat      int
}

// evalAnswers is the run's answers by item, the fingerprint measurements
// and the invalid answers per participant.
type evalAnswers struct {
	byItem       map[int64]map[evalAnswerKey]*database.AuthorLLMEvalAnswer
	measurements []llmeval.FingerprintMeasurement
	invalid      map[string]*llmeval.ParticipantCount
	// failed are the jobs that ended invalid or failed, in read order.
	failed []evalFailedJob
	// modes is the output mode each participant's fingerprint was answered
	// in; noMode the participants whose fingerprint no mode served.
	modes  map[string]string
	noMode map[string]bool
}

// readEvalAnswers reads and sorts the run's answers.
func (d *AuthorLLMEvalDriver) readEvalAnswers(ctx context.Context, runID int64) (*evalAnswers, error) {
	answers, err := database.AuthorLLMEvalAnswers(ctx, d.db, runID)
	if err != nil {
		return nil, err
	}
	out := &evalAnswers{byItem: map[int64]map[evalAnswerKey]*database.AuthorLLMEvalAnswer{},
		invalid: map[string]*llmeval.ParticipantCount{}, modes: map[string]string{}, noMode: map[string]bool{}}
	for i := range answers {
		a := &answers[i]
		arm := ""
		if a.Arm != nil {
			arm = *a.Arm
		}
		if out.byItem[a.EvalItemID] == nil {
			out.byItem[a.EvalItemID] = map[evalAnswerKey]*database.AuthorLLMEvalAnswer{}
		}
		out.byItem[a.EvalItemID][evalAnswerKey{a.Purpose, a.Slot, arm, a.BatchSize, a.RepeatNo}] = a
		if a.Purpose == string(models.AuthorLLMPurposeEvalFingerprint) {
			if a.Status == string(models.AuthorLLMJobAnswered) {
				out.modes[a.Slot] = a.OutputMode
			}
			if deref(a.LastErrorClass) == database.AuthorLLMClassNoOutputMode {
				out.noMode[a.Slot] = true
			}
			out.measurements = append(out.measurements, measurementOf(a))
			continue
		}
		if !models.AuthorLLMJobStatus(a.Status).IsTerminal() {
			continue
		}
		c := out.invalid[a.Slot]
		if c == nil {
			c = &llmeval.ParticipantCount{Participant: a.Slot}
			out.invalid[a.Slot] = c
		}
		c.Total++
		if a.Status != string(models.AuthorLLMJobAnswered) {
			c.Matched++
			out.failed = append(out.failed, evalFailedJob{EvalItemID: a.EvalItemID, Participant: a.Slot,
				Purpose: a.Purpose, Status: a.Status, Class: deref(a.LastErrorClass)})
		}
	}
	return out, nil
}

// measurementOf is a fingerprint answer's measurement: the answering call's
// model id, prompt tokens and reasoning (or completion) tokens.
func measurementOf(a *database.AuthorLLMEvalAnswer) llmeval.FingerprintMeasurement {
	m := llmeval.FingerprintMeasurement{Participant: a.Slot, Answered: a.Status == string(models.AuthorLLMJobAnswered)}
	if a.ResponseModel == nil || a.PromptTokens == nil {
		m.Answered = false
		return m
	}
	m.Model, m.PromptTokens = *a.ResponseModel, *a.PromptTokens
	m.Reasoning = deref(a.CompletionTokens)
	if a.ReasoningTokens != nil {
		m.Reasoning = *a.ReasoningTokens
	}
	return m
}

// collect reads the run's answers and frozen items into the llmeval input.
func (d *AuthorLLMEvalDriver) collect(ctx context.Context, run *evalRun) (*evalData, error) {
	answers, err := d.readEvalAnswers(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	data := &evalData{failed: answers.failed}
	reasoning := map[string]bool{}
	for _, p := range d.identity.Participants {
		reasoning[string(p.Slot)] = p.ReasoningEffort != ""
	}
	data.fingerprints, data.fingerprintResult = llmeval.EvaluateFingerprint(answers.measurements, reasoning)
	data.modes = answers.modes
	// A participant that refused every output mode has no fingerprint; the
	// reason says so before anything the measurements say.
	for _, slot := range llmreq.Slots() {
		if answers.noMode[string(slot)] {
			data.fingerprintResult = llmeval.FingerprintResult{Reason: "no_output_mode: " + string(slot)}
			break
		}
	}

	anchors, err := database.AuthorLLMEvalItems(ctx, d.db, []string{authorLLMEvalAnchorSet},
		[]models.AuthorLLMEvalItemSet{models.AuthorLLMEvalAnchor})
	if err != nil {
		return nil, err
	}
	data.anchor = anchorCounts(anchors, answers.byItem)

	items, err := d.scopeItems(ctx, run)
	if err != nil {
		return nil, err
	}
	combos := make([]llmeval.Combo, 0, evalArms*len(run.Settings.BatchSizes))
	for _, b := range run.Settings.BatchSizes {
		combos = append(combos, llmeval.Combo{Arm: llmeval.ArmA, Batch: b}, llmeval.Combo{Arm: llmeval.ArmB, Batch: b})
	}
	repeat := map[string]*llmeval.ParticipantCount{}
	for i := range items {
		jobs := answers.byItem[items[i].ID]
		if evalItemDropped(jobs) {
			data.dropped++
			continue
		}
		data.input.Items = append(data.input.Items, evalItem(&items[i], jobs, combos))
		countRepeats(repeat, jobs)
	}
	data.input.Combos = combos
	data.input.Fingerprint = data.fingerprintResult
	data.input.Anchor = data.anchor
	data.input.Repeat = sortedCounts(repeat)
	data.input.Invalid = sortedCounts(answers.invalid)
	data.input.Registrable = run.Mode != models.AuthorLLMEvalModeDev
	return data, nil
}

// evalItem is one measured item with its judges' and candidates' answers.
func evalItem(
	it *database.AuthorLLMEvalItemRow, jobs map[evalAnswerKey]*database.AuthorLLMEvalAnswer, combos []llmeval.Combo,
) llmeval.Item {
	item := llmeval.Item{ID: it.ID, Stratum: it.Stratum, Weight: it.Weight,
		Holdout: it.ItemSet == string(models.AuthorLLMEvalHoldout), HasContext: len(it.ContextSHA256) > 0,
		Candidates: map[llmeval.Combo][2]llmeval.Answer{}}
	judge := string(models.AuthorLLMPurposeEvalJudge)
	item.Judges = [2]llmeval.Answer{
		answerOf(jobs[evalAnswerKey{judge, string(llmreq.SlotJudgeA), "", 1, 0}]),
		answerOf(jobs[evalAnswerKey{judge, string(llmreq.SlotJudgeB), "", 1, 0}]),
	}
	cand := string(models.AuthorLLMPurposeEvalCandidate)
	for _, c := range combos {
		a := jobs[evalAnswerKey{cand, string(llmreq.SlotProductionA), c.Arm, c.Batch, 0}]
		b := jobs[evalAnswerKey{cand, string(llmreq.SlotProductionB), c.Arm, c.Batch, 0}]
		if a == nil && b == nil {
			continue
		}
		item.Candidates[c] = [2]llmeval.Answer{answerOf(a), answerOf(b)}
	}
	return item
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// evalItemDropped reports an item whose frozen excerpt could not be read back
// unchanged: the book was deleted or changed since the freeze. Such an item
// drops out of the measurement and is counted in the report.
func evalItemDropped(jobs map[evalAnswerKey]*database.AuthorLLMEvalAnswer) bool {
	for _, a := range jobs {
		if a.WithContext && models.AuthorLLMJobStatus(a.Status).IsTerminal() &&
			a.ContextState != string(models.AuthorLLMContextAttached) {
			return true
		}
	}
	return false
}

// countRepeats compares an item's repeated answers with its first ones: the
// judge's own, and the production participants' arm A at one per request.
func countRepeats(counts map[string]*llmeval.ParticipantCount, jobs map[evalAnswerKey]*database.AuthorLLMEvalAnswer) {
	for _, slot := range llmreq.Slots() {
		arm, purpose := llmeval.ArmA, string(models.AuthorLLMPurposeEvalCandidate)
		if slot == llmreq.SlotJudgeA || slot == llmreq.SlotJudgeB {
			arm, purpose = "", string(models.AuthorLLMPurposeEvalJudge)
		}
		again := jobs[evalAnswerKey{string(models.AuthorLLMPurposeEvalRepeat), string(slot), arm, 1, 1}]
		if again == nil || !models.AuthorLLMJobStatus(again.Status).IsTerminal() {
			continue
		}
		first := answerOf(jobs[evalAnswerKey{purpose, string(slot), arm, 1, 0}])
		second := answerOf(again)
		c := counts[string(slot)]
		if c == nil {
			c = &llmeval.ParticipantCount{Participant: string(slot)}
			counts[string(slot)] = c
		}
		c.Total++
		if first.Valid && second.Valid && first == second {
			c.Matched++
		}
	}
}

func sortedCounts(m map[string]*llmeval.ParticipantCount) []llmeval.ParticipantCount {
	out := make([]llmeval.ParticipantCount, 0, len(m))
	for _, slot := range llmreq.Slots() {
		if c, ok := m[string(slot)]; ok {
			out = append(out, *c)
		}
	}
	return out
}

// anchorCounts counts every participant's anchor answers equal to the
// expected one.
func anchorCounts(
	anchors []database.AuthorLLMEvalItemRow, byItem map[int64]map[evalAnswerKey]*database.AuthorLLMEvalAnswer,
) []llmeval.ParticipantCount {
	out := make([]llmeval.ParticipantCount, 0, len(llmreq.Slots()))
	for _, slot := range llmreq.Slots() {
		c := llmeval.ParticipantCount{Participant: string(slot), Total: len(anchors)}
		for i := range anchors {
			it := &anchors[i]
			want, err := llmres.ParseReply(it.Expected)
			if err != nil {
				continue
			}
			sum := sha256.Sum256(want.CanonicalBytes())
			got := answerOf(byItem[it.ID][evalAnswerKey{string(models.AuthorLLMPurposeEvalAnchor), string(slot), "", 1, 0}])
			if got.Valid && !got.Refused && got.Canonical == hex.EncodeToString(sum[:]) {
				c.Matched++
			}
		}
		out = append(out, c)
	}
	return out
}

// authorLLMEvalReportDoc is the stored report.
type authorLLMEvalReportDoc struct {
	RunID         int64                 `json:"run_id"`
	Mode          string                `json:"mode"`
	ConfigVersion string                `json:"config_version"`
	Settings      AuthorLLMEvalSettings `json:"settings"`
	Sets          []evalSetSummary      `json:"sets"`
	// FullRunOnHoldout is which full run on these sets this is for the
	// prompt line (1 for the first); zero for dev and pilot runs.
	FullRunOnHoldout int `json:"full_run_on_holdout"`
	Dropped          int `json:"dropped_items"`
	// Skipped are the items left out of the sets or the run, with their
	// identifiers and why; FailedJobs the jobs that ended without an answer.
	Skipped      []evalSkipped                    `json:"skipped"`
	FailedJobs   []evalFailedJob                  `json:"failed_jobs"`
	Fingerprints []llmeval.ParticipantFingerprint `json:"fingerprints"`
	Evaluation   llmeval.Report                   `json:"evaluation"`
}

// evalSkipped is a skipped item as the report shows it.
type evalSkipped struct {
	Stage       string `json:"stage"`
	Reason      string `json:"reason"`
	SetVersion  string `json:"set_version,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	EvalItemID  int64  `json:"eval_item_id,omitempty"`
	BookID      int64  `json:"book_id,omitempty"`
}

type evalSetSummary struct {
	SetVersion string `json:"set_version"`
	SHA256     string `json:"sha256"`
	Items      int    `json:"items"`
}

// report computes the report, stores it with its digest and completes the
// run.
func (d *AuthorLLMEvalDriver) report(ctx context.Context, run *evalRun) error {
	data, err := d.collect(ctx, run)
	if err != nil {
		return err
	}
	evaluation := llmeval.Evaluate(&data.input)
	versions, _, err := d.scope(ctx, run)
	if err != nil {
		return err
	}
	versions = append(versions, authorLLMEvalAnchorSet, authorLLMEvalFingerprints)
	sets, err := database.AuthorLLMEvalSetsByVersion(ctx, d.db, versions)
	if err != nil {
		return err
	}
	skips, err := database.AuthorLLMEvalSkips(ctx, d.db, run.ID, versions)
	if err != nil {
		return err
	}
	// The report shows the mode each participant was asked in: the
	// negotiated one, unless the run's settings named another.
	settings := run.Settings
	settings.OutputModes = map[string]string{}
	for _, slot := range llmreq.Slots() {
		mode := d.mode(run, slot)
		if m := data.modes[string(slot)]; m != "" && run.Settings.mode(slot) == "" {
			mode = m
		}
		settings.OutputModes[string(slot)] = mode
	}
	doc := authorLLMEvalReportDoc{RunID: run.ID, Mode: run.Mode, ConfigVersion: d.version, Settings: settings,
		Dropped: data.dropped, Skipped: make([]evalSkipped, 0, len(skips)), FailedJobs: data.failed,
		Fingerprints: data.fingerprints, Evaluation: evaluation}
	if doc.FailedJobs == nil {
		doc.FailedJobs = []evalFailedJob{}
	}
	for i := range skips {
		sk := &skips[i]
		doc.Skipped = append(doc.Skipped, evalSkipped{Stage: sk.Stage, Reason: sk.Reason, SetVersion: deref(sk.SetVersion),
			Fingerprint: hex.EncodeToString(sk.SourceFingerprint), EvalItemID: deref(sk.EvalItemID), BookID: deref(sk.BookID)})
	}
	for _, s := range sets {
		doc.Sets = append(doc.Sets, evalSetSummary{SetVersion: s.SetVersion, SHA256: hex.EncodeToString(s.SHA256), Items: s.Items})
	}
	if run.Mode == models.AuthorLLMEvalModeFull {
		prior, priorErr := database.AuthorLLMEvalPriorFullRuns(ctx, d.db, d.version)
		if priorErr != nil {
			return priorErr
		}
		doc.FullRunOnHoldout = prior + 1
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return database.StoreAuthorLLMEvalReport(ctx, d.db, run.ID, d.version, raw)
}

// AuthorLLMEvalReportFile is a report as the admin downloads it: the stored
// text and its SHA-256, which is the digest a registration cites.
func AuthorLLMEvalReportFile(ctx context.Context, db pg.DBI, runID int64) (report []byte, sha string, err error) {
	r, err := database.GetAuthorLLMEvalReport(ctx, db, runID)
	if err != nil {
		return nil, "", err
	}
	return []byte(r.Report), hex.EncodeToString(r.ReportSHA256), nil
}

// The freeze. Sets are written once: a set version already frozen is never
// sampled again, so a new configuration is measured on the same items.

// freeze freezes every set the run needs that is not frozen yet, then
// records the run's token estimate.
func (d *AuthorLLMEvalDriver) freeze(ctx context.Context, run *evalRun) error {
	frozen, err := database.FrozenAuthorLLMEvalSets(ctx, d.db)
	if err != nil {
		return err
	}
	if !frozen[authorLLMEvalFingerprints] {
		if err := d.freezeFingerprint(ctx, run); err != nil {
			return err
		}
	}
	if !frozen[authorLLMEvalAnchorSet] {
		if err := d.freezeAnchor(ctx, run); err != nil {
			return err
		}
	}
	if err := d.freezeStrata(ctx, run, frozen); err != nil {
		return err
	}
	if run.Mode != models.AuthorLLMEvalModePilot && !frozen[authorLLMEvalCanarySet] {
		if err := d.freezeCanary(ctx, run); err != nil {
			return err
		}
	}
	return d.estimate(ctx, run)
}

// Closed reasons of a skipped eval item.
const (
	evalSkipInvalidSource      = "invalid_source"
	evalSkipInputTooLong       = "input_too_long"
	evalSkipInputUnreadable    = "input_unreadable"
	evalSkipExcerptUnavailable = "excerpt_unavailable"
	evalSkipAnchorIncomplete   = "anchor_incomplete"
	// evalSkipItemPanic: building the item panicked (a catalog value no
	// code path expected); the item is left out, the freeze goes on.
	evalSkipItemPanic = "item_panic"
)

// errAuthorLLMItemPanic is a panic one item raised, caught so it cannot stop
// its batch or its stage.
var errAuthorLLMItemPanic = errors.New("services: an LLM item panicked")

// isolateItem runs fn for one item and turns a panic into
// errAuthorLLMItemPanic: one bad catalog value is that item's failure, never
// its batch's or its stage's. The panic's value is not kept — it may carry a
// name.
func isolateItem(fn func() error) (err error) {
	defer func() {
		if recover() != nil {
			err = errAuthorLLMItemPanic
		}
	}()
	return fn()
}

// AuthorLLMEvalSkipReasons lists the closed reasons of a skipped eval item.
func AuthorLLMEvalSkipReasons() []string {
	return []string{evalSkipInvalidSource, evalSkipInputTooLong, evalSkipInputUnreadable,
		evalSkipExcerptUnavailable, evalSkipAnchorIncomplete, evalSkipItemPanic}
}

// skip records an item the freeze left out and logs it by its identifiers;
// the freeze moves on. Never a field, a name or an excerpt.
func (d *AuthorLLMEvalDriver) skip(ctx context.Context, run *evalRun, sk *database.AuthorLLMEvalSkip) error {
	sk.Stage = evalStageFreeze
	if err := database.RecordAuthorLLMEvalSkip(ctx, d.db, run.ID, sk); err != nil {
		return err
	}
	LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
		Name: AuthorMetadataEventLLMEvalItemSkipped, Stage: AuthorMetadataStageRunner, RunID: run.ID,
		BookID: deref(sk.BookID), ItemID: deref(sk.EvalItemID), Fingerprint: sk.SourceFingerprint, Status: sk.Reason,
		Worker: authorLLMEvalDriverName})
	return nil
}

// tokenSkipReason is the skip reason of an input whose tokens cannot be
// built.
func tokenSkipReason(err error) string {
	if errors.Is(err, llmres.ErrInputTooLong) {
		return evalSkipInputTooLong
	}
	return evalSkipInputUnreadable
}

// freezeSet writes one set; a set another replica froze first is fine.
func (d *AuthorLLMEvalDriver) freezeSet(ctx context.Context, run *evalRun, version string, items []database.AuthorLLMNewEvalItem) error {
	err := database.FreezeAuthorLLMEvalSet(ctx, d.db, run.ID, version, evalSetSHA(items), items)
	if errors.Is(err, database.ErrAuthorLLMEvalSetFrozen) {
		return nil
	}
	return err
}

// evalSetSHA is the SHA-256 of a set's canonical export: one JSON line per
// item, in (item set, fingerprint) order.
func evalSetSHA(items []database.AuthorLLMNewEvalItem) []byte {
	type line struct {
		ItemSet     string          `json:"item_set"`
		Stratum     string          `json:"stratum"`
		Fingerprint string          `json:"fingerprint"`
		Extractor   string          `json:"extractor_version"`
		Input       json.RawMessage `json:"input"`
		Weight      int             `json:"weight"`
		BookID      *int64          `json:"context_book_id"`
		BookMD5     *string         `json:"book_md5"`
		Context     string          `json:"context_sha256"`
		Expected    json.RawMessage `json:"expected"`
	}
	sorted := slices.Clone(items)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].ItemSet != sorted[j].ItemSet {
			return sorted[i].ItemSet < sorted[j].ItemSet
		}
		return bytes.Compare(sorted[i].SourceFingerprint, sorted[j].SourceFingerprint) < 0
	})
	h := sha256.New()
	for i := range sorted {
		it := &sorted[i]
		expected := it.Expected
		if len(expected) == 0 {
			expected = json.RawMessage("null")
		}
		raw, err := json.Marshal(line{string(it.ItemSet), it.Stratum, hex.EncodeToString(it.SourceFingerprint),
			it.ExtractorVersion, it.Input, max(it.Weight, 1), it.ContextBookID, it.BookMD5,
			hex.EncodeToString(it.ContextSHA256), expected})
		if err != nil {
			panic(err) // the line shape is always marshalable
		}
		h.Write(raw)
		h.Write([]byte{'\n'})
	}
	return h.Sum(nil)
}

// freezeFingerprint freezes the fixed fingerprint request as an item.
func (d *AuthorLLMEvalDriver) freezeFingerprint(ctx context.Context, run *evalRun) error {
	fp := llmreq.FingerprintItem()
	value, err := sourceValueOf(fp.Input.Fields)
	if err != nil {
		return err
	}
	sum := authornorm.SourceFingerprint(value)
	input, err := json.Marshal(fp.Input)
	if err != nil {
		return err
	}
	return d.freezeSet(ctx, run, authorLLMEvalFingerprints, []database.AuthorLLMNewEvalItem{{
		ItemSet: models.AuthorLLMEvalFingerprint, Stratum: "fingerprint", SourceFingerprint: sum[:],
		ExtractorVersion: "fixed", Input: input, Weight: 1,
	}})
}

// sourceValueOf rebuilds the canonical source value of fields.
func sourceValueOf(fields []llmres.FieldValue) (authornorm.SourceValue, error) {
	kinds := map[llmres.Field]authornorm.ComponentKind{
		llmres.FieldFirst: authornorm.ComponentFirst, llmres.FieldMiddle: authornorm.ComponentMiddle,
		llmres.FieldLast: authornorm.ComponentLast, llmres.FieldNickname: authornorm.ComponentNickname,
	}
	comps := make([]authornorm.SourceComponent, 0, len(fields))
	for _, f := range fields {
		comps = append(comps, authornorm.SourceComponent{Kind: kinds[f.Field], Value: f.Text})
	}
	return authornorm.NewSourceValue(comps)
}

// freezeAnchor freezes the anchor set: credits whose answer is known without
// an LLM, half Cyrillic and half Latin, in hash order. The expected answer is
// a person in given-first order with the roles of the FB2 fields. A set short
// of its size in either script is not frozen — a frozen set is never
// sampled again, so a short one could never be repaired: the run records the
// skip and measures no anchor (its report is not registrable), and a later
// run on a grown catalog freezes the full set.
func (d *AuthorLLMEvalDriver) freezeAnchor(ctx context.Context, run *evalRun) error {
	rows, err := database.AuthorLLMAnchorCandidates(ctx, d.db, d.sizes.AnchorMinSupport, d.sizes.AnchorMinSharePercent)
	if err != nil {
		return err
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return bytes.Compare(evalOrder(rows[i].SourceFingerprint), evalOrder(rows[j].SourceFingerprint)) < 0
	})
	var items []database.AuthorLLMNewEvalItem
	taken := map[string]int{}
	for i := range rows {
		r := &rows[i]
		if taken[r.Script] >= d.sizes.Anchor/2 {
			continue
		}
		var item *database.AuthorLLMNewEvalItem
		if err := isolateItem(func() error {
			item = anchorItemOf(r)
			return nil
		}); err != nil {
			if skipErr := d.skip(ctx, run, &database.AuthorLLMEvalSkip{Reason: evalSkipItemPanic,
				SourceFingerprint: r.SourceFingerprint}); skipErr != nil {
				return skipErr
			}
			continue
		}
		if item == nil {
			continue
		}
		items = append(items, *item)
		taken[r.Script]++
	}
	for _, script := range []authornorm.Script{authornorm.ScriptCyrillic, authornorm.ScriptLatin} {
		if taken[string(script)] < d.sizes.Anchor/2 {
			set := authorLLMEvalAnchorSet
			return d.skip(ctx, run, &database.AuthorLLMEvalSkip{Reason: evalSkipAnchorIncomplete, SetVersion: &set})
		}
	}
	return d.freezeSet(ctx, run, authorLLMEvalAnchorSet, items)
}

// anchorItemOf is a candidate's anchor item, or nil when its name is not
// exactly two sendable tokens.
func anchorItemOf(r *database.AuthorLLMAnchorRow) *database.AuthorLLMNewEvalItem {
	input := llmreq.ItemInput{Fields: []llmres.FieldValue{
		{Field: llmres.FieldFirst, Text: r.First}, {Field: llmres.FieldLast, Text: r.Last}},
		Script: r.Script, Flags: []string{}}
	tokens, err := input.Tokens()
	if err != nil || len(tokens) != 2 {
		return nil
	}
	expected := llmres.Reply{Form: llmres.FormPerson, Order: llmres.OrderGivenFirst,
		Roles:   []llmres.RoleAssignment{{I: 0, Role: llmres.RoleGiven}, {I: 1, Role: llmres.RoleFamily}},
		CaseFix: []llmres.CaseFix{}}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil
	}
	return &database.AuthorLLMNewEvalItem{ItemSet: models.AuthorLLMEvalAnchor,
		Stratum: "anchor." + strings.ToLower(r.Script), SourceFingerprint: r.SourceFingerprint,
		ExtractorVersion: r.ExtractorVersion, Input: raw, Weight: 1, Expected: expected.CanonicalBytes()}
}

// stratumOf is an input's stratum: the homoglyph stratum when the homoglyph
// rule would change a token, else the local class and the script.
func stratumOf(row *database.AuthorLLMPopulationRow, tokens []llmres.Token) string {
	if _, repaired := llmres.RepairHomoglyphs(tokens); repaired {
		return authorLLMHomoglyphStratum
	}
	script := authorLLMNoScript
	if row.Script != nil && *row.Script != "" {
		script = strings.ToLower(*row.Script)
	}
	return row.DecisionClass + "." + script
}

// populationItem is one sampled input before freezing.
type populationItem struct {
	row     *database.AuthorLLMPopulationRow
	input   llmreq.ItemInput
	tokens  []llmres.Token
	stratum string
}

// freezeStrata freezes every stratum the run needs that is not frozen yet:
// the pilot's one stratum, or all of them.
func (d *AuthorLLMEvalDriver) freezeStrata(ctx context.Context, run *evalRun, frozen map[string]bool) error {
	rows, err := database.AuthorLLMEvalPopulation(ctx, d.db)
	if err != nil {
		return err
	}
	strata, skipped := classifyPopulation(rows)
	for i := range skipped {
		if err := d.skip(ctx, run, &skipped[i]); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(strata))
	for name := range strata {
		if run.Mode == models.AuthorLLMEvalModePilot && name != authorLLMEvalPilotStratum {
			continue
		}
		if !frozen[authorLLMEvalStratumSet+name] {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		if err := d.freezeStratum(ctx, run, name, strata[name]); err != nil {
			return err
		}
	}
	return nil
}

// classifyPopulation turns the review population into request inputs by
// stratum, leaving out — with the reason — every row that cannot be sent: a
// source value that does not build, an input over the request limits, or a
// row whose handling panicked. One row is never more than its own skip.
func classifyPopulation(rows []database.AuthorLLMPopulationRow) (map[string][]populationItem, []database.AuthorLLMEvalSkip) {
	strata := map[string][]populationItem{}
	var skipped []database.AuthorLLMEvalSkip
	for i := range rows {
		row := &rows[i]
		var item populationItem
		reason := ""
		if err := isolateItem(func() error {
			item, reason = buildPopulationItem(row)
			return nil
		}); err != nil {
			reason = evalSkipItemPanic
		}
		if reason != "" {
			skipped = append(skipped, database.AuthorLLMEvalSkip{Reason: reason, SourceFingerprint: row.SourceFingerprint})
			continue
		}
		strata[item.stratum] = append(strata[item.stratum], item)
	}
	return strata, skipped
}

// buildPopulationItem is populationItemOf; a test swaps it to make one row
// panic.
var buildPopulationItem = populationItemOf

// populationItemOf builds one row's request input and stratum, or says why
// the row cannot be sent.
func populationItemOf(row *database.AuthorLLMPopulationRow) (item populationItem, skipReason string) {
	value, err := authornorm.NewSourceValue(componentsOf(row))
	if err != nil {
		return populationItem{}, evalSkipInvalidSource
	}
	input := llmreq.ItemInput{Fields: llmres.FieldValuesOf(value), Flags: row.QualityFlags}
	if row.Script != nil {
		input.Script = *row.Script
	}
	if input.Flags == nil {
		input.Flags = []string{}
	}
	tokens, err := input.Tokens()
	if err != nil {
		// Over the request limits: never sent, never measured.
		return populationItem{}, tokenSkipReason(err)
	}
	item = populationItem{row: row, input: input, tokens: tokens}
	item.stratum = stratumOf(row, tokens)
	return item, ""
}

func componentsOf(row *database.AuthorLLMPopulationRow) []authornorm.SourceComponent {
	var comps []authornorm.SourceComponent
	for _, f := range []struct {
		kind  authornorm.ComponentKind
		value *string
	}{
		{authornorm.ComponentFirst, row.First}, {authornorm.ComponentMiddle, row.Middle},
		{authornorm.ComponentLast, row.Last}, {authornorm.ComponentNickname, row.Nickname},
	} {
		if f.value != nil {
			comps = append(comps, authornorm.SourceComponent{Kind: f.kind, Value: *f.value})
		}
	}
	return comps
}

// freezeStratum samples a stratum in hash order: holdout first, then dev; a
// stratum smaller than both goes into the holdout whole. Every sampled item's
// excerpt is read now and its digest frozen.
func (d *AuthorLLMEvalDriver) freezeStratum(ctx context.Context, run *evalRun, name string, pop []populationItem) error {
	sort.SliceStable(pop, func(i, j int) bool {
		return bytes.Compare(evalOrder(pop[i].row.SourceFingerprint), evalOrder(pop[j].row.SourceFingerprint)) < 0
	})
	holdout := len(pop)
	if len(pop) >= d.sizes.Holdout+d.sizes.Dev {
		holdout = d.sizes.Holdout
	}
	end := min(len(pop), d.sizes.Holdout+d.sizes.Dev)
	items := make([]database.AuthorLLMNewEvalItem, 0, end)
	for i, p := range pop[:end] {
		set := models.AuthorLLMEvalHoldout
		if i >= holdout {
			set = models.AuthorLLMEvalDev
		}
		raw, err := json.Marshal(p.input)
		if err != nil {
			return err
		}
		item := database.AuthorLLMNewEvalItem{ItemSet: set, Stratum: name, SourceFingerprint: p.row.SourceFingerprint,
			ExtractorVersion: p.row.ExtractorVersion, Input: raw, Weight: p.row.Weight}
		version := authorLLMEvalStratumSet + name
		var book int64
		if err := isolateItem(func() error {
			book = d.freezeExcerpt(ctx, &item, p.tokens)
			return nil
		}); err != nil {
			// Reading the excerpt panicked: the item is frozen without one.
			item.ContextBookID, item.BookMD5, item.ContextSHA256 = nil, nil, nil
			if err := d.skip(ctx, run, &database.AuthorLLMEvalSkip{Reason: evalSkipItemPanic,
				SetVersion: &version, SourceFingerprint: item.SourceFingerprint}); err != nil {
				return err
			}
		}
		if book != 0 {
			if err := d.skip(ctx, run, &database.AuthorLLMEvalSkip{Reason: evalSkipExcerptUnavailable,
				SetVersion: &version, SourceFingerprint: item.SourceFingerprint, BookID: &book}); err != nil {
				return err
			}
		}
		items = append(items, item)
	}
	return d.freezeSet(ctx, run, authorLLMEvalStratumSet+name, items)
}

// freezeExcerpt reads an item's excerpt as the worker will — the book with
// the lowest id whose archive reads — and freezes its book and, when it is
// relevant, its digest. A book without a relevant excerpt is frozen without a
// digest; an item with no readable book has neither. It returns the first
// book whose excerpt did not read (zero when none failed), for the skip
// record.
func (d *AuthorLLMEvalDriver) freezeExcerpt(
	ctx context.Context, item *database.AuthorLLMNewEvalItem, tokens []llmres.Token,
) (unreadable int64) {
	books, err := database.AuthorLLMContextBooks(ctx, d.db, item.SourceFingerprint, item.ExtractorVersion)
	if err != nil {
		return 0
	}
	for i := range books {
		book := books[i]
		bc, ok := readAuthorLLMExcerpt(ctx, d.archives, d.archivesDir, &book)
		if !ok {
			if unreadable == 0 {
				unreadable = book.BookID
			}
			continue
		}
		id, md5 := book.BookID, book.BookMD5
		item.ContextBookID, item.BookMD5 = &id, &md5
		if llmres.ContextRelevant(&bc, tokens) {
			sum := bc.SHA256()
			item.ContextSHA256 = sum[:]
		}
		return unreadable
	}
	return unreadable
}

// freezeCanary freezes the canary set: copies of the first dev items of all
// strata in hash order (design K, section 6).
func (d *AuthorLLMEvalDriver) freezeCanary(ctx context.Context, run *evalRun) error {
	frozen, err := database.FrozenAuthorLLMEvalSets(ctx, d.db)
	if err != nil {
		return err
	}
	var versions []string
	for v := range frozen {
		if strings.HasPrefix(v, authorLLMEvalStratumSet) {
			versions = append(versions, v)
		}
	}
	dev, err := database.AuthorLLMEvalItems(ctx, d.db, versions, []models.AuthorLLMEvalItemSet{models.AuthorLLMEvalDev})
	if err != nil {
		return err
	}
	if len(dev) == 0 {
		return nil
	}
	var full []struct {
		ID               int64   `pg:"id"`
		ExtractorVersion string  `pg:"extractor_version"`
		ContextBookID    *int64  `pg:"context_book_id"`
		BookMD5          *string `pg:"book_md5"`
	}
	if _, err := d.db.QueryContext(ctx, &full, `
		SELECT id, extractor_version, context_book_id, book_md5 FROM author_llm_eval_item WHERE id IN (?) ORDER BY id`,
		pg.In(itemIDs(dev))); err != nil {
		return fmt.Errorf("reading the dev items: %w", err)
	}
	extra := map[int64]int{}
	for i, f := range full {
		extra[f.ID] = i
	}
	sort.SliceStable(dev, func(i, j int) bool {
		return bytes.Compare(evalOrder(dev[i].SourceFingerprint), evalOrder(dev[j].SourceFingerprint)) < 0
	})
	items := make([]database.AuthorLLMNewEvalItem, 0, d.sizes.Canary)
	for i := range dev[:min(len(dev), d.sizes.Canary)] {
		it := &dev[i]
		f := full[extra[it.ID]]
		items = append(items, database.AuthorLLMNewEvalItem{ItemSet: models.AuthorLLMEvalCanary, Stratum: it.Stratum,
			SourceFingerprint: it.SourceFingerprint, ExtractorVersion: f.ExtractorVersion, Input: it.Input,
			Weight: it.Weight, ContextBookID: f.ContextBookID, BookMD5: f.BookMD5, ContextSHA256: it.ContextSHA256})
	}
	return d.freezeSet(ctx, run, authorLLMEvalCanarySet, items)
}

// estimate records the run's token estimate from the jobs it will send.
func (d *AuthorLLMEvalDriver) estimate(ctx context.Context, run *evalRun) error {
	items, err := d.scopeItems(ctx, run)
	if err != nil {
		return err
	}
	anchors, err := database.AuthorLLMEvalItems(ctx, d.db, []string{authorLLMEvalAnchorSet},
		[]models.AuthorLLMEvalItemSet{models.AuthorLLMEvalAnchor})
	if err != nil {
		return err
	}
	withContext := 0
	for i := range items {
		if len(items[i].ContextSHA256) > 0 {
			withContext++
		}
	}
	batches := len(run.Settings.BatchSizes)
	judges, production := evalJudges, evalParticipants-evalJudges
	jobs := evalParticipants*d.sizes.FingerprintRepeats + evalParticipants*len(anchors) + judges*len(items) +
		production*batches*(len(items)+withContext) + evalParticipants*min(len(items), d.sizes.Repeat)
	return database.SetAuthorLLMRunEstimate(ctx, d.db, run.ID, int64(jobs)*authorLLMEvalTokensPerJob)
}

// AuthorLLMSamplingReport is what the freeze would sample from the current
// catalog, without freezing anything: the review population by stratum, the
// rows it would leave out by reason (with sample fingerprints, never a
// name), and the anchor candidates by script. It is the read-only check run
// against production data before an eval run is started there.
type AuthorLLMSamplingReport struct {
	Fingerprints int            `json:"fingerprints"`
	Weight       int64          `json:"weight"`
	Strata       map[string]int `json:"strata"`
	Skipped      map[string]int `json:"skipped"`
	// SkippedSamples holds up to authorLLMSamplingSamples fingerprints of
	// each skip reason, hex.
	SkippedSamples   map[string][]string `json:"skipped_samples"`
	AnchorCandidates map[string]int      `json:"anchor_candidates"`
	AnchorPanics     int                 `json:"anchor_panics"`
}

// authorLLMSamplingSamples bounds the sample fingerprints per skip reason.
const authorLLMSamplingSamples = 20

// AuthorLLMSamplingCheck runs the freeze's population and anchor reads and
// its per-row classification — the tokenizer and the strata — and reports
// them. It only reads.
func AuthorLLMSamplingCheck(ctx context.Context, db pg.DBI, sizes AuthorLLMEvalSizes) (*AuthorLLMSamplingReport, error) {
	rows, err := database.AuthorLLMEvalPopulation(ctx, db)
	if err != nil {
		return nil, err
	}
	report := &AuthorLLMSamplingReport{Fingerprints: len(rows), Strata: map[string]int{}, Skipped: map[string]int{},
		SkippedSamples: map[string][]string{}, AnchorCandidates: map[string]int{}}
	for i := range rows {
		report.Weight += int64(max(rows[i].Weight, 1))
	}
	strata, skipped := classifyPopulation(rows)
	for name, items := range strata {
		report.Strata[name] = len(items)
	}
	for _, sk := range skipped {
		report.Skipped[sk.Reason]++
		if len(report.SkippedSamples[sk.Reason]) < authorLLMSamplingSamples {
			report.SkippedSamples[sk.Reason] = append(report.SkippedSamples[sk.Reason], hex.EncodeToString(sk.SourceFingerprint))
		}
	}
	anchors, err := database.AuthorLLMAnchorCandidates(ctx, db, sizes.AnchorMinSupport, sizes.AnchorMinSharePercent)
	if err != nil {
		return nil, err
	}
	for i := range anchors {
		var item *database.AuthorLLMNewEvalItem
		if isolateItem(func() error {
			item = anchorItemOf(&anchors[i])
			return nil
		}) != nil {
			report.AnchorPanics++
			continue
		}
		if item != nil {
			report.AnchorCandidates[anchors[i].Script]++
		}
	}
	return report, nil
}
