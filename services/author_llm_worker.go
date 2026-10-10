package services

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/internal/authornorm/llmres"
	"gopds-api/internal/safepath"
	"gopds-api/llm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The synchronous LLM worker of the author layer (design K, section 6). It
// runs in the server process next to the metadata workers. Each round it asks
// the database, participant by participant, whether one more request may go
// out (ClaimAuthorLLMCall decides: pauses, the AIMD window, the endpoint
// ceiling, a due fingerprint check, the token budget), builds the request
// outside any transaction — reading a book excerpt from the archives when the
// job wants one — sends it through the shared OpenAI-compatible client, and
// settles the call. No progress lives in the process: a crash leaves open
// calls, whose leases expire; the next claim closes them as abandoned and the
// jobs are claimed again.

// AuthorLLMCaller is the structured call; *llm.Client satisfies it.
type AuthorLLMCaller interface {
	CompleteStructured(ctx context.Context, req *llm.StructuredRequest) (*llm.StructuredResponse, error)
}

// AuthorLLMWorkerConfig is everything the worker needs besides its database
// and its client.
type AuthorLLMWorkerConfig struct {
	Settings config.AuthorLLMConfig
	// Identity is the configuration the worker calls under; ConfigVersion is
	// its stored version (database.EnsureAuthorLLMConfig).
	Identity      llmreq.Identity
	ConfigVersion string
	// ArchivesDir and Archives read the excerpts.
	ArchivesDir string
	Archives    ArchiveSource
	// PollInterval is the rest of an idle round.
	PollInterval time.Duration
	// LeaseMargin is how much longer than its timeout a call's lease lasts.
	LeaseMargin time.Duration
	// BaseBackoff and MaxBackoff shape the retry delay of a failed job.
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	// Clock overrides the operation clock in tests; nil is the server clock.
	Clock func() *time.Time
}

// Defaults of the worker beyond the configuration section.
const (
	authorLLMPollInterval = 5 * time.Second
	authorLLMLeaseMargin  = time.Minute
	authorLLMBaseBackoff  = 5 * time.Second
	authorLLMMaxBackoff   = 10 * time.Minute
	// authorLLMTierClass is the decision class an attempt's reply is
	// assembled under: the assembly gives the tier, and the class of the
	// result proper is decided when a verdict becomes a result.
	authorLLMTierClass authornorm.DecisionClass = "llm"
)

// authorLLMFoldEvery is how many dispatch rounds pass between two folds of
// the token tally.
const authorLLMFoldEvery = 100

// Classes of a job that is not sent at all.
const (
	authorLLMInputUnreadable  = "input_unreadable"
	authorLLMInputTooLong     = "input_too_long"
	authorLLMRequestOverBound = "request_over_bound"
)

// errAuthorLLMOverBound marks a request larger than the bound its claim
// reserved: sending it could cost more than the reserve.
var errAuthorLLMOverBound = errors.New("services: the request exceeds its reserved bound")

// AuthorLLMWorker is the worker.
type AuthorLLMWorker struct {
	db     *pg.DB
	client AuthorLLMCaller
	cfg    AuthorLLMWorkerConfig
	owner  string
	// handlers counts the call handlers Run has running.
	handlers atomic.Int64
}

// NewAuthorLLMWorker builds a worker with a lease owner of its own.
func NewAuthorLLMWorker(db *pg.DB, client AuthorLLMCaller, cfg *AuthorLLMWorkerConfig) (*AuthorLLMWorker, error) {
	c := *cfg
	if c.ConfigVersion == "" || c.ConfigVersion != c.Identity.Version() {
		return nil, errors.New("services: the LLM worker needs the stored version of its identity")
	}
	if c.Archives == nil {
		c.Archives = ZipArchiveSource{}
	}
	if c.PollInterval <= 0 {
		c.PollInterval = authorLLMPollInterval
	}
	if c.LeaseMargin <= 0 {
		c.LeaseMargin = authorLLMLeaseMargin
	}
	if c.BaseBackoff <= 0 {
		c.BaseBackoff = authorLLMBaseBackoff
	}
	if c.MaxBackoff < c.BaseBackoff {
		c.MaxBackoff = authorLLMMaxBackoff
	}
	return &AuthorLLMWorker{db: db, client: client, cfg: c, owner: database.NewLeaseOwner()}, nil
}

