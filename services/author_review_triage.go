package services

import (
	"cmp"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/internal/authornorm/llmres"
	"gopds-api/internal/authortriage"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// The review triage (design/review-triage): a dry run decides every open
// review item with the rule set authortriage.RuleVersion from what the
// database holds, and stores the plan in the two triage tables. Nothing else
// is written until an administrator applies the plan.

// triagePlanPage is how many open items one read of the plan takes.
const triagePlanPage = 1000

// triageListLimit is how many batches the list shows.
const triageListLimit = 20

// BuildTriagePlan decides every open review item and returns the plan rows
// and their counts. It only reads; the caller gives it one snapshot.
func BuildTriagePlan(ctx context.Context, db pg.DBI) ([]database.TriagePlanItem, json.RawMessage, error) {
	names, err := database.LoadReviewTriageNames(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	env := &authortriage.Env{Given: authorentity.GivenNamesOf(names.Credits)}
	sources := firstUnselectedOf(names.Unselected)
	names = nil

	plan := []database.TriagePlanItem{}
	for after := int64(0); ; {
		page, loadErr := database.LoadTriageOpenItems(ctx, db, authortriage.RuleVersion, after, triagePlanPage)
		if loadErr != nil {
			return nil, nil, loadErr
		}
		if len(page) == 0 {
			break
		}
		decided, decideErr := decideTriagePage(ctx, db, page, sources, env)
		if decideErr != nil {
			return nil, nil, decideErr
		}
		plan = append(plan, decided...)
		after = page[len(page)-1].ID
	}
	counts, err := triagePlanCounts(plan)
	if err != nil {
		return nil, nil, err
	}
	return plan, counts, nil
}

// firstUnselectedOf keeps the lowest-ID credit under review of each
// fingerprint: the source a proposal is read from.
func firstUnselectedOf(unselected []authorentity.Unselected) map[string]*authorentity.Unselected {
	out := make(map[string]*authorentity.Unselected, len(unselected))
	for i := range unselected {
		u := &unselected[i]
		if have, ok := out[u.Fingerprint]; !ok || u.ID < have.ID {
			out[u.Fingerprint] = u
		}
	}
	return out
}

// decideTriagePage decides one page of open items.
func decideTriagePage(
	ctx context.Context, db pg.DBI, page []database.TriageOpenItem, sources map[string]*authorentity.Unselected,
	env *authortriage.Env,
) ([]database.TriagePlanItem, error) {
	fingerprints := make([][]byte, 0, len(page))
	var entities []uuid.UUID
	for i := range page {
		fingerprints = append(fingerprints, page[i].Fingerprint)
		if page[i].EntityRule == authortriage.StepSameName.Name() && page[i].EntityID != nil {
			entities = append(entities, *page[i].EntityID)
		}
	}
	stored, err := database.LoadTriageStoredAnswers(ctx, db, fingerprints)
	if err != nil {
		return nil, err
	}
	answers := map[string][]*database.TriageStoredAnswer{}
	for i := range stored {
		fp := string(stored[i].Fingerprint)
		answers[fp] = append(answers[fp], &stored[i])
	}
	sameName, err := database.LoadTriageSameNameStructures(ctx, db, entities)
	if err != nil {
		return nil, err
	}
	out := make([]database.TriagePlanItem, 0, len(page))
	for i := range page {
		item := &page[i]
		c := triageCandidate(item, answers[string(item.Fingerprint)], sources[string(item.Fingerprint)])
		if item.EntityID != nil && item.EntityRule == authortriage.StepSameName.Name() {
			c.SameName = sameName[*item.EntityID]
		}
		d := authortriage.Decide(c, env)
		row, rowErr := triagePlanRow(item, &d)
		if rowErr != nil {
			return nil, rowErr
		}
		out = append(out, row)
	}
	return out, nil
}

// triageCandidate gathers an item's facts for the rule set.
func triageCandidate(
	item *database.TriageOpenItem, stored []*database.TriageStoredAnswer, source *authorentity.Unselected,
) *authortriage.Candidate {
	c := &authortriage.Candidate{ItemID: item.ID, Class: item.Class, Reason: item.Reason,
		CreditScoped: item.ScopeCreditID != nil, HasProposal: item.Proposal != nil, Credits: item.Credits,
		Source: source, EntityRule: item.EntityRule, Pinned: item.Pinned, UndoneBefore: item.UndoneBefore}
	if item.Proposal != nil {
		c.Proposal = proposalName(item.Proposal)
	}
	for _, a := range stored {
		c.Answers = append(c.Answers, triageAnswer(a, item.Class))
	}
	return c
}

// proposalName is a result's decided name as the rule set compares it.
func proposalName(p *models.ContributorNormalizationResult) authortriage.Name {
	return authortriage.Name{Given: deref(p.GivenName), Additional: strings.Join(p.AdditionalNames, " "),
		Family: deref(p.FamilyName), Nickname: deref(p.Nickname), Prefix: deref(p.Prefix), Suffix: deref(p.Suffix),
		Display: deref(p.DisplayName), Sort: deref(p.SortName), Kind: string(p.Kind)}
}

// triageAnswer reads one stored answer: its form, and the name it assembles
// to now. A refusal assembles nothing, and an answer that no longer reads or
// assembles has no name.
func triageAnswer(a *database.TriageStoredAnswer, class string) authortriage.Answer {
	out := authortriage.Answer{RunID: a.RunID, Arm: a.Arm, BatchSize: a.BatchSize, Slot: a.Slot,
		Canonical: hex.EncodeToString(a.Canonical), Refused: a.Refused}
	rep, err := llmres.ParseStoredReply(a.Reply)
	if err == nil {
		out.Form = string(rep.Form)
	}
	if a.Refused || err != nil {
		if out.Form == "" && a.Refused {
			out.Form = string(llmres.FormCannotTell)
		}
		return out
	}
	var input llmreq.ItemInput
	if json.Unmarshal(a.Input, &input) != nil {
		return out
	}
	in, err := llmres.NewInput(input.Fields)
	if err != nil {
		return out
	}
	assembled, err := llmres.Assemble(&in, &rep, a.ExtractorVersion, a.ConfigVersion,
		authornorm.LLMClass(authornorm.DecisionClass(class)))
	if err != nil {
		return out
	}
	r := &assembled.Result
	out.Name = &authortriage.Name{Given: r.GivenName, Additional: r.AdditionalNames, Family: r.FamilyName,
		Nickname: r.Nickname, Prefix: r.Prefix, Suffix: r.Suffix, Display: r.DisplayName, Sort: r.SortName,
		Kind: string(r.Kind)}
	return out
}

// triagePlanRow is the stored form of one decision.
func triagePlanRow(item *database.TriageOpenItem, d *authortriage.Decision) (database.TriagePlanItem, error) {
	row := database.TriagePlanItem{ReviewItemID: item.ID, Fingerprint: item.Fingerprint, Class: item.Class,
		Credits: item.Credits, ProposalResultID: item.ProposalResultID}
	switch {
	case d.Excluded != "":
		row.Outcome, row.Exclusion = database.TriageOutcomeExcluded, string(d.Excluded)
		return row, nil
	case d.Step == 0:
		row.Outcome = database.TriageOutcomeLeft
		return row, nil
	case d.Step < authortriage.StepFourModel || d.Step > authortriage.StepParticleOnly:
		return row, fmt.Errorf("services: the rule set decided an unknown step %d", d.Step)
	}
	planned, err := json.Marshal(d.After)
	if err != nil {
		return row, fmt.Errorf("encoding the planned name: %w", err)
	}
	evidence, err := json.Marshal(d.Evidence)
	if err != nil {
		return row, fmt.Errorf("encoding the evidence: %w", err)
	}
	row.Outcome, row.Step, row.Rule, row.Action = database.TriageOutcomePlanned, int16(d.Step), d.Rule, string(d.Action)
	row.Planned, row.Evidence = planned, evidence
	return row, nil
}

// triageCountsJSON is the plan's own totals, kept on the batch.
type triageCountsJSON struct {
	RuleVersion string                 `json:"rule_version"`
	Cells       []triageCellCount      `json:"cells"`
	Left        []database.TriageCount `json:"left"`
	Excluded    []database.TriageCount `json:"excluded"`
}

type triageCellCount struct {
	Step    int16  `json:"step"`
	Class   string `json:"class"`
	Items   int    `json:"items"`
	Credits int    `json:"credits"`
}

// triagePlanCounts counts the plan per (step, class), and what it left per
// class and excluded per reason.
func triagePlanCounts(plan []database.TriagePlanItem) (json.RawMessage, error) {
	type cellKey struct {
		step  int16
		class string
	}
	cells := map[cellKey]*triageCellCount{}
	left, excluded := map[string]*database.TriageCount{}, map[string]*database.TriageCount{}
	add := func(m map[string]*database.TriageCount, key string, credits int) {
		if m[key] == nil {
			m[key] = &database.TriageCount{Key: key}
		}
		m[key].Items++
		m[key].Credits += credits
	}
	for i := range plan {
		p := &plan[i]
		switch p.Outcome {
		case database.TriageOutcomeLeft:
			add(left, p.Class, p.Credits)
		case database.TriageOutcomeExcluded:
			add(excluded, p.Exclusion, p.Credits)
		default:
			k := cellKey{p.Step, p.Class}
			if cells[k] == nil {
				cells[k] = &triageCellCount{Step: p.Step, Class: p.Class}
			}
			cells[k].Items++
			cells[k].Credits += p.Credits
		}
	}
	out := triageCountsJSON{RuleVersion: authortriage.RuleVersion, Cells: []triageCellCount{},
		Left: sortedTriageCounts(left), Excluded: sortedTriageCounts(excluded)}
	for _, c := range cells {
		out.Cells = append(out.Cells, *c)
	}
	slices.SortFunc(out.Cells, func(a, b triageCellCount) int {
		return cmp.Or(cmp.Compare(a.Step, b.Step), cmp.Compare(a.Class, b.Class))
	})
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encoding the plan's counts: %w", err)
	}
	return raw, nil
}

func sortedTriageCounts(m map[string]*database.TriageCount) []database.TriageCount {
	out := make([]database.TriageCount, 0, len(m))
	for _, c := range m {
		out = append(out, *c)
	}
	slices.SortFunc(out, func(a, b database.TriageCount) int {
		return cmp.Or(cmp.Compare(b.Items, a.Items), cmp.Compare(a.Key, b.Key))
	})
	return out
}

// AuthorReviewTriageStatuses are the statuses the triage's events carry: the
// outcomes of an item's apply and undo.
func AuthorReviewTriageStatuses() []string {
	return []string{database.TriageOutcomeApplied, database.TriageOutcomeSkippedChanged,
		database.TriageOutcomeConflict, database.TriageOutcomeFailed, database.TriageUndoRestored,
		database.TriageUndoSuperseded, database.TriageUndoBlocked, database.TriageUndoGone}
}

// defaultTriageStopWait is how long a stop waits for the item in flight:
// below the server's write timeout (10 s by default), so the request answers
// ErrTriageStopBusy instead of losing its connection.
const defaultTriageStopWait = 5 * time.Second

// AuthorReviewTriageAdmin is the administrator's side of the triage: start a
// dry run, read the batches and a plan, export the audit sample, stop or
// undo an apply. stopWait bounds a stop's wait for the item in flight.
type AuthorReviewTriageAdmin struct {
	db       *pg.DB
	stopWait time.Duration
}

// NewAuthorReviewTriageAdmin builds the admin over db.
func NewAuthorReviewTriageAdmin(db *pg.DB) *AuthorReviewTriageAdmin {
	return &AuthorReviewTriageAdmin{db: db, stopWait: defaultTriageStopWait}
}

// StartDryRun records a dry run of the current rule version; the worker
// plans it. database.ErrTriageActiveBatch while another batch works.
func (a *AuthorReviewTriageAdmin) StartDryRun(ctx context.Context, actor int64) (*database.TriageBatch, error) {
	return database.StartTriageBatch(ctx, a.db, authortriage.RuleVersion, actor)
}

// Batches lists the newest batches.
func (a *AuthorReviewTriageAdmin) Batches(ctx context.Context) ([]database.TriageBatch, error) {
	return database.ListTriageBatches(ctx, a.db, triageListLimit)
}

// Batch reads one batch with its plan's table and outcomes.
func (a *AuthorReviewTriageAdmin) Batch(ctx context.Context, id int64) (*database.TriageBatchView, error) {
	return database.ReadTriageBatch(ctx, a.db, id)
}

// The audit gate's seams: the audited steps and the audit log an apply is
// approved under. Tests replace them.
var (
	auditedStepsFn = authortriage.AuditedSteps
	auditLogFn     = authortriage.AuditLog
)

// Apply hands a planned batch to the worker to apply the steps that passed
// their audit (database.ErrTriageNothingAudited while none has), recording
// that audit on the batch.
func (a *AuthorReviewTriageAdmin) Apply(ctx context.Context, id, actor int64) error {
	steps := []int16{}
	for _, s := range auditedStepsFn() {
		steps = append(steps, int16(s)) // #nosec G115 -- a step is 1..8
	}
	audit, err := json.Marshal(auditLogFn())
	if err != nil {
		return fmt.Errorf("encoding the audit: %w", err)
	}
	return database.RequestTriageApply(ctx, a.db, id, actor, authortriage.RuleVersion, steps, audit)
}

// Undo hands an applied batch to the worker to undo one step, or every step
// when step is 0; it returns how many items it marked.
func (a *AuthorReviewTriageAdmin) Undo(ctx context.Context, id, actor int64, step int16) (int, error) {
	if step < 0 || step > int16(authortriage.StepParticleOnly) {
		return 0, database.ErrTriageBatchState
	}
	return database.RequestTriageUndo(ctx, a.db, id, actor, step)
}

// Stop stops an applying or undoing batch for good: when it returns, no
// item starts any more. The item in flight commits or rolls back first;
// when it does not within stopWait, database.ErrTriageStopBusy and nothing
// is stopped (asked again, the stop succeeds once the item's lifetime is
// over). What was applied stays and can be undone; the rest stays planned.
func (a *AuthorReviewTriageAdmin) Stop(ctx context.Context, id, actor int64) (*database.TriageBatch, error) {
	batch, err := database.StopTriageBatch(ctx, a.db, id, actor, a.stopWait)
	if err != nil {
		return nil, err
	}
	LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
		Name: AuthorMetadataEventTriageStopped, Stage: AuthorMetadataStageReviewTriage, RunID: id,
		Count: batch.ItemsDone,
	})
	return batch, nil
}

