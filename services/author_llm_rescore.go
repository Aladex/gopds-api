package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/internal/authornorm/llmres"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The re-score preview: a completed eval run's report computed twice from
// the replies the run stored — once from the stored answers, once with every
// sent reply judged again by this build's validators — so a validator change
// shows which strata would pass before any token is spent. It writes
// nothing and is never evidence: a registration cites a report of the
// configuration in force, and the configuration identity carries the prompt
// hash and the validator version. A run answered under another prompt (every
// run before the change that relaxed the validators, since the prompt is one
// text for every stratum) measured a question the new configuration no
// longer asks, so no stratum of it is registrable.

// AuthorLLMRescoreNotRegistrable says why a preview never backs a
// registration.
const AuthorLLMRescoreNotRegistrable = "preview only: the stored replies answer the run's own prompt and " +
	"validators; the configuration identity (prompt hash, validator version) differs, a recomputation " +
	"is only stored for the run's configuration, and the prompt is shared by every stratum, so no " +
	"stratum's question is unchanged"

// AuthorLLMRescorePreview is the preview of one eval run.
type AuthorLLMRescorePreview struct {
	RunID         int64  `json:"run_id"`
	ConfigVersion string `json:"config_version"`
	// ValidatorVersion is this build's, the one the replies were judged by.
	ValidatorVersion string               `json:"validator_version"`
	Registrable      bool                 `json:"registrable"`
	NotRegistrable   string               `json:"not_registrable"`
	Jobs             AuthorLLMRescoreJobs `json:"jobs"`
	Before           llmeval.Report       `json:"before"`
	After            llmeval.Report       `json:"after"`
}

// AuthorLLMRescoreJobs counts the jobs judged again and how their answers
// moved.
type AuthorLLMRescoreJobs struct {
	Rejudged         int `json:"rejudged"`
	BecameValid      int `json:"became_valid"`
	BecameInvalid    int `json:"became_invalid"`
	CanonicalChanged int `json:"canonical_changed"`
}

// rejudgedAnswer is a job's answer under this build's validators.
type rejudgedAnswer struct {
	answered  bool
	canonical []byte
	refused   bool
	tier      string
	class     string
}

// apply replaces a stored answer with the rejudged one.
func (r *rejudgedAnswer) apply(a *database.AuthorLLMEvalAnswer) {
	if r.answered {
		a.Status, a.CanonicalSHA256, a.LastErrorClass = string(models.AuthorLLMJobAnswered), r.canonical, nil
		a.Refused, a.Tier = &r.refused, nil
		if r.tier != "" {
			a.Tier = &r.tier
		}
		return
	}
	class := r.class
	a.Status, a.CanonicalSHA256, a.Refused, a.Tier = string(models.AuthorLLMJobInvalid), nil, nil, nil
	a.LastErrorClass = &class
}

// PreviewAuthorLLMEvalRescore computes the preview of a completed eval run
// (ErrAuthorLLMInvalidRun for another kind, database.ErrAuthorLLMRunState
// before it completed). It only reads.
func PreviewAuthorLLMEvalRescore(ctx context.Context, db *pg.DB, runID int64) (*AuthorLLMRescorePreview, error) {
	row, err := database.GetAuthorLLMRunByID(ctx, db, runID)
	if err != nil {
		return nil, err
	}
	switch {
	case row.Kind != models.AuthorLLMRunEval:
		return nil, ErrAuthorLLMInvalidRun
	case row.Status != models.AuthorLLMRunCompleted:
		return nil, database.ErrAuthorLLMRunState
	}
	prev, err := database.GetAuthorLLMEvalReport(ctx, db, runID)
	if err != nil {
		return nil, err
	}
	_, run, fingerprint, err := storedEvalReport(row, prev)
	if err != nil {
		return nil, err
	}
	d := &AuthorLLMEvalDriver{db: db, version: row.ConfigVersion}
	before, err := d.collect(ctx, run)
	if err != nil {
		return nil, err
	}
	before.input.Fingerprint = fingerprint

	stored := map[int64]database.AuthorLLMEvalAnswer{}
	answers, err := database.AuthorLLMEvalAnswers(ctx, db, runID)
	if err != nil {
		return nil, err
	}
	for i := range answers {
		stored[answers[i].JobID] = answers[i]
	}
	if d.rejudged, err = rejudgeEvalRun(ctx, db, runID, row.ConfigVersion); err != nil {
		return nil, err
	}
	after, err := d.collect(ctx, run)
	if err != nil {
		return nil, err
	}
	after.input.Fingerprint = fingerprint

	p := &AuthorLLMRescorePreview{RunID: runID, ConfigVersion: row.ConfigVersion,
		ValidatorVersion: llmreq.ValidatorVersion, NotRegistrable: AuthorLLMRescoreNotRegistrable,
		Before: evaluateEval(&before.input), After: evaluateEval(&after.input)}
	p.Jobs = countRejudged(stored, d.rejudged)
	return p, nil
}