// authorLLMWorkerName is the worker's log label.
const authorLLMWorkerName = "author_llm"

// Name is the worker's log label.
func (w *AuthorLLMWorker) Name() string { return authorLLMWorkerName }

func (w *AuthorLLMWorker) now() *time.Time {
	if w.cfg.Clock == nil {
		return nil
	}
	return w.cfg.Clock()
}

func (w *AuthorLLMWorker) claimOptions(slot llmreq.Slot) *database.AuthorLLMClaimOptions {
	s := w.cfg.Settings
	return &database.AuthorLLMClaimOptions{
		ConfigVersion:    w.cfg.ConfigVersion,
		Slot:             slot,
		Owner:            w.owner,
		Lease:            func(items int) time.Duration { return s.CallTimeout(items) + w.cfg.LeaseMargin },
		ConcurrencyTotal: s.ConcurrencyTotal,
		FingerprintEvery: s.FingerprintEvery,
		MaxAttempts:      s.MaxAttempts,
		Output:           w.cfg.Identity.Output,
		OneOffTokens:     s.Budget.OneOffTokens,
		RunOverrun:       s.Budget.RunOverrun,
		At:               w.now(),
	}
}

// Claim claims one call for a participant.
func (w *AuthorLLMWorker) Claim(ctx context.Context, slot llmreq.Slot) (*database.AuthorLLMClaim, database.AuthorLLMClaimRefusal, error) {
	return database.ClaimAuthorLLMCall(ctx, w.db, w.claimOptions(slot))
}

// RunOnce claims at most one call per participant and processes each to its
// settle, in turn. It returns how many calls it settled.
func (w *AuthorLLMWorker) RunOnce(ctx context.Context) (int, error) {
	settled := 0
	for _, slot := range llmreq.Slots() {
		claim, _, err := w.Claim(ctx, slot)
		if err != nil {
			return settled, err
		}
		if claim == nil {
			continue
		}
		if err := w.Process(ctx, slot, claim); err != nil {
			return settled, err
		}
		settled++
	}
	return settled, nil
}

// Run keeps up to concurrency_total calls of this process in flight until
// ctx is canceled; the database decides how many of them each participant
// gets. A handler holds its semaphore slot from the claim until it returns,
// and its completion is a coalesced, non-blocking wake-up: a handler never
// waits for the dispatch loop, so the loop's exit — and the wait for the
// handlers still running, whose requests the cancellation ends — is bounded.
func (w *AuthorLLMWorker) Run(ctx context.Context) error {
	sem := make(chan struct{}, w.cfg.Settings.ConcurrencyTotal)
	wake := make(chan struct{}, 1)
	var wg sync.WaitGroup
	defer wg.Wait()
	timer := time.NewTimer(0)
	defer timer.Stop()
	foldEvery := 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		claimed := false
		for _, slot := range llmreq.Slots() {
			select {
			case sem <- struct{}{}:
			default:
				continue
			}
			claim, _, err := w.Claim(ctx, slot)
			if err != nil || claim == nil {
				<-sem
				if err != nil && ctx.Err() == nil {
					LogAuthorLLMEvent(AuthorMetadataEventWarn, &AuthorLLMEvent{Name: AuthorLLMEventClaimFailed,
						Slot: slot, SQLState: AuthorMetadataSQLState(err)})
				}
				continue
			}
			claimed = true
			wg.Add(1)
			go w.handle(ctx, slot, claim, &wg, sem, wake)
		}
		foldEvery++
		if foldEvery%authorLLMFoldEvery == 0 {
			_ = database.FoldAuthorLLMTokenTally(ctx, w.db)
		}
		if claimed {
			continue
		}
		timer.Reset(w.cfg.PollInterval)
		select {
		case <-ctx.Done():
			return nil
		case <-wake:
		case <-timer.C:
		}
	}
}

