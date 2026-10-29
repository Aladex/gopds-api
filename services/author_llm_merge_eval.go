package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"gopds-api/database"
	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// The merge eval (E4c, plan §3, §7): the eval driver of the merge
// configuration measures the merge question, in its own stages:
//
//	freeze      — reads the entity plan in one snapshot (the entity build's
//	              own read, read only, nothing written) and freezes the
//	              mode's sets: a random holdout of the stored candidates of
//	              each kind, or every one of them (a census), their cards
//	              exactly as the build hashed them, the digest the key; the
//	              adversarial suite and the known-same anchors; merge3's
//	              extra Latin questions; later runs of the mode reuse them;
//	fingerprint — the merge configuration's fixed request to every
//	              participant, learned as in K;
//	anchor      — the known-same anchors to all four: recall, never gated;
//	candidates  — the production pair on the holdout and the suite;
//	judge       — both judges on the holdout, and on the suite items the
//	              pair agreed are one person (only those can be accepted);
//	repeat      — part of the holdout once more to every participant;
//	report      — llmeval under the merge options (judge consensus, 0.1%,
//	              contested is an error, only "same" accepts), per kind and
//	              per script.
//
// Strata are kinds, pooled over scripts; every item records its script in
// its stratum name ("swap.cyrl"), which the report splits again.

// authorLLMMergeEvalDriverName is the merge eval driver's log label.
const authorLLMMergeEvalDriverName = "author_llm_merge_eval"

// Set versions of the merge eval: prefix m1., never K's v1., so K's scope
// never reads them.
const (
	mergeEvalSetPrefix      = "m1."
	mergeEvalFingerprintSet = mergeEvalSetPrefix + "fingerprint"
	mergeEvalSalt           = "e4-merge-eval-v1"
	// mergeEvalMaxErrorShare is the merge gate's bound (§3.1).
	mergeEvalMaxErrorShare = 0.001
	// The merge eval's token estimate, measured on the merge0 pilot (run
	// 32, §7): tokens per answer of a holdout or census item and of an
	// anchor, per suite item (the pair, and the judges where the pair
	// agreed), and per run for the fingerprint stage and the rest.
	mergeTokensPerAnswer     = 1520
	mergeTokensPerAnchor     = 1830
	mergeTokensPerSuiteItem  = 2950
	mergeTokensPerRunOverall = 1_450_000
	// mergeEvalGate is what a merge eval is gated against (§3.4).
	mergeEvalGate = "judge_consensus"
	// The fixed request's item: its stratum and extractor version, as K's.
	mergeFingerprintStratum  = "fingerprint"
	mergeFingerprintVersion  = "fixed"
	mergeEvalPerKind         = 300
	mergeEvalSuitePerShape   = 300
	mergeEvalSuiteLatin      = 30
	mergeEvalAnchorsPerShape = 150
	// merge1..merge4 (§3.3, §3.5): a suite of 1,000 per shape with 100
	// Latin, merge3's bucket holdout of 6,000 questions and its 1,000 extra
	// Latin ones.
	mergeEvalStageSuite      = 1000
	mergeEvalStageSuiteLatin = 100
	mergeEvalBucketHoldout   = 6000
	mergeEvalBucketLatin     = 1000
)

// mergeEvalMode is what a merge eval mode measures: its kinds; census asks
// every stored candidate of them instead of a holdout; supplement adds
// extra Latin questions gated outside |A|; wikidataSuite asks the
// candidates whose sides name two Wikidata items in the suite instead of
// the census (they are known to be two people).
type mergeEvalMode struct {
	kinds                             []string
	census, supplement, wikidataSuite bool
}

// mergeEvalModes are the merge eval modes (§7, §8 E4c): the pilot, then a
// stage per kind.
var mergeEvalModes = map[string]mergeEvalMode{
	models.AuthorLLMEvalModeMerge0: {kinds: []string{authorentity.CandidateSwap, authorentity.CandidateInitials}},
	models.AuthorLLMEvalModeMerge1: {kinds: []string{authorentity.CandidateSwap}, census: true},
	models.AuthorLLMEvalModeMerge2: {kinds: []string{authorentity.CandidateInitials}, census: true},
	models.AuthorLLMEvalModeMerge3: {kinds: []string{authorentity.CandidateBucket}, supplement: true},
	models.AuthorLLMEvalModeMerge4: {kinds: []string{authorentity.CandidateTranslit}, census: true,
		wikidataSuite: true},
}

// AuthorLLMMergeEvalSizes are a merge eval's sizes (§7).
type AuthorLLMMergeEvalSizes struct {
	// PerKind is the holdout of each kind (a census mode asks every
	// candidate); SuitePerShape the suite items of each kind's shape,
	// SuiteLatinPerShape the Latin ones among them (none in the translit
	// shape, Cyrillic against Latin by definition); AnchorsPerShape the
	// known-same anchors of each kind's shape; SupplementLatin merge3's
	// extra Latin questions.
	PerKind, SuitePerShape, SuiteLatinPerShape, AnchorsPerShape int
	SupplementLatin                                             int
	Repeat, FingerprintRepeats                                  int
}

// DefaultAuthorLLMMergeEvalSizes are merge0's sizes: 300 per kind, a
// suite of 300 per shape with 10% Latin, 150 anchors per shape, 200 repeats.
func DefaultAuthorLLMMergeEvalSizes() AuthorLLMMergeEvalSizes {
	return AuthorLLMMergeEvalSizes{PerKind: mergeEvalPerKind, SuitePerShape: mergeEvalSuitePerShape,
		SuiteLatinPerShape: mergeEvalSuiteLatin, AnchorsPerShape: mergeEvalAnchorsPerShape,
		Repeat: evalRepeatItems, FingerprintRepeats: evalFingerprintRepeats}
}

// DefaultAuthorLLMMergeEvalSizesOf are a mode's sizes: merge0's, or a
// stage's suite of 1,000 with 100 Latin (merge3: a holdout of 6,000 and
// 1,000 extra Latin questions).
func DefaultAuthorLLMMergeEvalSizesOf(mode string) AuthorLLMMergeEvalSizes {
	s := DefaultAuthorLLMMergeEvalSizes()
	if mode == models.AuthorLLMEvalModeMerge0 {
		return s
	}
	s.SuitePerShape, s.SuiteLatinPerShape = mergeEvalStageSuite, mergeEvalStageSuiteLatin
	if mode == models.AuthorLLMEvalModeMerge3 {
		s.PerKind, s.SupplementLatin = mergeEvalBucketHoldout, mergeEvalBucketLatin
	}
	return s
}

// WithMergeSizes sets a merge eval's sizes, whatever its mode.
func (d *AuthorLLMEvalDriver) WithMergeSizes(s AuthorLLMMergeEvalSizes) *AuthorLLMEvalDriver {
	d.mergeSizes = &s
	return d
}