// countRejudged compares each rejudged job with its stored answer.
func countRejudged(stored map[int64]database.AuthorLLMEvalAnswer, rejudged map[int64]rejudgedAnswer) AuthorLLMRescoreJobs {
	var c AuthorLLMRescoreJobs
	for id, r := range rejudged {
		c.Rejudged++
		was := stored[id]
		wasValid := was.Status == string(models.AuthorLLMJobAnswered)
		switch {
		case r.answered && !wasValid:
			c.BecameValid++
		case !r.answered && wasValid:
			c.BecameInvalid++
		case r.answered && !bytes.Equal(r.canonical, was.CanonicalSHA256):
			c.CanonicalChanged++
		}
	}
	return c
}

// rejudgeEvalRun judges every sent attempt of the run again, splitting each
// call's reply once with the keys of the attempts it carried, and gives each
// job the answer its first valid attempt now gives — the attempt production
// would have stopped at — or, when none is valid, the class its last attempt
// breaks.
func rejudgeEvalRun(ctx context.Context, db *pg.DB, runID int64, version string) (map[int64]rejudgedAnswer, error) {
	attempts, err := database.AuthorLLMEvalSentAttempts(ctx, db, runID)
	if err != nil {
		return nil, err
	}
	keys := map[int64][]string{}
	for i := range attempts {
		keys[attempts[i].CallID] = append(keys[attempts[i].CallID], attempts[i].ItemKey)
	}
	ids := make([]int64, 0, len(keys))
	for id := range keys {
		ids = append(ids, id)
	}
	contents, err := database.AuthorLLMCallContent(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	type split struct {
		replies map[string]llmreq.ItemReply
		err     error
	}
	splits := make(map[int64]split, len(keys))
	for id, k := range keys {
		replies, splitErr := llmreq.SplitReply(contents[id], k)
		splits[id] = split{replies, splitErr}
	}

	out := map[int64]rejudgedAnswer{}
	for i := range attempts {
		a := &attempts[i]
		if prev, done := out[a.JobID]; done && prev.answered {
			continue
		}
		sp := splits[a.CallID]
		var s database.AuthorLLMItemSettlement
		in, skip := rescoreInput(a.Input)
		switch {
		case skip != "":
			s.ValidatorClass = skip
		case sp.err != nil:
			s.ValidatorClass = authorLLMMalformedBatch
		default:
			judgeItemReply(&in, sp.replies[a.ItemKey], a.ExtractorVersion, version, &s)
		}
		r := rejudgedAnswer{class: s.ValidatorClass}
		if s.Outcome == models.AuthorLLMAttemptAnswered {
			r = rejudgedAnswer{answered: true, canonical: s.CanonicalSHA256, refused: s.Refused, tier: s.Tier}
		}
		out[a.JobID] = r
	}
	return out, nil
}

// rescoreInput parses a job's stored input as the worker does, or names why
// the worker would not have sent it.
func rescoreInput(raw json.RawMessage) (in llmres.Input, skip string) {
	var input llmreq.ItemInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return llmres.Input{}, authorLLMInputUnreadable
	}
	parsed, err := llmres.NewInput(input.Fields)
	if err != nil {
		return llmres.Input{}, authorLLMInputUnreadable
	}
	if llmres.CheckLimits(parsed.Tokens) != nil {
		return llmres.Input{}, authorLLMInputTooLong
	}
	return parsed, ""
}

// String renders a short summary for logs: counts only, no name.
func (p *AuthorLLMRescorePreview) String() string {
	return fmt.Sprintf("run %d: %d jobs rejudged, %d became valid, %d became invalid, %d changed canonical",
		p.RunID, p.Jobs.Rejudged, p.Jobs.BecameValid, p.Jobs.BecameInvalid, p.Jobs.CanonicalChanged)
}
