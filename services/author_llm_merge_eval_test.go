package services_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The merge eval (E4c-2a) brought up on a fixture catalog and a stub
// provider: the entity build stores its candidates, a merge0 run freezes
// them with the namesake suite and the known-same anchors, the merge
// worker answers every stage, and the report breaks the gate down per kind
// and per script. K's configuration is never touched, and no name or title
// reaches the logs.

// mergeEvalSizes are merge0's sizes shrunk to the fixture.
var mergeEvalSizes = services.AuthorLLMMergeEvalSizes{PerKind: 10, SuitePerShape: 3, SuiteLatinPerShape: 1,
	AnchorsPerShape: 5, Repeat: 3, FingerprintRepeats: 3}

// mergePerson is a fixture person: name parts, script and genre code.
type mergePerson struct {
	family, given, additional, script, genre string
	books                                    int
	series                                   int64
}

// mergeFixture is the catalog: swap and initials pairs of one person each
// (one genre), namesakes of two people (two genres), and a typo pair the
// build merges (a shared series), in both scripts.
var mergeFixture = []mergePerson{
	{"Кинг", "Стивен", "", "Cyrl", "sf_horror", 2, 0}, {"Стивен", "Кинг", "", "Cyrl", "sf_horror", 1, 0},
	{"Азимов", "Айзек", "", "Cyrl", "sf", 2, 0}, {"Айзек", "Азимов", "", "Cyrl", "sf", 1, 0},
	{"Брэдбери", "Рэй", "", "Cyrl", "sf", 1, 0}, {"Рэй", "Брэдбери", "", "Cyrl", "sf", 1, 0},
	{"King", "Stephen", "", "Latn", "sf_horror", 2, 0}, {"Stephen", "King", "", "Latn", "sf_horror", 1, 0},
	{"Лукьяненко", "С.", "", "Cyrl", "sf_fantasy", 1, 0}, {"Лукьяненко", "Сергей", "", "Cyrl", "sf_fantasy", 2, 0},
	{"Пелевин", "В.", "", "Cyrl", "prose_contemporary", 1, 0}, {"Пелевин", "Виктор", "", "Cyrl", "prose_contemporary", 2, 0},
	{"Tolkien", "J.", "", "Latn", "sf_fantasy", 1, 0}, {"Tolkien", "John", "", "Latn", "sf_fantasy", 2, 0},
	{"Иванов", "Алексей", "Викторович", "Cyrl", "prose_history", 2, 0},
	{"Иванов", "Алексей", "Александрович", "Cyrl", "sci_psychology", 2, 0},
	{"Петров", "Сергей", "Иванович", "Cyrl", "det_classic", 1, 0},
	{"Петров", "Сергей", "Николаевич", "Cyrl", "sci_math", 1, 0},
	{"Смирнова", "Анна", "Петровна", "Cyrl", "love_contemporary", 1, 0},
	{"Смирнова", "Анна", "Сергеевна", "Cyrl", "sci_biology", 1, 0},
	{"Кузнецов", "Дмитрий", "Олегович", "Cyrl", "adv_history", 1, 0},
	{"Кузнецов", "Дмитрий", "Андреевич", "Cyrl", "comp_programming", 1, 0},
	{"Smith", "John", "Michael", "Latn", "det_police", 1, 0},
	{"Smith", "John", "Robert", "Latn", "sci_chem", 1, 0},
	{"Достоевский", "Фёдор", "Михайлович", "Cyrl", "prose_classic", 1, 77},
	{"Достоевский", "Фёдор", "Михайлвич", "Cyrl", "prose_classic", 1, 77},
}