// mergeSizesOf are a merge eval run's sizes: the driver's when set, else the
// mode's.
func (d *AuthorLLMEvalDriver) mergeSizesOf(mode string) *AuthorLLMMergeEvalSizes {
	if d.mergeSizes != nil {
		return d.mergeSizes
	}
	s := DefaultAuthorLLMMergeEvalSizesOf(mode)
	return &s
}

// merge reports the driver of a merge configuration.
func (d *AuthorLLMEvalDriver) merge() bool { return d.identity.Task == llmreq.TaskMerge }

// mergeEvalSets are a mode's frozen set versions; supplement holds merge3's
// extra Latin questions, holdout items gated outside |A| (empty in the
// other modes).
type mergeEvalSets struct{ holdout, suite, anchor, supplement string }

func mergeEvalSetsOf(mode string) mergeEvalSets {
	base := mergeEvalSetPrefix + mode + "."
	return mergeEvalSets{holdout: base + "holdout", suite: base + "adversarial", anchor: base + "anchor",
		supplement: base + "supplement"}
}

// all are the mode's set versions.
func (s mergeEvalSets) all() []string { return []string{s.holdout, s.suite, s.anchor, s.supplement} }

// mergeStratum is the stratum name of an item of kind in script.
func mergeStratum(kind, script string) string { return kind + "." + strings.ToLower(script) }

// splitMergeStratum is a stratum name's kind and script.
func splitMergeStratum(stratum string) (kind, script string) {
	kind, script, _ = strings.Cut(stratum, ".")
	return kind, script
}

// The merge answers' canonical digests, as answerOf spells them.
var (
	mergeSameCanonical      = mergeCanonicalHex(llmreq.MergeSame)
	mergeDifferentCanonical = mergeCanonicalHex(llmreq.MergeDifferent)
	mergeCannotTellCanon    = mergeCanonicalHex(llmreq.MergeCannotTell)
)

func mergeCanonicalHex(answer string) string {
	sum := sha256.Sum256(llmreq.MergeCanonical(answer))
	return hex.EncodeToString(sum[:])
}

// StartAuthorLLMMergeEval starts a merge eval run of the merge
// configuration's version; it returns at once.
func StartAuthorLLMMergeEval(ctx context.Context, db *pg.DB, version, mode string, adminID *int64) (int64, error) {
	if _, ok := mergeEvalModes[mode]; !ok {
		return 0, ErrAuthorLLMInvalidRun
	}
	settings := AuthorLLMEvalSettings{OutputModes: map[string]string{}, BatchSizes: []int{1}, NoExcerpt: true}
	raw, err := json.Marshal(settings)
	if err != nil {
		return 0, err
	}
	id, _, err := database.StartAuthorLLMEvalRun(ctx, db, version, mode, raw, adminID)
	return id, err
}

// mergeStage does one stage of a merge eval run; it returns the next stage
// once this one is done.
func (d *AuthorLLMEvalDriver) mergeStage(ctx context.Context, run *evalRun) (string, error) {
	if _, ok := mergeEvalModes[run.Mode]; !ok {
		// No stage of a mode the merge eval does not build can be done.
		return "", d.failMergeRun(ctx, run)
	}
	switch run.Stage {
	case evalStageFreeze:
		if err := d.mergeFreeze(ctx, run); err != nil {
			if errors.Is(err, errMergeEvalCensus) {
				return "", d.failMergeRun(ctx, run)
			}
			return "", err
		}
		return evalStageFingerprint, nil
	case evalStageFingerprint:
		done, err := d.awaitStage(ctx, run, models.AuthorLLMPurposeEvalFingerprint, d.enqueueMergeFingerprint)
		if !done || err != nil {
			return "", err
		}
		ok, err := d.learnMergeFingerprint(ctx, run)
		if err != nil {
			return "", err
		}
		if !ok {
			return evalStageReport, nil
		}
		return evalStageAnchor, nil
	case evalStageAnchor:
		return d.next(ctx, run, models.AuthorLLMPurposeEvalAnchor, d.enqueueMergeAnchor, evalStageCandidates)
	case evalStageCandidates:
		return d.next(ctx, run, models.AuthorLLMPurposeEvalCandidate, d.enqueueMergeCandidates, evalStageJudge)
	case evalStageJudge:
		return d.next(ctx, run, models.AuthorLLMPurposeEvalJudge, d.enqueueMergeJudges, evalStageRepeat)
	case evalStageRepeat:
		return d.next(ctx, run, models.AuthorLLMPurposeEvalRepeat, d.enqueueMergeRepeat, evalStageReport)
	case evalStageReport:
		return "", d.mergeReport(ctx, run)
	}
	return "", nil
}

// failMergeRun ends a merge eval run no stage of which can be done.
func (d *AuthorLLMEvalDriver) failMergeRun(ctx context.Context, run *evalRun) error {
	LogAuthorMetadataEvent(AuthorMetadataEventError, &AuthorMetadataEvent{
		Name: AuthorMetadataEventLLMEvalRunFailed, Stage: AuthorMetadataStageRunner, RunID: run.ID,
		Worker: authorLLMMergeEvalDriverName})
	return database.FailAuthorLLMRun(ctx, d.db, run.ID, authorLLMEvalStageInvalid)
}

// errMergeEvalCensus is a freeze whose plan has no sealed census: nothing
// can be frozen from it.
var errMergeEvalCensus = errors.New("services: the merge eval's plan has no sealed census")

// mergeFreeze freezes the fingerprint item and the mode's sets that are not
// frozen yet, then records the run's estimate.
func (d *AuthorLLMEvalDriver) mergeFreeze(ctx context.Context, run *evalRun) error {
	frozen, err := database.FrozenAuthorLLMEvalSets(ctx, d.db)
	if err != nil {
		return err
	}
	if !frozen[mergeEvalFingerprintSet] {
		if err := d.freezeMergeFingerprint(ctx, run); err != nil {
			return err
		}
	}
	sets := mergeEvalSetsOf(run.Mode)
	supplement := mergeEvalModes[run.Mode].supplement
	if !frozen[sets.holdout] || !frozen[sets.suite] || !frozen[sets.anchor] || (supplement && !frozen[sets.supplement]) {
		src, err := d.readMergeSources(ctx, run)
		if err != nil {
			return err
		}
		for _, s := range []struct {
			version string
			items   []database.AuthorLLMNewEvalItem
			freeze  bool
		}{{sets.holdout, src.holdout, true}, {sets.suite, src.suite, true}, {sets.anchor, src.anchor, true},
			{sets.supplement, src.supplement, supplement}} {
			if frozen[s.version] || !s.freeze {
				continue
			}
			if err := d.freezeSet(ctx, run, s.version, s.items); err != nil {
				return err
			}
		}
		LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
			Name: AuthorMetadataEventLLMMergeEvalFrozen, Stage: AuthorMetadataStageRunner, RunID: run.ID,
			Count:  len(src.holdout) + len(src.suite) + len(src.anchor) + len(src.supplement),
			Worker: authorLLMMergeEvalDriverName})
	}
	return d.mergeEstimate(ctx, run)
}