// handle processes one claimed call for Run. It releases its semaphore slot
// and its wait-group entry only when it returns, and wakes the dispatch loop
// without ever waiting for it.
func (w *AuthorLLMWorker) handle(
	ctx context.Context, slot llmreq.Slot, claim *database.AuthorLLMClaim,
	wg *sync.WaitGroup, sem <-chan struct{}, wake chan<- struct{},
) {
	w.handlers.Add(1)
	defer wg.Done()
	defer w.handlers.Add(-1)
	defer func() {
		<-sem
		select {
		case wake <- struct{}{}:
		default:
		}
	}()
	_ = w.Process(ctx, slot, claim)
}

// ActiveHandlers is how many call handlers of Run are running. Run waits for
// all of them before it returns.
func (w *AuthorLLMWorker) ActiveHandlers() int64 { return w.handlers.Load() }

// preparedItem is one job of a call as it is sent.
type preparedItem struct {
	job   *database.AuthorLLMClaimedJob
	input llmres.Input
	item  llmreq.Item
	// skip is the class of a job that cannot be sent at all.
	skip string
}

// Process sends one claimed call and settles it. Errors are the settle's
// own database errors; the call's failures are settled, not returned.
func (w *AuthorLLMWorker) Process(ctx context.Context, slot llmreq.Slot, claim *database.AuthorLLMClaim) error {
	participant, ok := w.cfg.Identity.Participant(slot)
	if !ok {
		return fmt.Errorf("services: no participant in slot %s", slot)
	}
	settlement := &database.AuthorLLMSettlement{
		CallID: claim.CallID, Owner: w.owner,
		MaxAttempts: w.cfg.Settings.MaxAttempts, ConcurrencyMax: w.cfg.Settings.ConcurrencyMax,
		BaseBackoff: w.cfg.BaseBackoff, MaxBackoff: w.cfg.MaxBackoff,
	}
	items, err := w.callItems(ctx, claim)
	if err != nil {
		return err
	}
	var send []llmreq.Item
	for _, it := range items {
		if it.skip == "" {
			send = append(send, it.item)
		}
	}
	if len(send) == 0 {
		// Nothing could be sent: the call settles without a request and
		// spends nothing.
		settlement.Outcome = models.AuthorLLMCallAnswered
		settlement.UsagePresent = true
		settlement.Items = invalidItems(items)
		return w.settle(ctx, slot, claim, settlement)
	}
	resp, err := w.send(ctx, &participant, claim, send, settlement)
	if errors.Is(err, errAuthorLLMOverBound) {
		// The request came out larger than the bound the claim reserved:
		// it is not sent, and spends nothing.
		settlement.Outcome = models.AuthorLLMCallAnswered
		settlement.UsagePresent = true
		for _, it := range items {
			if it.skip == "" {
				it.skip = authorLLMRequestOverBound
			}
		}
		settlement.Items = invalidItems(items)
		return w.settle(ctx, slot, claim, settlement)
	}
	if err != nil {
		w.failedCall(settlement, err)
		return w.settle(ctx, slot, claim, settlement)
	}
	w.answered(claim, resp, settlement)
	if claim.Kind == models.AuthorLLMCallFingerprint {
		passed := fingerprintPassed(claim, resp, settlement.ModelMismatch)
		settlement.CheckPassed = &passed
		settlement.ModelMismatch = false
		return w.settle(ctx, slot, claim, settlement)
	}
	if !settlement.ModelMismatch {
		settlement.Items = w.judgeReplies(items, resp.Content)
	}
	return w.settle(ctx, slot, claim, settlement)
}