// Audit sample pools.
const (
	TriagePoolSample  = "sample"
	TriagePoolReserve = "reserve"
)

// AuditSample is the batch's stratified audit sample: per step,
// SampleSize rows allocated over its classes in proportion, then
// ReserveSize rows the same way over what each class has left. Within a
// class the rows come in the batch's own random order, so the sample is the
// same at every call.
func (a *AuthorReviewTriageAdmin) AuditSample(ctx context.Context, id int64) ([]database.TriageAuditRow, error) {
	if _, err := database.ReadTriageBatch(ctx, a.db, id); err != nil {
		return nil, err
	}
	rows, err := database.TriageAuditCandidates(ctx, a.db, id)
	if err != nil {
		return nil, err
	}
	return allocateTriageAudit(rows), nil
}

// triageHeavyShare: an item that alone carries at least 1/triageHeavyShare
// of its step's credits is always in the step's sample.
const triageHeavyShare = 100

// allocateTriageAudit draws the sample and the reserve from the candidates,
// which come in the batch's random order; each pool keeps that order. The
// heavy items of a step come first (heaviest first, within the sample's
// size), the rest of the sample in proportion to the classes.
func allocateTriageAudit(rows []database.TriageAuditRow) []database.TriageAuditRow {
	byStep := map[int16][]int{}
	stepCredits := map[int16]int{}
	for i := range rows {
		byStep[rows[i].Step] = append(byStep[rows[i].Step], i)
		stepCredits[rows[i].Step] += rows[i].Credits
	}
	steps := make([]int16, 0, len(byStep))
	for s := range byStep {
		steps = append(steps, s)
	}
	slices.Sort(steps)
	out := []database.TriageAuditRow{}
	for _, s := range steps {
		step := authortriage.Step(s)
		heavy := heavyTriageRows(rows, byStep[s], stepCredits[s], authortriage.SampleSize(step))
		classes := map[string][]int{}
		cells := map[string]int{}
		for _, i := range byStep[s] {
			if !heavy[i] {
				classes[rows[i].Class] = append(classes[rows[i].Class], i)
				cells[rows[i].Class]++
			}
		}
		taken := authortriage.AllocateSample(cells, authortriage.SampleSize(step)-len(heavy))
		rest := map[string]int{}
		for class, n := range cells {
			rest[class] = n - taken[class]
		}
		reserve := authortriage.AllocateSample(rest, authortriage.ReserveSize(step))
		sample := drawTriagePool(classes, nil, taken)
		for i := range heavy {
			sample = append(sample, i)
		}
		out = append(out, triagePool(rows, sample, TriagePoolSample)...)
		out = append(out, triagePool(rows, drawTriagePool(classes, taken, reserve), TriagePoolReserve)...)
	}
	return out
}