// freezeMergeFingerprint freezes the merge configuration's fixed request.
func (d *AuthorLLMEvalDriver) freezeMergeFingerprint(ctx context.Context, run *evalRun) error {
	input, err := json.Marshal(llmreq.MergeFingerprintItem().Input)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(input)
	return d.freezeSet(ctx, run, mergeEvalFingerprintSet, []database.AuthorLLMNewEvalItem{{
		ItemSet: models.AuthorLLMEvalFingerprint, Stratum: mergeFingerprintStratum, SourceFingerprint: sum[:],
		ExtractorVersion: mergeFingerprintVersion, Input: input, Weight: 1,
	}})
}

// mergeSources are a mode's items, to freeze.
type mergeSources struct {
	holdout, suite, anchor, supplement []database.AuthorLLMNewEvalItem
}

// readMergeSources reads the entity plan in one snapshot, read only, and
// builds the mode's items from it: the stored candidates of its kinds (a
// holdout, or all of them), merge3's extra Latin questions, the suite —
// merge4's with the candidates whose sides name two Wikidata items — and
// the anchors.
func (d *AuthorLLMEvalDriver) readMergeSources(ctx context.Context, run *evalRun) (*mergeSources, error) {
	loop := NewAuthorEntityBuildLoop(d.db, AuthorEntityBuildConfig{})
	r, err := loop.readPlan(ctx)
	if err != nil {
		return nil, err
	}
	census := r.census
	if census == "" {
		census = sealCensus(r.plan, r.cards)
	}
	if census != "" || !r.plan.MergeCandidatesSealed() {
		return nil, errMergeEvalCensus
	}
	plan := r.plan
	stored, err := database.MergeCandidateDigests(ctx, d.db)
	if err != nil {
		return nil, err
	}
	mode := mergeEvalModes[run.Mode]
	sizes := d.mergeSizesOf(run.Mode)
	src := &mergeSources{}
	natural := map[int]bool{}
	if mode.wikidataSuite {
		natural = wikidataDifferent(plan)
	}
	if src.holdout, src.supplement, err = d.mergeHoldout(ctx, run, plan, stored, natural); err != nil {
		return nil, err
	}
	exclude := map[uuid.UUID]bool{}
	cands := plan.MergeCandidates()
	for i := range cands {
		exclude[cands[i].SideA], exclude[cands[i].SideB] = true, true
	}
	naturalItems, err := d.naturalSuiteItems(ctx, run, plan, stored, natural)
	if err != nil {
		return nil, err
	}
	suite := selectSuite(plan.MergeSuitePairs(exclude), mode.kinds, sizes, len(naturalItems))
	anchors := selectAnchors(mergeAnchorsOf(plan, mode.kinds), mode.kinds, sizes.AnchorsPerShape)
	pairs := make([]authorentity.MergeSuitePair, 0, len(suite))
	for _, s := range suite {
		pairs = append(pairs, s.pair)
	}
	ids := append(plan.MergeSuiteBookIDs(pairs), plan.MergeAnchorBookIDs(anchors)...)
	slices.Sort(ids)
	books, err := database.LoadMergeCardBooks(ctx, d.db, slices.Compact(ids))
	if err != nil {
		return nil, err
	}
	if src.suite, err = d.suiteItems(ctx, run, plan, suite, books); err != nil {
		return nil, err
	}
	src.suite = append(naturalItems, src.suite...)
	if src.anchor, err = d.anchorItems(ctx, run, plan, anchors, books); err != nil {
		return nil, err
	}
	return src, nil
}

// suiteItems renders the picked suite items.
func (d *AuthorLLMEvalDriver) suiteItems(ctx context.Context, run *evalRun, plan *authorentity.Plan, suite []suitePick,
	books map[int64]authorentity.CardBookFields) ([]database.AuthorLLMNewEvalItem, error) {
	different := json.RawMessage(llmreq.MergeCanonical(llmreq.MergeDifferent))
	var out []database.AuthorLLMNewEvalItem
	for i := range suite {
		s := &suite[i]
		key := authorentity.SuiteItemKey(&s.pair, s.shape, s.romanized)
		item := database.AuthorLLMNewEvalItem{ItemSet: models.AuthorLLMEvalAdversarial,
			Stratum: mergeStratum(s.shape, s.script), SourceFingerprint: key[:],
			ExtractorVersion: authorentity.SuiteVersion, Weight: 1, Expected: different}
		ok, err := d.renderMergeItem(ctx, run, &item, func() authorentity.MergeInput {
			return plan.RenderSuiteItem(&s.pair, s.shape, s.romanized, books)
		})
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, item)
		}
	}
	return out, nil
}

// anchorItems renders the picked anchors.
func (d *AuthorLLMEvalDriver) anchorItems(ctx context.Context, run *evalRun, plan *authorentity.Plan,
	anchors []authorentity.MergeAnchorPair, books map[int64]authorentity.CardBookFields,
) ([]database.AuthorLLMNewEvalItem, error) {
	same := json.RawMessage(llmreq.MergeCanonical(llmreq.MergeSame))
	var out []database.AuthorLLMNewEvalItem
	for i := range anchors {
		a := &anchors[i]
		key := plan.AnchorItemKey(a)
		item := database.AuthorLLMNewEvalItem{ItemSet: models.AuthorLLMEvalAnchor,
			Stratum: mergeStratum(a.Kind, a.Script), SourceFingerprint: key[:],
			ExtractorVersion: authorentity.AnchorVersion, Weight: 1, Expected: same}
		ok, err := d.renderMergeItem(ctx, run, &item, func() authorentity.MergeInput {
			return plan.RenderAnchor(a, books)
		})
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, item)
		}
	}
	return out, nil
}

// mergeHoldout is the measured items of the mode's kinds: the plan's
// candidates whose digest a committed pass stored, in a salted digest
// order — every one of them in a census mode, else the first PerKind of
// each kind — and, for a mode with a supplement, the first SupplementLatin
// Latin ones outside the holdout. A candidate in natural (merge4's
// Wikidata pairs) is asked in the suite instead.
func (d *AuthorLLMEvalDriver) mergeHoldout(
	ctx context.Context, run *evalRun, plan *authorentity.Plan, stored map[string]bool, natural map[int]bool,
) (holdout, supplement []database.AuthorLLMNewEvalItem, err error) {
	mode := mergeEvalModes[run.Mode]
	sizes := d.mergeSizesOf(run.Mode)
	byKind := mergeCandidatesByKind(plan, stored, mode.kinds, natural)
	for _, kind := range mode.kinds {
		picks := byKind[kind]
		limit := len(picks)
		if !mode.census {
			limit = sizes.PerKind
		}
		var rest []int
		if holdout, rest, err = d.takeCandidates(ctx, run, plan, picks, limit, holdout); err != nil {
			return nil, nil, err
		}
		if !mode.supplement {
			continue
		}
		var latin []int
		for _, i := range rest {
			if plan.MergeCandidates()[i].Script == authorentity.ScriptLatn {
				latin = append(latin, i)
			}
		}
		if supplement, _, err = d.takeCandidates(ctx, run, plan, latin, sizes.SupplementLatin, supplement); err != nil {
			return nil, nil, err
		}
	}
	return holdout, supplement, nil
}