// callItems is what a call carries: the claimed jobs, or the fixed item of a
// fingerprint check.
func (w *AuthorLLMWorker) callItems(ctx context.Context, claim *database.AuthorLLMClaim) ([]*preparedItem, error) {
	if claim.Kind != models.AuthorLLMCallFingerprint {
		return w.prepareItems(ctx, claim), nil
	}
	fp := llmreq.FingerprintItem()
	in, err := llmres.NewInput(fp.Input.Fields)
	if err != nil {
		return nil, err
	}
	return []*preparedItem{{item: fp, input: in}}, nil
}

// send builds the request of the items and sends it.
func (w *AuthorLLMWorker) send(
	ctx context.Context, participant *llmreq.Participant, claim *database.AuthorLLMClaim, items []llmreq.Item,
	settlement *database.AuthorLLMSettlement,
) (*llm.StructuredResponse, error) {
	user, err := llmreq.BuildUserMessage(items)
	if err != nil {
		return nil, err
	}
	if llmreq.RequestBytes(user) > claim.PromptBoundBytes {
		return nil, errAuthorLLMOverBound
	}
	req := &llm.StructuredRequest{
		Model: participant.Model, System: llmreq.SystemPrompt, User: string(user),
		Mode: llm.OutputMode(claim.OutputMode), SchemaName: llmreq.SchemaName, Schema: llmreq.BatchSchema(),
		Temperature: participant.Temperature, ReasoningEffort: participant.ReasoningEffort,
		Timeout:         w.cfg.Settings.CallTimeout(len(items)),
		MaxOutputTokens: claim.OutputCeiling, OutputLimitField: w.cfg.Identity.Output.Field,
	}
	reqSHA := sha256.Sum256(append(append([]byte(req.System), 0), user...))
	settlement.RequestSHA256 = reqSHA[:]
	started := time.Now()
	resp, err := w.client.CompleteStructured(ctx, req)
	settlement.Latency = time.Since(started)
	return resp, err
}

// answered records a response's standard fields and its V0 verdict.
func (w *AuthorLLMWorker) answered(claim *database.AuthorLLMClaim, resp *llm.StructuredResponse, s *database.AuthorLLMSettlement) {
	s.Outcome = models.AuthorLLMCallAnswered
	s.ResponseModel = resp.Model
	s.Content = resp.Content
	s.UsagePresent = resp.Usage.Present
	s.Usage = resp.Usage.Raw
	s.PromptTokens = resp.Usage.PromptTokens
	s.CompletionTokens = resp.Usage.CompletionTokens
	s.ReasoningTokens = resp.Usage.ReasoningTokens
	if claim.ExpectedModels != nil && llmres.ValidateModelID(resp.Model, claim.ExpectedModels) != nil {
		s.ModelMismatch = true
	}
}

// fingerprintPassed compares a fingerprint response with the reference: the
// model id V0 accepts, the exact prompt tokens of the fixed request, and for a
// reasoning participant a reasoning (or, when the endpoint reports none,
// completion) token count at least the floor.
func fingerprintPassed(claim *database.AuthorLLMClaim, resp *llm.StructuredResponse, mismatch bool) bool {
	ref := claim.Reference
	if ref == nil || mismatch || !resp.Usage.Present || resp.Usage.PromptTokens != ref.PromptTokens {
		return false
	}
	if ref.ReasoningFloor > 0 {
		reasoning := resp.Usage.CompletionTokens
		if resp.Usage.ReasoningTokens != nil {
			reasoning = *resp.Usage.ReasoningTokens
		}
		return reasoning >= ref.ReasoningFloor
	}
	return true
}