// heavyTriageRows picks the step's rows that alone carry 1/triageHeavyShare
// of its credits, the heaviest first, at most size of them.
func heavyTriageRows(rows []database.TriageAuditRow, step []int, credits, size int) map[int]bool {
	var heavy []int
	for _, i := range step {
		if credits > 0 && rows[i].Credits*triageHeavyShare >= credits {
			heavy = append(heavy, i)
		}
	}
	slices.SortStableFunc(heavy, func(a, b int) int { return cmp.Compare(rows[b].Credits, rows[a].Credits) })
	out := map[int]bool{}
	for _, i := range heavy[:min(size, len(heavy))] {
		out[i] = true
	}
	return out
}

// triagePool is the rows at the indexes, in the order of rows, marked pool.
func triagePool(rows []database.TriageAuditRow, picked []int, pool string) []database.TriageAuditRow {
	slices.Sort(picked)
	out := make([]database.TriageAuditRow, 0, len(picked))
	for _, i := range picked {
		r := rows[i]
		r.Pool = pool
		out = append(out, r)
	}
	return out
}

// drawTriagePool takes want[class] indexes of each class after its first
// skip[class].
func drawTriagePool(classes map[string][]int, skip, want map[string]int) []int {
	var picked []int
	for class, list := range classes {
		from := skip[class]
		picked = append(picked, list[from:min(from+want[class], len(list))]...)
	}
	return picked
}