// mergeCandidatesByKind are the plan's candidates of the kinds whose digest
// a committed pass stored, by kind, in the sampling order; natural ones are
// left out.
func mergeCandidatesByKind(plan *authorentity.Plan, stored map[string]bool, kinds []string,
	natural map[int]bool) map[string][]int {
	type pick struct {
		i     int
		order []byte
	}
	byKind := map[string][]pick{}
	cands := plan.MergeCandidates()
	for i := range cands {
		c := &cands[i]
		if !slices.Contains(kinds, c.Kind) || !stored[string(c.Digest[:])] || natural[i] {
			continue
		}
		byKind[c.Kind] = append(byKind[c.Kind], pick{i: i, order: mergeEvalOrder(c.Digest[:])})
	}
	out := map[string][]int{}
	for kind, picks := range byKind {
		slices.SortFunc(picks, func(x, y pick) int { return bytes.Compare(x.order, y.order) })
		for _, p := range picks {
			out[kind] = append(out[kind], p.i)
		}
	}
	return out
}

// takeCandidates appends the first limit of picks as holdout items to out;
// an input over the bound is skipped, recorded, and does not count. rest
// are the picks not taken.
func (d *AuthorLLMEvalDriver) takeCandidates(
	ctx context.Context, run *evalRun, plan *authorentity.Plan, picks []int, limit int,
	out []database.AuthorLLMNewEvalItem,
) (_ []database.AuthorLLMNewEvalItem, rest []int, err error) {
	cands := plan.MergeCandidates()
	taken := 0
	for n, i := range picks {
		if taken == limit {
			return out, picks[n:], nil
		}
		c := &cands[i]
		input := plan.MergeInput(i)
		if len(input) > llmreq.MergeItemMaxBytes {
			if err := d.skip(ctx, run, &database.AuthorLLMEvalSkip{Reason: evalSkipInputTooLong,
				SourceFingerprint: c.Digest[:]}); err != nil {
				return nil, nil, err
			}
			continue
		}
		out = append(out, database.AuthorLLMNewEvalItem{ItemSet: models.AuthorLLMEvalHoldout,
			Stratum: mergeStratum(c.Kind, c.Script), SourceFingerprint: bytes.Clone(c.Digest[:]),
			ExtractorVersion: authorentity.MergeCandidateVersion, Input: bytes.Clone(input), Weight: 1})
		taken++
	}
	return out, nil, nil
}

// wikidataDifferent are the plan's translit candidates whose two sides
// name two different Wikidata items: natural namesakes (§3.1, A.11), asked
// in the suite, never in the census.
func wikidataDifferent(plan *authorentity.Plan) map[int]bool {
	out := map[int]bool{}
	cands := plan.MergeCandidates()
	for i := range cands {
		c := &cands[i]
		if c.Kind != authorentity.CandidateTranslit {
			continue
		}
		a, b := plan.WikidataOf(c.SideA), plan.WikidataOf(c.SideB)
		if a != "" && b != "" && a != b {
			out[i] = true
		}
	}
	return out
}

// naturalSuiteItems are the natural namesake candidates as suite items:
// their stored cards exactly as the build hashed them, expected different.
func (d *AuthorLLMEvalDriver) naturalSuiteItems(
	ctx context.Context, run *evalRun, plan *authorentity.Plan, stored map[string]bool, natural map[int]bool,
) ([]database.AuthorLLMNewEvalItem, error) {
	different := json.RawMessage(llmreq.MergeCanonical(llmreq.MergeDifferent))
	cands := plan.MergeCandidates()
	var out []database.AuthorLLMNewEvalItem
	ids := make([]int, 0, len(natural))
	for i := range natural {
		ids = append(ids, i)
	}
	slices.Sort(ids)
	for _, i := range ids {
		c := &cands[i]
		if !stored[string(c.Digest[:])] {
			continue
		}
		input := plan.MergeInput(i)
		if len(input) > llmreq.MergeItemMaxBytes {
			if err := d.skip(ctx, run, &database.AuthorLLMEvalSkip{Reason: evalSkipInputTooLong,
				SourceFingerprint: c.Digest[:]}); err != nil {
				return nil, err
			}
			continue
		}
		out = append(out, database.AuthorLLMNewEvalItem{ItemSet: models.AuthorLLMEvalAdversarial,
			Stratum: mergeStratum(c.Kind, c.Script), SourceFingerprint: bytes.Clone(c.Digest[:]),
			ExtractorVersion: authorentity.MergeCandidateVersion, Input: bytes.Clone(input), Weight: 1,
			Expected: different})
	}
	return out, nil
}

// mergeAnchorsOf are the plan's known-same anchors of the kinds' shapes.
func mergeAnchorsOf(plan *authorentity.Plan, kinds []string) []authorentity.MergeAnchorPair {
	var out []authorentity.MergeAnchorPair
	if slices.Contains(kinds, authorentity.CandidateSwap) || slices.Contains(kinds, authorentity.CandidateInitials) {
		out = append(out, plan.MergeAnchorPairs(nil)...)
	}
	if slices.Contains(kinds, authorentity.CandidateBucket) {
		out = append(out, plan.MergeBucketAnchorPairs()...)
	}
	if slices.Contains(kinds, authorentity.CandidateTranslit) {
		out = append(out, plan.MergeTranslitAnchorPairs()...)
	}
	return out
}

// mergeEvalOrder is the merge eval's sampling order.
func mergeEvalOrder(key []byte) []byte {
	sum := sha256.Sum256(append([]byte(mergeEvalSalt), key...))
	return sum[:]
}

// renderMergeItem renders one constructed item's input; a render that
// panicked or an input over the bound is skipped, recorded by its key.
func (d *AuthorLLMEvalDriver) renderMergeItem(
	ctx context.Context, run *evalRun, item *database.AuthorLLMNewEvalItem, render func() authorentity.MergeInput,
) (bool, error) {
	var input []byte
	err := isolateItem(func() error {
		in := render()
		var err error
		input, err = json.Marshal(&in)
		return err
	})
	reason := ""
	switch {
	case errors.Is(err, errAuthorLLMItemPanic):
		reason = evalSkipItemPanic
	case err != nil:
		return false, err
	case len(input) > llmreq.MergeItemMaxBytes:
		reason = evalSkipInputTooLong
	}
	if reason != "" {
		return false, d.skip(ctx, run, &database.AuthorLLMEvalSkip{Reason: reason, SourceFingerprint: item.SourceFingerprint})
	}
	item.Input = input
	return true, nil
}