// failedCall maps a client error onto the call's outcome and class.
func (w *AuthorLLMWorker) failedCall(s *database.AuthorLLMSettlement, err error) {
	var ce *llm.CallError
	if !errors.As(err, &ce) {
		s.Outcome = models.AuthorLLMCallTransportError
		s.ErrorClass = string(llm.ErrorTransport)
		return
	}
	s.ErrorClass = string(ce.Class)
	s.HTTPStatus = ce.HTTPStatus
	s.RetryAfter = ce.RetryAfter
	switch ce.Class {
	case llm.ErrorTimeout:
		s.Outcome = models.AuthorLLMCallTimeout
	case llm.ErrorTransport, llm.ErrorCanceled:
		s.Outcome = models.AuthorLLMCallTransportError
	case llm.ErrorMalformed:
		s.Outcome = models.AuthorLLMCallMalformed
	case llm.ErrorAuth, llm.ErrorQuotaExhausted, llm.ErrorRateLimited, llm.ErrorBadRequest, llm.ErrorServer:
		s.Outcome = models.AuthorLLMCallHTTPError
	default:
		s.Outcome = models.AuthorLLMCallHTTPError
	}
}

// judgeReplies splits the reply per item and validates each: V1 (strict
// parsing), then V2-V7 through the assembly, which also gives the tier. A
// reply that is not the batch envelope invalidates every item.
func (w *AuthorLLMWorker) judgeReplies(items []*preparedItem, content []byte) []database.AuthorLLMItemSettlement {
	keys := make([]string, 0, len(items))
	for _, it := range items {
		if it.skip == "" {
			keys = append(keys, it.item.Key)
		}
	}
	replies, splitErr := llmreq.SplitReply(content, keys)
	out := make([]database.AuthorLLMItemSettlement, 0, len(items))
	for _, it := range items {
		s := database.AuthorLLMItemSettlement{AttemptID: it.job.AttemptID, Outcome: models.AuthorLLMAttemptInvalid}
		switch {
		case it.skip != "":
			s.ValidatorClass = it.skip
		case splitErr != nil:
			s.ValidatorClass = "malformed_batch"
		default:
			w.judgeReply(it, replies[it.item.Key], &s)
		}
		out = append(out, s)
	}
	return out
}

func (w *AuthorLLMWorker) judgeReply(it *preparedItem, r llmreq.ItemReply, s *database.AuthorLLMItemSettlement) {
	if r.Err != nil {
		var ie *llmreq.ItemError
		if errors.As(r.Err, &ie) {
			s.ValidatorClass = "item_" + string(ie.Class)
		} else {
			s.ValidatorClass = "item_invalid"
		}
		return
	}
	rep := r.Reply
	canonical := rep.CanonicalBytes()
	sum := sha256.Sum256(canonical)
	if rep.Refused() {
		s.Outcome, s.Refused, s.Reply, s.CanonicalSHA256 = models.AuthorLLMAttemptAnswered, true, canonical, sum[:]
		return
	}
	extractor := "eval"
	if it.job.ExtractorVersion != nil {
		extractor = *it.job.ExtractorVersion
	}
	outcome, err := llmres.Assemble(&it.input, rep, extractor, w.cfg.ConfigVersion, authorLLMTierClass)
	if err != nil {
		s.ValidatorClass = violationClass(err)
		return
	}
	s.Outcome, s.Reply, s.CanonicalSHA256, s.Tier = models.AuthorLLMAttemptAnswered, canonical, sum[:], string(outcome.Tier)
}

// violationClass is the closed class of a validator failure: v2..v7.
func violationClass(err error) string {
	var v *llmres.Violation
	if errors.As(err, &v) {
		return strings.ToLower(v.Validator) + "_violation"
	}
	return "assembly_failed"
}

// invalidItems settles every item as invalid with its skip class.
func invalidItems(items []*preparedItem) []database.AuthorLLMItemSettlement {
	out := make([]database.AuthorLLMItemSettlement, 0, len(items))
	for _, it := range items {
		class := it.skip
		if class == "" {
			class = "not_sent"
		}
		out = append(out, database.AuthorLLMItemSettlement{AttemptID: it.job.AttemptID,
			Outcome: models.AuthorLLMAttemptInvalid, ValidatorClass: class})
	}
	return out
}