// creditIn is entityCatalog.credit in a script.
func (c *entityCatalog) creditIn(snap int64, fill byte, script, family, given string, additional ...string) {
	c.t.Helper()
	fp := bytes.Repeat([]byte{fill}, 32)
	display := strings.Join(append(append([]string{given}, additional...), family), " ")
	var credit, result int64
	_, err := c.db.QueryOne(pg.Scan(&credit), `INSERT INTO book_contributor_credit
		(snapshot_id, role, position, source_first_name, source_last_name, source_display_name, source_fingerprint)
		VALUES (?, 'author', 0, ?, ?, ?, ?) RETURNING id`, snap, given, family, display, fp)
	require.NoError(c.t, err)
	_, err = c.db.Query(pg.Scan(&result), `SELECT id FROM contributor_normalization_result WHERE source_fingerprint = ?`, fp)
	require.NoError(c.t, err)
	if result == 0 {
		key := bytes.Repeat([]byte{fill ^ 0xff}, 32)
		_, err = c.db.QueryOne(pg.Scan(&result), `INSERT INTO contributor_normalization_result
			(source_fingerprint, normalization_key, extractor_version, normalizer_version, result_schema_version,
			 method, kind, status, decision_class, family_name, given_name, additional_names,
			 display_name, sort_name, search_key, script)
			VALUES (?, ?, 'extractor-v1', 'normalizer-v1', 'result-v1', 'rules', 'person', 'normalized',
			 'structured_person', ?, ?, coalesce(?::text[], '{}'), ?, ?, ?, ?) RETURNING id`,
			fp, key, family, given, pg.Array(additional), display, display, authornorm.SearchKey(display), script)
		require.NoError(c.t, err)
	}
	c.exec(`INSERT INTO book_contributor_credit_selection
		(credit_id, source_fingerprint, state, result_id, basis, policy_version)
		VALUES (?, ?, 'selected', ?, 'automatic', '4')`, credit, fp, result)
}

// seedMergeFixture writes the fixture's books and credits; it returns
// every name and title the logs must never show.
func (c *entityCatalog) seedMergeFixture() []string {
	c.t.Helper()
	c.exec(`INSERT INTO author_acceptance_class
		(policy_version, decision_class, script, config_version, evidence_report_sha256, evidence_ref, source)
		VALUES ('4', 'structured_person', 'Latn', 'normalizer-v1', ?, 'internal/authornorm/policy_evidence/fixture.json',
		 'shipped')`, bytes.Repeat([]byte{0xee}, 32))
	genres := map[string]int64{}
	var secrets []string
	for i, p := range mergeFixture {
		gid, ok := genres[p.genre]
		if !ok {
			gid = int64(900 + len(genres))
			genres[p.genre] = gid
			c.exec(`INSERT INTO opds_catalog_genre (id, genre, title) VALUES (?, ?, '')`, gid, p.genre)
		}
		secrets = append(secrets, p.family, p.given)
		for b := range p.books {
			title := fmt.Sprintf("Повесть %d-%d", i, b)
			var snap int64
			if p.series > 0 {
				snap = c.book(title, p.series)
			} else {
				snap = c.book(title)
			}
			c.exec(`INSERT INTO opds_catalog_bgenre (genre_id, book_id) VALUES (?, ?)`, gid, c.bookOf(snap))
			var extra []string
			if p.additional != "" {
				extra = []string{p.additional}
			}
			c.creditIn(snap, byte(0x20+i), p.script, p.family, p.given, extra...)
			secrets = append(secrets, title)
		}
	}
	return secrets
}

// stubWords is a name's words, lowercased, without dots.
func stubWords(name string) []string {
	return strings.Fields(strings.ToLower(strings.ReplaceAll(name, ".", " ")))
}

// stubSameName is how a careful reader matches two names by their words:
// the same words (a typo in a long word aside) in any order, or initials
// standing for the other's words with the same family name.
func stubSameName(a, b string) bool {
	wa, wb := stubWords(a), stubWords(b)
	if len(wa) == 0 || len(wb) == 0 {
		return false
	}
	prefix := func(w string) string {
		r := []rune(w)
		return string(r[:min(4, len(r))])
	}
	if len(wa) == len(wb) {
		pa, pb := make([]string, len(wa)), make([]string, len(wb))
		for i := range wa {
			pa[i], pb[i] = prefix(wa[i]), prefix(wb[i])
		}
		slices.Sort(pa)
		slices.Sort(pb)
		if slices.Equal(pa, pb) {
			return true
		}
	}
	short, full := wa, wb
	if len(short) > len(full) {
		short, full = full, short
	}
	if short[len(short)-1] != full[len(full)-1] {
		return false
	}
	for i, w := range short[:len(short)-1] {
		r, _ := utf8.DecodeRuneInString(w)
		f, _ := utf8.DecodeRuneInString(full[i])
		if r != f {
			return false
		}
	}
	return true
}