// suitePick is one suite item to render: a pair in a kind's shape, and
// whether its names are romanized.
type suitePick struct {
	pair      authorentity.MergeSuitePair
	shape     string
	script    string
	romanized bool
}

// selectSuite picks each shape's suite items from distinct pairs (§3.5):
// SuiteLatinPerShape Latin items — the natural Latin pairs first, then
// Cyrillic pairs romanized — and the rest Cyrillic. The translit shape
// romanizes one side of a Cyrillic pair by itself, so it takes no Latin
// pair and natural fewer: the natural namesakes asked beside it.
func selectSuite(pairs []authorentity.MergeSuitePair, shapes []string, sizes *AuthorLLMMergeEvalSizes,
	natural int) []suitePick {
	var latin, cyrl []authorentity.MergeSuitePair
	for _, p := range pairs {
		if p.Script == authorentity.ScriptLatn {
			latin = append(latin, p)
		} else {
			cyrl = append(cyrl, p)
		}
	}
	var out []suitePick
	take := func(from *[]authorentity.MergeSuitePair, shape, script string, romanized bool, n int) int {
		taken := 0
		for taken < n && len(*from) > 0 {
			out = append(out, suitePick{pair: (*from)[0], shape: shape, script: script, romanized: romanized})
			*from = (*from)[1:]
			taken++
		}
		return taken
	}
	for _, shape := range shapes {
		if shape == authorentity.CandidateTranslit {
			take(&cyrl, shape, authorentity.ScriptCyrlLatn, false, sizes.SuitePerShape-natural)
			continue
		}
		latinN := min(sizes.SuiteLatinPerShape, sizes.SuitePerShape)
		taken := take(&latin, shape, authorentity.ScriptLatn, false, latinN)
		take(&cyrl, shape, authorentity.ScriptLatn, true, latinN-taken)
		take(&cyrl, shape, authorentity.ScriptCyrl, false, sizes.SuitePerShape-latinN)
	}
	return out
}

// selectAnchors takes the first n anchors of each kind.
func selectAnchors(anchors []authorentity.MergeAnchorPair, kinds []string, n int) []authorentity.MergeAnchorPair {
	var out []authorentity.MergeAnchorPair
	for _, kind := range kinds {
		taken := 0
		for _, a := range anchors {
			if a.Kind == kind && taken < n {
				out = append(out, a)
				taken++
			}
		}
	}
	return out
}

// mergeItems reads frozen items of one of the run's sets.
func (d *AuthorLLMEvalDriver) mergeItems(
	ctx context.Context, db pg.DBI, version string, set models.AuthorLLMEvalItemSet,
) ([]database.AuthorLLMEvalItemRow, error) {
	return database.AuthorLLMEvalItems(ctx, db, []string{version}, []models.AuthorLLMEvalItemSet{set})
}

// mergeEstimate records the run's token estimate from the frozen sets and
// the pilot's measured figures (an upper one for the suite: the judges are
// asked only where the pair agreed).
func (d *AuthorLLMEvalDriver) mergeEstimate(ctx context.Context, run *evalRun) error {
	sets := mergeEvalSetsOf(run.Mode)
	count := func(version string, set models.AuthorLLMEvalItemSet) (int, error) {
		c, err := database.CountAuthorLLMEvalItems(ctx, d.db, []string{version}, []models.AuthorLLMEvalItemSet{set})
		return c.Items, err
	}
	var n mergeEvalCounts
	var err error
	for _, c := range []struct {
		to      *int
		version string
		set     models.AuthorLLMEvalItemSet
	}{{&n.holdout, sets.holdout, models.AuthorLLMEvalHoldout}, {&n.supplement, sets.supplement, models.AuthorLLMEvalHoldout},
		{&n.suite, sets.suite, models.AuthorLLMEvalAdversarial}, {&n.anchors, sets.anchor, models.AuthorLLMEvalAnchor}} {
		if *c.to, err = count(c.version, c.set); err != nil {
			return err
		}
	}
	return database.SetAuthorLLMRunEstimate(ctx, d.db, run.ID, mergeEstimateTokens(&n, d.mergeSizesOf(run.Mode)))
}

// mergeEvalCounts are a merge eval's frozen items per set.
type mergeEvalCounts struct{ holdout, supplement, suite, anchors int }

// mergeEstimateTokens is a merge eval's token estimate: every participant
// on each holdout, supplement, repeated and anchor item, the suite per
// item, and the run's fixed part, at the pilot's measured figures.
func mergeEstimateTokens(n *mergeEvalCounts, sizes *AuthorLLMMergeEvalSizes) int64 {
	answers := evalParticipants * (n.holdout + n.supplement + min(n.holdout, sizes.Repeat))
	return int64(answers)*mergeTokensPerAnswer + int64(evalParticipants*n.anchors)*mergeTokensPerAnchor +
		int64(n.suite)*mergeTokensPerSuiteItem + mergeTokensPerRunOverall
}