// prepareItems turns the claimed jobs into request items: the stored input,
// and the excerpt when the job wants one.
func (w *AuthorLLMWorker) prepareItems(ctx context.Context, claim *database.AuthorLLMClaim) []*preparedItem {
	items := make([]*preparedItem, 0, len(claim.Jobs))
	for i := range claim.Jobs {
		job := &claim.Jobs[i]
		it := &preparedItem{job: job, item: llmreq.Item{Key: job.ItemKey}}
		items = append(items, it)
		var input llmreq.ItemInput
		if err := json.Unmarshal(job.Input, &input); err != nil {
			it.skip = authorLLMInputUnreadable
			continue
		}
		parsed, err := llmres.NewInput(input.Fields)
		if err != nil {
			it.skip = authorLLMInputUnreadable
			continue
		}
		if llmres.CheckLimits(parsed.Tokens) != nil {
			it.skip = authorLLMInputTooLong
			continue
		}
		it.input, it.item.Input = parsed, input
		if job.WithContext {
			it.item.Context = w.excerpt(ctx, claim, job, parsed.Tokens)
		}
	}
	return items
}

// excerpt returns the job's excerpt, reading and storing it the first time.
// The decision is recorded on the job: attached, irrelevant (the excerpt does
// not mention the name — it is about someone else) or unavailable (no
// readable book). Only an attached excerpt is sent.
func (w *AuthorLLMWorker) excerpt(
	ctx context.Context, claim *database.AuthorLLMClaim, job *database.AuthorLLMClaimedJob, tokens []llmres.Token,
) *llmres.BookContext {
	switch job.ContextState {
	case models.AuthorLLMContextIrrelevant, models.AuthorLLMContextUnavailable:
		return nil
	case models.AuthorLLMContextAttached:
		return w.storedExcerpt(ctx, job.ContextSHA256)
	case models.AuthorLLMContextNone:
	}
	for _, book := range w.excerptBooks(ctx, job) {
		bc, ok := w.readExcerpt(ctx, &book)
		if !ok {
			continue
		}
		bookID := book.BookID
		if !llmres.ContextRelevant(&bc, tokens) {
			w.recordContext(ctx, claim, job, models.AuthorLLMContextIrrelevant, &bookID, nil)
			return nil
		}
		sum := bc.SHA256()
		exclude := sha256.Sum256([]byte(book.CreditDisplay))
		stored, err := database.StoreAuthorLLMContext(ctx, w.db, &database.AuthorLLMContextKey{
			BookID: book.BookID, BookMD5: book.BookMD5, Version: llmres.ContextVersion, ExcludeSHA256: exclude[:],
		}, bc.Payload(), sum[:])
		if err != nil {
			continue
		}
		if !slices.Equal(stored, sum[:]) {
			// Another writer stored a different excerpt for the same file
			// version and the same excluded credit: the reader is not
			// deterministic, so neither is sent.
			LogAuthorLLMEvent(AuthorMetadataEventWarn, &AuthorLLMEvent{Name: AuthorLLMEventContextFailed,
				CallID: claim.CallID, Class: "excerpt_unreadable"})
			continue
		}
		if w.recordContext(ctx, claim, job, models.AuthorLLMContextAttached, &bookID, stored) {
			return &bc
		}
		return nil
	}
	w.recordContext(ctx, claim, job, models.AuthorLLMContextUnavailable, nil, nil)
	return nil
}

// storedExcerpt reads an attached excerpt back: every retry and both
// participants see the same bytes.
func (w *AuthorLLMWorker) storedExcerpt(ctx context.Context, sha []byte) *llmres.BookContext {
	payload, err := database.LoadAuthorLLMContext(ctx, w.db, sha)
	if err != nil || payload == nil {
		return nil
	}
	var bc llmres.BookContext
	if json.Unmarshal(payload, &bc) != nil {
		return nil
	}
	return &bc
}