// stubSharedGenre reports two cards with a genre in common.
func stubSharedGenre(a, b *authorentity.MergeCard) bool {
	for i := range a.Books {
		for j := range b.Books {
			for _, g := range a.Books[i].Genres {
				if slices.Contains(b.Books[j].Genres, g) {
					return true
				}
			}
		}
	}
	return false
}

// mergeStub is the stub provider: the production pair answers "same" on
// the names alone (the failure the suite is there to catch), the judges
// only where the books fit one author too; the fixed request gets each
// model's own prompt tokens, as three tokenizers would.
func mergeStub(req *fakeRequest) fakeReply {
	var msg struct {
		Cards map[string]authorentity.MergeCard `json:"cards"`
		Items []struct {
			ID string `json:"id"`
			A  string `json:"a"`
			B  string `json:"b"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(req.User), &msg) != nil || msg.Cards == nil {
		return fakeReply{}
	}
	judge := req.Model == "gpt-6-sol" || req.Model == "claude-opus-5"
	results := make([]any, 0, len(msg.Items))
	fingerprint := false
	for _, it := range msg.Items {
		a, b := msg.Cards[it.A], msg.Cards[it.B]
		fingerprint = fingerprint || (len(a.Books) == 1 && a.Books[0].Title == "Ночной дозор")
		answer := llmreq.MergeDifferent
		if stubSameName(a.Names[0], b.Names[0]) && (!judge || stubSharedGenre(&a, &b)) {
			answer = llmreq.MergeSame
		}
		results = append(results, map[string]any{"id": it.ID, "answer": answer})
	}
	out, _ := json.Marshal(map[string]any{"results": results})
	reply := fakeReply{Content: string(out), Prompt: 1200}
	if fingerprint {
		reply.Prompt = distinctLabs[req.Model]
	}
	return reply
}

// mergeEvalReport is the part of a stored merge eval report the test reads.
type mergeEvalReport struct {
	Mode   string `json:"mode"`
	Task   string `json:"task"`
	Gate   string `json:"gate"`
	Recall []struct {
		Kind, Script string
		Items        int `json:"items"`
		Accepted     int `json:"accepted"`
	} `json:"recall"`
	Tokens []struct {
		ItemSet       string  `json:"item_set"`
		Stratum       string  `json:"stratum"`
		Items         int     `json:"items"`
		TokensPerItem float64 `json:"tokens_per_item"`
	} `json:"tokens"`
	Evaluation struct {
		Fingerprint struct {
			OK bool `json:"ok"`
		} `json:"fingerprint"`
		Strata map[string]struct {
			Holdout int `json:"holdout"`
			Combos  map[string]struct {
				Accepted    int `json:"accepted"`
				Errors      int `json:"errors"`
				Adversarial *struct {
					Items        int `json:"items"`
					PairAccepted int `json:"pair_accepted"`
					Accepted     int `json:"accepted"`
				} `json:"adversarial"`
				Scripts map[string]struct {
					Items        int `json:"items"`
					PairAccepted int `json:"pair_accepted"`
					Accepted     int `json:"accepted"`
					Adversarial  *struct {
						Items int `json:"items"`
					} `json:"adversarial"`
				} `json:"scripts"`
			} `json:"combos"`
		} `json:"strata"`
		Decision struct {
			Registrable bool `json:"registrable"`
			Strata      []struct {
				Stratum string `json:"stratum"`
				Passed  bool   `json:"passed"`
				Reason  string `json:"reason"`
			} `json:"strata"`
		} `json:"decision"`
	} `json:"evaluation"`
}

// A merge0 run on the fixture goes freeze → fingerprint → anchor →
// candidates → judge → repeat → report through the merge driver and the
// merge worker; K's driver steps nothing of it and K asks nothing.
func TestTheMergePilotRunsEndToEnd(t *testing.T) {
	e := newLLMEnv(t)
	c := &entityCatalog{t: t, db: e.db, next: 1000}
	c.exec(`INSERT INTO author_acceptance_class
		(policy_version, decision_class, script, config_version, evidence_report_sha256, evidence_ref, source)
		VALUES ('4', 'structured_person', 'Cyrl', 'normalizer-v1', ?, 'internal/authornorm/policy_evidence/fixture.json',
		 'shipped')`, bytes.Repeat([]byte{0xee}, 32))
	secrets := c.seedMergeFixture()
	rep := c.build()
	require.Equal(t, 4, rep.Candidates.ByKind["swap"]["Cyrl"].Candidates+rep.Candidates.ByKind["swap"]["Latn"].Candidates)
	require.Equal(t, 3, rep.Candidates.ByKind["initials"]["Cyrl"].Candidates+rep.Candidates.ByKind["initials"]["Latn"].Candidates)
	logs := captureLogs(t)

	m := e.mergeConfig()
	ctx := context.Background()
	run, err := services.StartAuthorLLMMergeEval(ctx, e.db, m.version, models.AuthorLLMEvalModeMerge0, nil)
	require.NoError(t, err)
	_, err = services.StartAuthorLLMMergeEval(ctx, e.db, m.version, models.AuthorLLMEvalModeMerge1, nil)
	require.ErrorIs(t, err, services.ErrAuthorLLMInvalidRun, "merge1 is E4c-2b")

	e.fake.setRespond(mergeStub)
	driver := e.mergeWorker(m).EvalDriver().WithMergeSizes(mergeEvalSizes)
	kDriver := e.worker().EvalDriver()
	w, kw := e.mergeWorker(m), e.worker()
	var stages []string
	for range 300 {
		stage, status := e.runStage(run)
		if status == string(models.AuthorLLMRunCompleted) {
			break
		}
		require.Equal(t, string(models.AuthorLLMRunRunning), status, "stopped in %s", stage)
		if len(stages) == 0 || stages[len(stages)-1] != stage {
			stages = append(stages, stage)
		}
		_, err = kDriver.Step(ctx)
		require.NoError(t, err)
		_, err = driver.Step(ctx)
		require.NoError(t, err)
		_, err = w.RunOnce(ctx)
		require.NoError(t, err)
		_, err = kw.RunOnce(ctx)
		require.NoError(t, err)
		time.Sleep(5 * time.Millisecond)
	}
	_, status := e.runStage(run)
	require.Equal(t, string(models.AuthorLLMRunCompleted), status)
	assert.Equal(t, []string{"scoped_freeze", "scoped_fingerprint", "scoped_anchor", "scoped_candidates",
		"scoped_judge", "scoped_repeat", "scoped_report"}, stages)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE config_version = ?`, e.version),
		"K was asked something")
	assert.Equal(t, 2, e.count(`SELECT count(*) FROM author_llm_config`))

	raw, _, err := services.AuthorLLMEvalReportFile(ctx, e.db, run)
	require.NoError(t, err)
	t.Logf("merge0 report: %s", raw)
	var r mergeEvalReport
	require.NoError(t, json.Unmarshal(raw, &r))
	assert.Equal(t, "merge0", r.Mode)
	assert.Equal(t, llmreq.TaskMerge, r.Task)
	assert.Equal(t, "judge_consensus", r.Gate)
	assert.True(t, r.Evaluation.Fingerprint.OK)
	assert.False(t, r.Evaluation.Decision.Registrable, "a pilot never registers")
	swap, initials := r.Evaluation.Strata["swap"].Combos["A1"], r.Evaluation.Strata["initials"].Combos["A1"]
	assert.Equal(t, 4, r.Evaluation.Strata["swap"].Holdout)
	assert.Equal(t, 3, r.Evaluation.Strata["initials"].Holdout)
	assert.Equal(t, 4, swap.Accepted)
	assert.Zero(t, swap.Errors)
	assert.Equal(t, 3, swap.Scripts["cyrl"].Accepted)
	assert.Equal(t, 1, swap.Scripts["latn"].Accepted)
	assert.Equal(t, 2, initials.Scripts["cyrl"].Accepted)
	assert.Equal(t, 1, initials.Scripts["latn"].Accepted)
	// The suite: the pair says same on every namesake rendered in the swap
	// and initials shapes; the judges see the books and say different, so
	// the production rule accepts none.
	require.NotNil(t, swap.Adversarial)
	require.NotNil(t, initials.Adversarial)
	assert.Equal(t, 3, swap.Adversarial.Items)
	assert.Equal(t, 2, initials.Adversarial.Items)
	assert.Equal(t, swap.Adversarial.Items, swap.Adversarial.PairAccepted)
	assert.Zero(t, swap.Adversarial.Accepted)
	assert.Zero(t, initials.Adversarial.Accepted)
	require.NotNil(t, swap.Scripts["latn"].Adversarial)
	require.NotNil(t, initials.Scripts["latn"].Adversarial, "a romanized namesake")
	require.Len(t, r.Recall, 1, "the typo merge's two names")
	assert.Equal(t, 1, r.Recall[0].Accepted)
	assert.NotEmpty(t, r.Tokens)

	// No name and no title in the logs or the report (K's privacy rule).
	out := logs.String()
	assert.NotEmpty(t, out)
	for _, s := range secrets {
		assert.NotContains(t, out, s)
		assert.NotContains(t, string(raw), s)
	}
}