func (d *AuthorLLMEvalDriver) enqueueMergeFingerprint(ctx context.Context, tx *pg.Tx, run *evalRun) error {
	items, err := d.mergeItems(ctx, tx, mergeEvalFingerprintSet, models.AuthorLLMEvalFingerprint)
	if err != nil {
		return err
	}
	for _, slot := range llmreq.Slots() {
		for r := range d.mergeSizesOf(run.Mode).FingerprintRepeats {
			if err := d.enqueue(ctx, tx, &database.AuthorLLMEvalJobSpec{RunID: run.ID, Slot: string(slot),
				Purpose: models.AuthorLLMPurposeEvalFingerprint, RepeatNo: r, OutputMode: d.mode(run, slot),
				ItemIDs: itemIDs(items)}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *AuthorLLMEvalDriver) enqueueMergeAnchor(ctx context.Context, tx *pg.Tx, run *evalRun) error {
	items, err := d.mergeItems(ctx, tx, mergeEvalSetsOf(run.Mode).anchor, models.AuthorLLMEvalAnchor)
	if err != nil {
		return err
	}
	for _, slot := range llmreq.Slots() {
		if err := d.enqueue(ctx, tx, &database.AuthorLLMEvalJobSpec{RunID: run.ID, Slot: string(slot),
			Purpose: models.AuthorLLMPurposeEvalAnchor, OutputMode: d.mode(run, slot), ItemIDs: itemIDs(items)}); err != nil {
			return err
		}
	}
	return nil
}

// mergeMeasured are the measured items of the run: the holdout with
// merge3's extra Latin questions, and the suite.
func (d *AuthorLLMEvalDriver) mergeMeasured(
	ctx context.Context, db pg.DBI, run *evalRun,
) (holdout, suite []database.AuthorLLMEvalItemRow, err error) {
	sets := mergeEvalSetsOf(run.Mode)
	holdout, err = database.AuthorLLMEvalItems(ctx, db, []string{sets.holdout, sets.supplement},
		[]models.AuthorLLMEvalItemSet{models.AuthorLLMEvalHoldout})
	if err != nil {
		return nil, nil, err
	}
	suite, err = d.mergeItems(ctx, db, sets.suite, models.AuthorLLMEvalAdversarial)
	return holdout, suite, err
}

// isSupplement reports an extra question, gated outside |A|.
func isSupplement(row *database.AuthorLLMEvalItemRow) bool {
	return strings.HasSuffix(row.SetVersion, ".supplement")
}

// mergeSupplementSet is the report's set name of the extra questions.
const mergeSupplementSet = "supplement"

// reportSet is an item's set as the report counts it: the extra questions
// apart from the holdout.
func reportSet(row *database.AuthorLLMEvalItemRow) string {
	if isSupplement(row) {
		return mergeSupplementSet
	}
	return row.ItemSet
}

// enqueueMergeCandidates asks the production pair every holdout and suite
// item, one per request, without an excerpt.
func (d *AuthorLLMEvalDriver) enqueueMergeCandidates(ctx context.Context, tx *pg.Tx, run *evalRun) error {
	holdout, suite, err := d.mergeMeasured(ctx, tx, run)
	if err != nil {
		return err
	}
	ids := append(itemIDs(holdout), itemIDs(suite)...)
	for _, slot := range []llmreq.Slot{llmreq.SlotProductionA, llmreq.SlotProductionB} {
		if err := d.enqueue(ctx, tx, &database.AuthorLLMEvalJobSpec{RunID: run.ID, Slot: string(slot),
			Purpose: models.AuthorLLMPurposeEvalCandidate, Arm: llmeval.ArmA, BatchSize: 1,
			OutputMode: d.mode(run, slot), ItemIDs: ids}); err != nil {
			return err
		}
	}
	return nil
}

// enqueueMergeJudges asks both judges every holdout item, and the suite
// items the production pair agreed are one person: only those could be
// accepted, so only their gold matters (§7).
func (d *AuthorLLMEvalDriver) enqueueMergeJudges(ctx context.Context, tx *pg.Tx, run *evalRun) error {
	holdout, suite, err := d.mergeMeasured(ctx, tx, run)
	if err != nil {
		return err
	}
	answers, err := database.AuthorLLMEvalAnswers(ctx, tx, run.ID)
	if err != nil {
		return err
	}
	same := map[int64]int{}
	for i := range answers {
		a := &answers[i]
		if a.Purpose == string(models.AuthorLLMPurposeEvalCandidate) && a.RepeatNo == 0 &&
			a.Status == string(models.AuthorLLMJobAnswered) && hex.EncodeToString(a.CanonicalSHA256) == mergeSameCanonical {
			same[a.EvalItemID]++
		}
	}
	ids := itemIDs(holdout)
	for i := range suite {
		if same[suite[i].ID] == evalParticipants-evalJudges {
			ids = append(ids, suite[i].ID)
		}
	}
	for _, slot := range []llmreq.Slot{llmreq.SlotJudgeA, llmreq.SlotJudgeB} {
		if err := d.enqueue(ctx, tx, &database.AuthorLLMEvalJobSpec{RunID: run.ID, Slot: string(slot),
			Purpose: models.AuthorLLMPurposeEvalJudge, OutputMode: d.mode(run, slot), ItemIDs: ids}); err != nil {
			return err
		}
	}
	return nil
}

// enqueueMergeRepeat asks the first Repeat holdout items, in the sampling
// order, once more of every participant (never the extra questions or the
// suite: the suite's accepts are decided by adjudication of the first
// answers, and a repeat there would feed nothing the gate reads).
func (d *AuthorLLMEvalDriver) enqueueMergeRepeat(ctx context.Context, tx *pg.Tx, run *evalRun) error {
	holdout, err := d.mergeItems(ctx, tx, mergeEvalSetsOf(run.Mode).holdout, models.AuthorLLMEvalHoldout)
	if err != nil {
		return err
	}
	slices.SortStableFunc(holdout, func(x, y database.AuthorLLMEvalItemRow) int {
		return bytes.Compare(mergeEvalOrder(x.SourceFingerprint), mergeEvalOrder(y.SourceFingerprint))
	})
	ids := itemIDs(holdout[:min(len(holdout), d.mergeSizesOf(run.Mode).Repeat)])
	for _, slot := range llmreq.Slots() {
		arm := llmeval.ArmA
		if slot == llmreq.SlotJudgeA || slot == llmreq.SlotJudgeB {
			arm = ""
		}
		if err := d.enqueue(ctx, tx, &database.AuthorLLMEvalJobSpec{RunID: run.ID, Slot: string(slot),
			Purpose: models.AuthorLLMPurposeEvalRepeat, Arm: arm, RepeatNo: 1, OutputMode: d.mode(run, slot),
			ItemIDs: ids}); err != nil {
			return err
		}
	}
	return nil
}

// learnMergeFingerprint evaluates the fingerprint stage and, when it
// passed, records what was learned on the run, as K's learnFingerprint.
func (d *AuthorLLMEvalDriver) learnMergeFingerprint(ctx context.Context, run *evalRun) (bool, error) {
	answers, err := d.readEvalAnswers(ctx, run.ID)
	if err != nil {
		return false, err
	}
	fingerprints, result := d.fingerprintResult(answers)
	if !result.OK {
		return false, nil
	}
	return true, d.recordLearned(ctx, run, fingerprints, answers.modes)
}

// mergeEvalReportDoc is a merge eval's stored report: identifiers, closed
// values and counts only, never a name or a title.
type mergeEvalReportDoc struct {
	ReportVersion int                              `json:"report_version"`
	RunID         int64                            `json:"run_id"`
	Mode          string                           `json:"mode"`
	ConfigVersion string                           `json:"config_version"`
	Task          string                           `json:"task"`
	Gate          string                           `json:"gate"`
	Options       llmeval.Options                  `json:"options"`
	Settings      AuthorLLMEvalSettings            `json:"settings"`
	Sets          []evalSetSummary                 `json:"sets"`
	Skipped       []evalSkipped                    `json:"skipped"`
	FailedJobs    []evalFailedJob                  `json:"failed_jobs"`
	Fingerprints  []llmeval.ParticipantFingerprint `json:"fingerprints"`
	// Recall is the known-same anchors per kind and script; Answers the
	// answers per set, stratum and participant; Tokens the tokens spent per
	// item of each set and stratum.
	Recall     []mergeRecall      `json:"recall"`
	Answers    []mergeAnswerCount `json:"answers"`
	Tokens     []mergeTokenCount  `json:"tokens"`
	Evaluation llmeval.Report     `json:"evaluation"`
}

// mergeRecall is the known-same anchors of one kind and script: per
// participant how many it answered same, and how many the production rule
// (the pair and both judges same) would accept.
type mergeRecall struct {
	Kind         string                     `json:"kind"`
	Script       string                     `json:"script"`
	Items        int                        `json:"items"`
	Participants []llmeval.ParticipantCount `json:"participants"`
	Accepted     int                        `json:"accepted"`
}

// mergeAnswerCount is one participant's answers in a set and stratum.
type mergeAnswerCount struct {
	ItemSet     string `json:"item_set"`
	Stratum     string `json:"stratum"`
	Participant string `json:"participant"`
	Purpose     string `json:"purpose"`
	Same        int    `json:"same"`
	Different   int    `json:"different"`
	CannotTell  int    `json:"cannot_tell"`
	Invalid     int    `json:"invalid"`
}

// mergeTokenCount is the tokens a set and stratum's answered jobs spent.
type mergeTokenCount struct {
	ItemSet       string  `json:"item_set"`
	Stratum       string  `json:"stratum"`
	Items         int     `json:"items"`
	Answers       int     `json:"answers"`
	Tokens        int64   `json:"tokens"`
	TokensPerItem float64 `json:"tokens_per_item"`
}

// mergeOptions are the merge gate's options (§3.1, §3.3, §4.5): a census
// mode's strata pass on no error and every candidate settled; a holdout's
// (merge0, merge3's bucket) on the bound.
func mergeOptions(mode string) llmeval.Options {
	rule := llmeval.GateRuleHoldout
	if mergeEvalModes[mode].census {
		rule = llmeval.GateRuleCensus
	}
	return llmeval.Options{MaxErrorShare: mergeEvalMaxErrorShare, ContestedIsError: true,
		ContestedAdjudicated: true, AcceptOnly: mergeSameCanonical, AnchorRecall: true, ByScript: true,
		GateRule: rule}
}

// mergeReport computes the report, stores it with its digest and completes
// the run.
func (d *AuthorLLMEvalDriver) mergeReport(ctx context.Context, run *evalRun) error {
	generation, err := database.AuthorLLMRelearnGeneration(ctx, d.db, run.ID)
	if err != nil {
		return err
	}
	waits, err := database.AuthorLLMEvalRunWaits(ctx, d.db, run.ID)
	if err != nil || waits > 0 {
		return err
	}
	doc, err := d.mergeReportDoc(ctx, run)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	err = database.StoreAuthorLLMEvalReportOf(ctx, d.db, run.ID, d.version, raw, generation)
	if errors.Is(err, database.ErrAuthorLLMReportStale) {
		return nil
	}
	return err
}

// mergeReportDoc computes a merge eval's report from the run's stored
// answers and the suite's current adjudications.
func (d *AuthorLLMEvalDriver) mergeReportDoc(ctx context.Context, run *evalRun) (*mergeEvalReportDoc, error) {
	answers, err := d.readEvalAnswers(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	holdout, suite, err := d.mergeMeasured(ctx, d.db, run)
	if err != nil {
		return nil, err
	}
	anchors, err := d.mergeItems(ctx, d.db, mergeEvalSetsOf(run.Mode).anchor, models.AuthorLLMEvalAnchor)
	if err != nil {
		return nil, err
	}
	// The suite's labels and the contested accepts' (any measured item may
	// be one).
	adjudications, err := database.AuthorLLMAdjudications(ctx, d.db, itemIDs(slices.Concat(holdout, suite)))
	if err != nil {
		return nil, err
	}
	fingerprints, fpResult := d.fingerprintResult(answers)
	in := mergeEvalInput(run.Mode, answers, holdout, suite, adjudications)
	in.Fingerprint = fpResult
	recall, anchorCount := mergeRecallOf(anchors, answers.byItem)
	in.Anchor = anchorCount
	opts := mergeOptions(run.Mode)
	evaluation := llmeval.EvaluateWith(&in, &opts)
	doc := &mergeEvalReportDoc{ReportVersion: 1, RunID: run.ID, Mode: run.Mode, ConfigVersion: d.version,
		Task: llmreq.TaskMerge, Gate: mergeEvalGate, Options: opts, Settings: d.reportSettings(run, answers),
		FailedJobs: answers.failed, Fingerprints: fingerprints,
		Recall: recall, Answers: mergeAnswerCounts(holdout, suite, anchors, answers.byItem),
		Tokens: mergeTokenCounts(holdout, suite, anchors, answers.byItem), Evaluation: evaluation}
	if setsErr := d.reportSets(ctx, run, doc); setsErr != nil {
		return nil, setsErr
	}
	return doc, nil
}

// mergeEvalInput is the llmeval input of the measured items: the holdout
// and the suite with its adjudications, one combo (arm A, one per request),
// strata the kinds. A report of the mode may back a registration as K's
// may (evalRegistrable): only a dev run's never does.
func mergeEvalInput(
	mode string, answers *evalAnswers, holdout, suite []database.AuthorLLMEvalItemRow,
	adjudications map[int64]database.AuthorLLMAdjudication,
) llmeval.Input {
	combos := []llmeval.Combo{{Arm: llmeval.ArmA, Batch: 1}}
	in := llmeval.Input{Combos: combos, Invalid: sortedCounts(answers.invalid),
		ProviderRejected: sortedCounts(answers.rejected), Registrable: evalRegistrable(mode)}
	repeat := map[string]*llmeval.ParticipantCount{}
	for _, rows := range [][]database.AuthorLLMEvalItemRow{holdout, suite} {
		for i := range rows {
			jobs := answers.byItem[rows[i].ID]
			item := evalItem(&rows[i], jobs, combos)
			item.Stratum, item.Script = splitMergeStratum(rows[i].Stratum)
			item.Holdout = true
			item.Adversarial = rows[i].ItemSet == string(models.AuthorLLMEvalAdversarial)
			item.Adjudication = adjudications[rows[i].ID].Label
			item.Supplement = isSupplement(&rows[i])
			if !item.Adversarial && !item.Supplement {
				item.Repeated = countRepeats(repeat, jobs)
			}
			in.Items = append(in.Items, item)
		}
	}
	in.Repeat = sortedCounts(repeat)
	return in
}

// reportSettings are the run's settings with the mode each participant
// was asked in.
func (d *AuthorLLMEvalDriver) reportSettings(run *evalRun, answers *evalAnswers) AuthorLLMEvalSettings {
	settings := run.Settings
	settings.OutputModes = map[string]string{}
	for _, slot := range llmreq.Slots() {
		mode := d.mode(run, slot)
		if m := answers.modes[string(slot)]; m != "" && run.Settings.mode(slot) == "" {
			mode = m
		}
		settings.OutputModes[string(slot)] = mode
	}
	return settings
}

// reportSets fills the report's sets, skips and failed jobs.
func (d *AuthorLLMEvalDriver) reportSets(ctx context.Context, run *evalRun, doc *mergeEvalReportDoc) error {
	versions := append(mergeEvalSetsOf(run.Mode).all(), mergeEvalFingerprintSet)
	setRows, err := database.AuthorLLMEvalSetsByVersion(ctx, d.db, versions)
	if err != nil {
		return err
	}
	skips, err := database.AuthorLLMEvalSkips(ctx, d.db, run.ID, versions)
	if err != nil {
		return err
	}
	doc.Skipped = make([]evalSkipped, 0, len(skips))
	if doc.FailedJobs == nil {
		doc.FailedJobs = []evalFailedJob{}
	}
	for i := range skips {
		sk := &skips[i]
		doc.Skipped = append(doc.Skipped, evalSkipped{Stage: sk.Stage, Reason: sk.Reason, SetVersion: deref(sk.SetVersion),
			Fingerprint: hex.EncodeToString(sk.SourceFingerprint), EvalItemID: deref(sk.EvalItemID), BookID: deref(sk.BookID)})
	}
	for _, s := range setRows {
		doc.Sets = append(doc.Sets, evalSetSummary{SetVersion: s.SetVersion, SHA256: hex.EncodeToString(s.SHA256), Items: s.Items})
	}
	return nil
}

// mergeRecallOf counts the known-same anchors per kind and script, and per
// participant over all of them (the report's anchor counts, never gated).
func mergeRecallOf(
	anchors []database.AuthorLLMEvalItemRow, byItem map[int64]map[evalAnswerKey]*database.AuthorLLMEvalAnswer,
) ([]mergeRecall, []llmeval.ParticipantCount) {
	purpose := string(models.AuthorLLMPurposeEvalAnchor)
	byStratum := map[string]*mergeRecall{}
	var names []string
	total := map[string]*llmeval.ParticipantCount{}
	for i := range anchors {
		it := &anchors[i]
		r := byStratum[it.Stratum]
		if r == nil {
			kind, script := splitMergeStratum(it.Stratum)
			r = &mergeRecall{Kind: kind, Script: script}
			for _, slot := range llmreq.Slots() {
				r.Participants = append(r.Participants, llmeval.ParticipantCount{Participant: string(slot)})
			}
			byStratum[it.Stratum] = r
			names = append(names, it.Stratum)
		}
		r.Items++
		all := true
		for j, slot := range llmreq.Slots() {
			got := answerOf(byItem[it.ID][evalAnswerKey{purpose, string(slot), "", 1, 0}])
			ok := got.Valid && !got.Refused && got.Canonical == mergeSameCanonical
			r.Participants[j].Total++
			c := total[string(slot)]
			if c == nil {
				c = &llmeval.ParticipantCount{Participant: string(slot)}
				total[string(slot)] = c
			}
			c.Total++
			if ok {
				r.Participants[j].Matched++
				c.Matched++
			}
			all = all && ok
		}
		if all {
			r.Accepted++
		}
	}
	slices.Sort(names)
	out := make([]mergeRecall, 0, len(names))
	for _, n := range names {
		out = append(out, *byStratum[n])
	}
	return out, sortedCounts(total)
}

// mergeAnswerCounts counts every participant's first answers per set and
// stratum.
func mergeAnswerCounts(
	holdout, suite, anchors []database.AuthorLLMEvalItemRow,
	byItem map[int64]map[evalAnswerKey]*database.AuthorLLMEvalAnswer,
) []mergeAnswerCount {
	type key struct{ set, stratum, slot, purpose string }
	counts := map[key]*mergeAnswerCount{}
	var keys []key
	for _, rows := range [][]database.AuthorLLMEvalItemRow{holdout, suite, anchors} {
		for i := range rows {
			it := &rows[i]
			for k, a := range byItem[it.ID] {
				if k.repeat != 0 || !models.AuthorLLMJobStatus(a.Status).IsTerminal() {
					continue
				}
				ck := key{reportSet(it), it.Stratum, k.slot, k.purpose}
				c := counts[ck]
				if c == nil {
					c = &mergeAnswerCount{ItemSet: ck.set, Stratum: it.Stratum, Participant: k.slot, Purpose: k.purpose}
					counts[ck] = c
					keys = append(keys, ck)
				}
				switch got := answerOf(a); {
				case !got.Valid:
					c.Invalid++
				case got.Canonical == mergeSameCanonical:
					c.Same++
				case got.Canonical == mergeDifferentCanonical:
					c.Different++
				case got.Canonical == mergeCannotTellCanon:
					c.CannotTell++
				default:
					c.Invalid++
				}
			}
		}
	}
	slices.SortFunc(keys, func(x, y key) int {
		return strings.Compare(x.set+"|"+x.stratum+"|"+x.purpose+"|"+x.slot, y.set+"|"+y.stratum+"|"+y.purpose+"|"+y.slot)
	})
	out := make([]mergeAnswerCount, 0, len(keys))
	for _, k := range keys {
		out = append(out, *counts[k])
	}
	return out
}

// mergeTokenCounts sums the tokens of every answered job's call per set and
// stratum: each call carries one item, so its tokens are the item's.
func mergeTokenCounts(
	holdout, suite, anchors []database.AuthorLLMEvalItemRow,
	byItem map[int64]map[evalAnswerKey]*database.AuthorLLMEvalAnswer,
) []mergeTokenCount {
	type key struct{ set, stratum string }
	counts := map[key]*mergeTokenCount{}
	var keys []key
	for _, rows := range [][]database.AuthorLLMEvalItemRow{holdout, suite, anchors} {
		for i := range rows {
			it := &rows[i]
			k := key{reportSet(it), it.Stratum}
			c := counts[k]
			if c == nil {
				c = &mergeTokenCount{ItemSet: k.set, Stratum: it.Stratum}
				counts[k] = c
				keys = append(keys, k)
			}
			c.Items++
			for _, a := range byItem[it.ID] {
				if a.Status != string(models.AuthorLLMJobAnswered) || a.PromptTokens == nil {
					continue
				}
				c.Answers++
				c.Tokens += *a.PromptTokens + deref(a.CompletionTokens)
			}
		}
	}
	slices.SortFunc(keys, func(x, y key) int { return strings.Compare(x.set+"|"+x.stratum, y.set+"|"+y.stratum) })
	out := make([]mergeTokenCount, 0, len(keys))
	for _, k := range keys {
		c := counts[k]
		if c.Items > 0 {
			c.TokensPerItem = float64(c.Tokens) / float64(c.Items)
		}
		out = append(out, *c)
	}
	return out
}