// excerptBooks lists the books an excerpt may be read from: an eval item's
// frozen book, or the fingerprint's books lowest id first.
func (w *AuthorLLMWorker) excerptBooks(ctx context.Context, job *database.AuthorLLMClaimedJob) []database.AuthorLLMContextBook {
	if job.EvalItemID != nil {
		if job.EvalBookID == nil || job.EvalBookMD5 == nil {
			return nil
		}
		book, err := database.AuthorLLMContextBookByID(ctx, w.db, *job.EvalBookID, *job.EvalBookMD5, job.EvalFingerprint)
		if err != nil || book == nil {
			return nil
		}
		return []database.AuthorLLMContextBook{*book}
	}
	if job.ExtractorVersion == nil {
		return nil
	}
	books, err := database.AuthorLLMContextBooks(ctx, w.db, job.SourceFingerprint, *job.ExtractorVersion)
	if err != nil {
		return nil
	}
	return books
}

func (w *AuthorLLMWorker) recordContext(ctx context.Context, claim *database.AuthorLLMClaim,
	job *database.AuthorLLMClaimedJob, state models.AuthorLLMContextState, bookID *int64, sha []byte) bool {
	if err := database.SetAuthorLLMJobContext(ctx, w.db, job.JobID, claim.CallID, state, bookID, sha); err != nil {
		LogAuthorLLMEvent(AuthorMetadataEventWarn, &AuthorLLMEvent{Name: AuthorLLMEventContextFailed,
			CallID: claim.CallID, Class: string(state), SQLState: AuthorMetadataSQLState(err)})
		return false
	}
	job.ContextState = state
	return true
}

// authorLLMMaxEntryBytes caps how much of one FB2 entry an excerpt reads.
const authorLLMMaxEntryBytes = 64 << 20

// readExcerpt reads one book's excerpt from its archive.
func (w *AuthorLLMWorker) readExcerpt(ctx context.Context, book *database.AuthorLLMContextBook) (bc llmres.BookContext, ok bool) {
	full, err := safepath.Resolve(w.cfg.ArchivesDir, book.ArchivePath)
	if err != nil {
		return llmres.BookContext{}, false
	}
	archive, err := w.cfg.Archives.Open(ctx, full)
	if err != nil {
		return llmres.BookContext{}, false
	}
	defer archive.Close()
	entry, err := archive.OpenEntry(book.EntryName)
	if err != nil {
		return llmres.BookContext{}, false
	}
	defer entry.Close()
	raw, err := io.ReadAll(io.LimitReader(entry, authorLLMMaxEntryBytes))
	if err != nil {
		return llmres.BookContext{}, false
	}
	bc, err = llmres.BuildContext(raw, book.CreditDisplay)
	if err != nil {
		return llmres.BookContext{}, false
	}
	return bc, true
}