// Each card shows its own configuration's run: with a merge eval active,
// the name normalization's card neither shows it nor offers to close it as
// an older configuration's — it says another run holds the slot — and the
// merge card shows it with its one step blocked; K's admin starts no merge
// mode and the merge admin nothing but a merge eval.
func TestEachCardShowsItsOwnConfigurationsRun(t *testing.T) {
	e := newLLMEnv(t)
	m := e.mergeConfig()
	ctx := context.Background()
	k := services.NewAuthorLLMAdmin(e.db, e.version, "", &e.settings)
	merge := services.NewAuthorLLMMergeAdmin(e.db, m.version, "", &e.settings)

	st, err := merge.Status(ctx)
	require.NoError(t, err)
	require.Len(t, st.Steps, 1)
	assert.Equal(t, "merge0", st.Steps[0].Key)
	assert.True(t, st.Steps[0].Enabled)

	_, err = k.Start(ctx, string(models.AuthorLLMRunEval), models.AuthorLLMEvalModeMerge0, 1)
	require.ErrorIs(t, err, services.ErrAuthorLLMInvalidRun)
	_, err = merge.Start(ctx, string(models.AuthorLLMRunResolve), "pilot", 1)
	require.ErrorIs(t, err, services.ErrAuthorLLMInvalidRun)
	_, err = merge.Start(ctx, string(models.AuthorLLMRunEval), models.AuthorLLMEvalModePilot, 1)
	require.ErrorIs(t, err, services.ErrAuthorLLMInvalidRun)
	started, err := merge.Start(ctx, string(models.AuthorLLMRunEval), models.AuthorLLMEvalModeMerge0, e.adminUser())
	require.NoError(t, err)

	st, err = k.Status(ctx)
	require.NoError(t, err)
	assert.True(t, st.OtherActive)
	assert.Nil(t, st.Run, "K's card showed the merge run")
	for _, step := range st.Steps {
		assert.False(t, step.Enabled, step.Key)
	}
	st, err = merge.Status(ctx)
	require.NoError(t, err)
	assert.False(t, st.OtherActive)
	require.NotNil(t, st.Run)
	assert.Equal(t, started.ID, st.Run.ID)
	assert.True(t, st.Run.CurrentConfig)
	assert.Equal(t, services.AuthorLLMStepActiveRun, st.Steps[0].Blocked)

	// A merge report is never recomputed with K's gate.
	_, _, err = services.RecomputeAuthorLLMEvalReport(ctx, e.db, m.version, started.ID)
	require.ErrorIs(t, err, services.ErrAuthorLLMInvalidRun)
}