// settle settles the call and logs the outcome by identifiers and classes.
func (w *AuthorLLMWorker) settle(
	ctx context.Context, slot llmreq.Slot, claim *database.AuthorLLMClaim, s *database.AuthorLLMSettlement,
) error {
	s.At = w.now()
	// The settle runs even when the worker is being stopped: the response is
	// paid for, and an unsettled call would only be abandoned later.
	report, err := database.SettleAuthorLLMCall(context.WithoutCancel(ctx), w.db, s)
	event := &AuthorLLMEvent{CallID: claim.CallID, Slot: slot, Kind: string(claim.Kind), Outcome: string(s.Outcome),
		Class: s.ErrorClass, HTTPStatus: s.HTTPStatus, Items: len(claim.Jobs),
		Tokens: s.PromptTokens + s.CompletionTokens, LatencyMS: s.Latency.Milliseconds(), RunID: claim.RunID}
	if s.ModelMismatch {
		event.Class = database.AuthorLLMClassModelMismatch
	}
	switch {
	case errors.Is(err, database.ErrAuthorLLMLeaseLost):
		event.Name = AuthorLLMEventCallLeaseLost
		LogAuthorLLMEvent(AuthorMetadataEventWarn, event)
		return nil
	case err != nil:
		event.Name, event.SQLState = AuthorLLMEventCallFailed, AuthorMetadataSQLState(err)
		LogAuthorLLMEvent(AuthorMetadataEventError, event)
		return err
	}
	event.Name, event.Answered, event.Invalid = AuthorLLMEventCallSettled, report.Answered, report.Invalid
	LogAuthorLLMEvent(AuthorMetadataEventInfo, event)
	if s.CheckPassed != nil {
		name := AuthorLLMEventCheckPassed
		if !*s.CheckPassed {
			name = AuthorLLMEventCheckFailed
		}
		LogAuthorLLMEvent(AuthorMetadataEventInfo, &AuthorLLMEvent{Name: name, CallID: claim.CallID, Slot: slot,
			Tokens: s.PromptTokens})
	}
	if report.Paused != "" {
		LogAuthorLLMEvent(AuthorMetadataEventWarn, &AuthorLLMEvent{Name: AuthorLLMEventParticipantOff,
			CallID: claim.CallID, Slot: slot, Class: report.Paused})
	}
	return nil
}

// NewAuthorLLMWorkerFromConfig builds the worker from the configuration: the
// shared llm section (endpoint, key) and author_metadata.llm (participants,
// limits). It stores the configuration identity. A layer that is switched off
// returns no worker and the reason, never an error: the review queue works
// without the LLM.
func NewAuthorLLMWorkerFromConfig(
	ctx context.Context, db *pg.DB, archivesDir string, c *config.AuthorMetadataConfig, shared config.LLMConfig,
) (*AuthorLLMWorker, string, error) {
	status := c.LLM.Status(shared)
	if !status.Enabled {
		return nil, status.Reason, nil
	}
	identity, err := llmreq.NewIdentity(shared.BaseURL, authorLLMParticipants(&c.LLM), llmreq.OutputLimits{
		Field: c.LLM.OutputLimitField, PerCall: c.LLM.MaxOutput.PerCallTokens, PerItem: c.LLM.MaxOutput.PerItemTokens,
	})
	if err != nil {
		return nil, config.AuthorLLMDisabledModels, nil
	}
	version, err := database.EnsureAuthorLLMConfig(ctx, db, &identity, c.LLM.ConcurrencyStart)
	if err != nil {
		return nil, "", err
	}
	w, err := NewAuthorLLMWorker(db, llm.NewClient(shared), &AuthorLLMWorkerConfig{
		Settings: c.LLM, Identity: identity, ConfigVersion: version,
		ArchivesDir: archivesDir, PollInterval: c.PollInterval,
	})
	if err != nil {
		return nil, "", err
	}
	return w, "", nil
}

// authorLLMParticipants maps the section's fixed fields onto the four slots.
func authorLLMParticipants(c *config.AuthorLLMConfig) []llmreq.Participant {
	one := func(slot llmreq.Slot, model string, p config.AuthorLLMParams) llmreq.Participant {
		return llmreq.Participant{Slot: slot, Model: model, Temperature: p.Temperature, ReasoningEffort: p.ReasoningEffort}
	}
	return []llmreq.Participant{
		one(llmreq.SlotProductionA, c.Models.ProductionA, c.Params.ProductionA),
		one(llmreq.SlotProductionB, c.Models.ProductionB, c.Params.ProductionB),
		one(llmreq.SlotJudgeA, c.Models.JudgeA, c.Params.JudgeA),
		one(llmreq.SlotJudgeB, c.Models.JudgeB, c.Params.JudgeB),
	}
}
