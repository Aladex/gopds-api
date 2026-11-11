package authortriage

import (
	"encoding/json"
	"fmt"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// person is a person name of given and family, display and sort as the
// assembler writes them.
func person(given, family string) Name {
	return Name{Given: given, Family: family, Display: given + " " + family, Sort: family + ", " + given,
		Kind: "person"}
}

// handle is a person known by one word, as the normalizer proposes it.
func handle(word string) Name {
	return Name{Nickname: word, Display: word, Sort: word, Kind: "person"}
}

// assembled is what the assembler gives for a form other than person: a
// display and a kind, no name parts.
func assembled(display, kind string) *Name {
	return &Name{Display: display, Kind: kind}
}

func ptrName[T any](v T) *T { return &v }

// answers are the four stored answers of one run; the pair answers
// pairCanonical, the judges judgeCanonical.
func answers(run int64, judgeCanonical, pairCanonical, form string, n *Name) []Answer {
	var out []Answer
	for _, slot := range []string{"production_a", "production_b", "judge_a", "judge_b"} {
		c := judgeCanonical
		if slot == "production_a" || slot == "production_b" {
			c = pairCanonical
		}
		out = append(out, Answer{RunID: run, Arm: "a", BatchSize: 4, Slot: slot, Canonical: c, Form: form, Name: n})
	}
	return out
}

func fourModel(n *Name, form string) []Answer  { return answers(14, "c1", "c1", form, n) }
func judgesOnly(n *Name, form string) []Answer { return answers(14, "c1", "c2", form, n) }

// candidate is an open ambiguous item of class with a proposal.
//
//nolint:gocritic // a fixture takes its proposal by value, as the table rows spell it
func candidate(class string, proposal Name) *Candidate {
	return &Candidate{ItemID: 1, Class: class, Reason: "ambiguous_decision", HasProposal: true, Credits: 3,
		Proposal: proposal}
}

// source is the unselected credit the build reads for a proposal.
func source(class string, flags []string, given string, additional []string, family string) *authorentity.Unselected {
	display := given
	for _, a := range additional {
		display += " " + a
	}
	if family != "" {
		display += " " + family
	}
	return &authorentity.Unselected{
		Credit: authorentity.Credit{ID: 1, Fingerprint: "fp", ExtractorVersion: "v1",
			Result: &authorentity.Result{Kind: authorentity.KindPerson, Given: given, Additional: additional,
				Family: family, DisplayName: display, SortName: display, Script: "Cyrl"}},
		Class: class, Flags: flags, SourceGiven: given, SourceFamily: family,
	}
}

// testEnv knows «Михаил» and «Платон»: each stands beside five families.
func testEnv() *Env {
	var credits []authorentity.Credit
	for _, given := range []string{"Михаил", "Платон"} {
		for i := 0; i < 5; i++ {
			credits = append(credits, authorentity.Credit{ID: int64(len(credits) + 1),
				Result: &authorentity.Result{Kind: authorentity.KindPerson, Given: given,
					Family: fmt.Sprintf("Фамилия%c", 'А'+rune(i))}})
		}
	}
	return &Env{Given: authorentity.GivenNamesOf(credits)}
}

func TestDecideCascade(t *testing.T) {
	andreev := person("Михаил", "Андреев")
	caps := Name{Given: "МИХАИЛ", Family: "АНДРЕЕВ", Display: "МИХАИЛ АНДРЕЕВ", Sort: "АНДРЕЕВ, МИХАИЛ", Kind: "person"}
	capsSource := func() *authorentity.Unselected {
		return source("case_defect", []string{"all_caps"}, "МИХАИЛ", nil, "АНДРЕЕВ")
	}
	zorro := handle("Зорро")
	mononym := func(n Name) *Candidate {
		c := candidate("mononym", n)
		c.Source = source("mononym", []string{"single_token"}, n.Display, nil, "")
		return c
	}

	for title, c := range map[string]struct {
		c      *Candidate
		step   Step
		action Action
		rule   string
		after  Name
	}{
		"four-model accept": {
			c: func() *Candidate {
				c := candidate("particle", andreev)
				c.Answers = fourModel(ptrName(andreev), "person")
				return c
			}(),
			step: StepFourModel, action: ActionAccept, rule: "four_model", after: andreev,
		},
		"four-model edit": {
			c: func() *Candidate {
				c := candidate("single_field", handle("Андреев Михаил"))
				c.Answers = fourModel(ptrName(andreev), "person")
				return c
			}(),
			step: StepFourModel, action: ActionEdit, rule: "four_model", after: andreev,
		},
		"not_a_name with the proposal's display is a classify, not an edit": {
			c: func() *Candidate {
				c := candidate("mononym", handle("Админ"))
				c.Answers = fourModel(assembled("Админ", "unknown"), "not_a_name")
				return c
			}(),
			step: StepFourModel, action: ActionClassify, rule: "four_model", after: *assembled("Админ", "unknown"),
		},
		"multiple_persons with the proposal's display is a classify to collective": {
			c: func() *Candidate {
				c := candidate("duplicate_like", person("Братья", "Стругацкие"))
				c.Answers = fourModel(assembled("Братья Стругацкие", "collective"), "multiple_persons")
				return c
			}(),
			step: StepFourModel, action: ActionClassify, rule: "four_model",
			after: *assembled("Братья Стругацкие", "collective"),
		},
		"not_a_name with another display is an edit": {
			c: func() *Candidate {
				c := candidate("mononym", handle("Админ1"))
				c.Answers = fourModel(assembled("Админ", "unknown"), "not_a_name")
				return c
			}(),
			step: StepFourModel, action: ActionEdit, rule: "four_model", after: *assembled("Админ", "unknown"),
		},
		"judges only": {
			c: func() *Candidate {
				c := candidate("particle", andreev)
				c.Answers = judgesOnly(ptrName(andreev), "person")
				return c
			}(),
			step: StepJudgesOnly, action: ActionAccept, rule: "judges_only", after: andreev,
		},
		"judges in conflict across two runs fall to the handle": {
			c: func() *Candidate {
				c := mononym(zorro)
				c.Answers = append(answers(14, "c1", "c1", "person", ptrName(zorro)),
					answers(27, "c9", "c9", "person", ptrName(handle("Зорро Второй")))...)
				return c
			}(),
			step: StepKeepHandle, action: ActionAccept, rule: "keep_handle", after: zorro,
		},
		"a refused judge is no consensus": {
			c: func() *Candidate {
				c := candidate("single_field", andreev)
				c.Answers = fourModel(ptrName(andreev), "person")
				c.Answers[2].Refused, c.Answers[2].Name = true, nil
				return c
			}(),
		},
		"an answer that no longer assembles falls through": {
			c: func() *Candidate {
				c := candidate("single_field", andreev)
				c.Answers = fourModel(nil, "person")
				return c
			}(),
		},
		"all caps with a known given name": {
			c:    func() *Candidate { c := candidate("case_defect", caps); c.Source = capsSource(); return c }(),
			step: StepRules, action: ActionEdit, rule: "rules:case",
			after: Name{Given: "Михаил", Family: "Андреев", Display: "Михаил Андреев", Sort: "Андреев, Михаил",
				Kind: "person"},
		},
		"all caps with a prefix": {
			c: func() *Candidate {
				p := caps
				p.Prefix = "проф."
				c := candidate("case_defect", p)
				c.Source = capsSource()
				return c
			}(),
		},
		"the one same-name structure": {
			c: func() *Candidate {
				c := candidate("single_field", handle("Андреев Михаил"))
				c.EntityRule, c.SameName = "same_name", []Name{andreev}
				return c
			}(),
			step: StepSameName, action: ActionEdit, rule: "same_name", after: andreev,
		},
		"two same-name structures": {
			c: func() *Candidate {
				c := candidate("single_field", handle("Андреев Михаил"))
				c.EntityRule, c.SameName = "same_name", []Name{andreev, person("Андреев", "Михаил")}
				return c
			}(),
		},
		"one structure of an entity joined by another rule": {
			c: func() *Candidate {
				c := candidate("single_field", handle("Андреев Михаил"))
				c.EntityRule, c.SameName = "as_written", []Name{andreev}
				return c
			}(),
		},
		"a same-name structure that is no person": {
			c: func() *Candidate {
				c := candidate("single_field", handle("Андреев Михаил"))
				c.EntityRule, c.SameName = "same_name", []Name{*assembled("Андреев Михаил", "collective")}
				return c
			}(),
		},
		"an answer with no display falls through": {
			c: func() *Candidate {
				c := candidate("single_field", andreev)
				c.Answers = fourModel(&Name{Given: "Михаил", Family: "Андреев", Kind: "person"}, "person")
				return c
			}(),
		},
		"a two-word mononym is no handle": {
			c: func() *Candidate {
				c := mononym(Name{Nickname: "Зорро Второй", Display: "Зорро Второй", Kind: "person"})
				return c
			}(),
		},
		"a placeholder phrase": {
			c:    candidate("partial_structure", person("неизвестен", "Автор")),
			step: StepPlaceholderOrg, action: ActionClassify, rule: "placeholder",
			after: func() Name { n := person("неизвестен", "Автор"); n.Kind = "unknown"; return n }(),
		},
		"an organization": {
			c:    candidate("unusual_characters", handle(`Журнал "Юный техник"`)),
			step: StepPlaceholderOrg, action: ActionClassify, rule: "organization",
			after: func() Name { n := handle(`Журнал "Юный техник"`); n.Kind = "collective"; return n }(),
		},
		"a collective phrase": {
			c:    candidate("unusual_characters", person("Коллектив", "авторов")),
			step: StepPlaceholderOrg, action: ActionClassify, rule: "organization",
			after: func() Name { n := person("Коллектив", "авторов"); n.Kind = "collective"; return n }(),
		},
		"a collective candidate read as a pair": {
			c: func() *Candidate {
				c := candidate("collective_candidate", handle(`Журнал "Юный техник"`))
				c.EntityRule = "pair"
				return c
			}(),
		},
		"a collective candidate read as named": {
			c: func() *Candidate {
				c := candidate("collective_candidate", handle("Ансамбль Березка"))
				c.EntityRule = "as_named"
				return c
			}(),
			step: StepAsNamedCollective, action: ActionClassify, rule: "as_named",
			after: func() Name { n := handle("Ансамбль Березка"); n.Kind = "collective"; return n }(),
		},
		"Admin is a placeholder, not a handle": {
			c:    mononym(handle("Admin")),
			step: StepPlaceholderOrg, action: ActionClassify, rule: "placeholder",
			after: func() Name { n := handle("Admin"); n.Kind = "unknown"; return n }(),
		},
		"a handle": {
			c:    mononym(zorro),
			step: StepKeepHandle, action: ActionAccept, rule: "keep_handle", after: zorro,
		},
		"a known given name is no handle": {c: mononym(handle("Платон"))},
		"a one-word name of another class is no handle": {
			c: func() *Candidate {
				c := candidate("single_field", zorro)
				c.Source = source("single_field", []string{"single_token"}, "Зорро", nil, "")
				return c
			}(),
		},
		"a handle a judge called a placeholder": {
			c: func() *Candidate {
				c := mononym(zorro)
				c.Answers = answers(14, "c1", "c1", "person", ptrName(zorro))
				c.Answers[2].Form, c.Answers[2].Canonical = "placeholder", "c3"
				return c
			}(),
		},
		"a tagged handle": {
			c: func() *Candidate { c := mononym(zorro); c.Source.Tag = 2; return c }(),
		},
		"a particle only": {
			c: func() *Candidate {
				c := candidate("particle", person("Ги", "де Мопассан"))
				c.Source = source("particle", []string{"particle"}, "Ги", nil, "де Мопассан")
				return c
			}(),
			step: StepParticleOnly, action: ActionAccept, rule: "particle_only", after: person("Ги", "де Мопассан"),
		},
		"a family that ends in a particle": {
			c: func() *Candidate {
				p := Name{Given: "Мопассан", Additional: "Ги", Family: "Де", Display: "Мопассан Ги Де",
					Sort: "Де, Мопассан Ги", Kind: "person"}
				c := candidate("particle", p)
				c.Source = source("particle", []string{"particle"}, "Мопассан", []string{"Ги"}, "Де")
				return c
			}(),
		},
	} {
		t.Run(title, func(t *testing.T) {
			d := Decide(c.c, testEnv())
			assert.Empty(t, d.Excluded)
			assert.Equal(t, c.step, d.Step)
			assert.Equal(t, c.action, d.Action)
			assert.Equal(t, c.rule, d.Rule)
			if c.step == 0 {
				assert.Equal(t, Name{}, d.After, "a left item has no decision")
				return
			}
			assert.Equal(t, c.after, d.After)
		})
	}
}

func TestDecideEvidence(t *testing.T) {
	andreev := person("Михаил", "Андреев")
	c := candidate("particle", andreev)
	c.Answers = append(answers(27, "c1", "c2", "person", ptrName(andreev)),
		answers(14, "c1", "c1", "person", ptrName(andreev))...)
	d := Decide(c, testEnv())
	require.Equal(t, StepFourModel, d.Step)
	assert.Equal(t, Evidence{RunID: 14, Arm: "a", BatchSize: 4, Canonical: "c1", Form: "person",
		PairRunID: 14, PairArm: "a", PairBatchSize: 4}, d.Evidence, "the lowest run that carries the canonical")

	// Run 14's pair disagrees, run 27's agrees: the evidence names both.
	c.Answers = append(answers(14, "c1", "c2", "person", ptrName(andreev)),
		answers(27, "c1", "c1", "person", ptrName(andreev))...)
	for i := range c.Answers[4:] {
		c.Answers[4+i].Arm, c.Answers[4+i].BatchSize = "b", 10
	}
	d = Decide(c, testEnv())
	require.Equal(t, StepFourModel, d.Step)
	assert.Equal(t, Evidence{RunID: 14, Arm: "a", BatchSize: 4, Canonical: "c1", Form: "person",
		PairRunID: 27, PairArm: "b", PairBatchSize: 10}, d.Evidence, "the judges' run and the confirming pair's")
	raw, err := json.Marshal(d.Evidence)
	require.NoError(t, err)
	assert.JSONEq(t, `{"run_id":14,"arm":"a","batch_size":4,"canonical":"c1","form":"person",
		"pair_run_id":27,"pair_arm":"b","pair_batch_size":10}`, string(raw))

	judged := candidate("particle", andreev)
	judged.Answers = answers(14, "c1", "c2", "person", ptrName(andreev))
	d = Decide(judged, testEnv())
	require.Equal(t, StepJudgesOnly, d.Step)
	assert.Equal(t, Evidence{RunID: 14, Arm: "a", BatchSize: 4, Canonical: "c1", Form: "person"}, d.Evidence,
		"step 2 has no confirming pair")

	c = candidate("case_defect", Name{Given: "МИХАИЛ", Family: "АНДРЕЕВ", Display: "МИХАИЛ АНДРЕЕВ", Kind: "person"})
	c.Source = source("case_defect", []string{"all_caps"}, "МИХАИЛ", nil, "АНДРЕЕВ")
	assert.Equal(t, Evidence{Rule: "rules:case"}, Decide(c, testEnv()).Evidence)
}

func TestExclusionsComeFirst(t *testing.T) {
	andreev := person("Михаил", "Андреев")
	for _, c := range []struct {
		want Exclusion
		set  func(c *Candidate)
	}{
		{ExcludedNotAmbiguous, func(c *Candidate) { c.Reason = "incompatible_manual_schema" }},
		{ExcludedCreditScoped, func(c *Candidate) { c.CreditScoped = true }},
		{ExcludedNoProposal, func(c *Candidate) { c.HasProposal = false }},
		{ExcludedDuplicateComponent, func(c *Candidate) { c.Class = "duplicate_component" }},
		{ExcludedPinned, func(c *Candidate) { c.Pinned = true }},
		{ExcludedUndoneBefore, func(c *Candidate) { c.UndoneBefore = true }},
	} {
		t.Run(string(c.want), func(t *testing.T) {
			cand := candidate("particle", andreev)
			cand.Answers = fourModel(ptrName(andreev), "person")
			c.set(cand)
			d := Decide(cand, testEnv())
			assert.Equal(t, c.want, d.Excluded)
			assert.Zero(t, d.Step)
			assert.Empty(t, d.Action)
			assert.Equal(t, Name{}, d.After)
		})
	}

	t.Run("in their order", func(t *testing.T) {
		cand := candidate("duplicate_component", andreev)
		cand.Answers = fourModel(ptrName(andreev), "person")
		cand.Pinned, cand.UndoneBefore, cand.CreditScoped = true, true, true
		assert.Equal(t, ExcludedCreditScoped, Decide(cand, testEnv()).Excluded)
		cand.CreditScoped = false
		assert.Equal(t, ExcludedDuplicateComponent, Decide(cand, testEnv()).Excluded)
		cand.Class = "particle"
		assert.Equal(t, ExcludedPinned, Decide(cand, testEnv()).Excluded)
		cand.Pinned = false
		assert.Equal(t, ExcludedUndoneBefore, Decide(cand, testEnv()).Excluded)
	})
}

// Every form the models give, under every step's item shape, and kinds no
// assembler gives: the batch never classifies malformed and never takes an
// action other than the three.
func TestDecideNeverClassifiesMalformed(t *testing.T) {
	kinds := map[string]string{"person": "person", "collective": "collective", "multiple_persons": "collective",
		"not_a_name": "unknown", "placeholder": "unknown", "bogus": "malformed"}
	shapes := map[string]func(n *Name, form string) *Candidate{
		"four model": func(n *Name, form string) *Candidate {
			c := candidate("particle", person("Ги", "де Мопассан"))
			c.Answers = fourModel(n, form)
			return c
		},
		"judges only": func(n *Name, form string) *Candidate {
			c := candidate("particle", person("Ги", "де Мопассан"))
			c.Answers = judgesOnly(n, form)
			return c
		},
		"rules": func(_ *Name, _ string) *Candidate {
			c := candidate("case_defect", Name{Given: "МИХАИЛ", Family: "АНДРЕЕВ", Display: "МИХАИЛ АНДРЕЕВ",
				Kind: "malformed"})
			c.Source = source("case_defect", []string{"all_caps"}, "МИХАИЛ", nil, "АНДРЕЕВ")
			return c
		},
		"same name": func(n *Name, _ string) *Candidate {
			c := candidate("single_field", handle("Андреев Михаил"))
			c.EntityRule, c.SameName = "same_name", []Name{*n}
			return c
		},
		"placeholder": func(_ *Name, _ string) *Candidate {
			return candidate("mononym", Name{Display: "Admin", Kind: "malformed"})
		},
		"as named": func(_ *Name, _ string) *Candidate {
			c := candidate("collective_candidate", Name{Display: "Ансамбль", Kind: "malformed"})
			c.EntityRule = "as_named"
			return c
		},
		"handle": func(_ *Name, _ string) *Candidate {
			c := candidate("mononym", Name{Display: "Зорро", Kind: "malformed"})
			c.Source = source("mononym", nil, "Зорро", nil, "")
			return c
		},
		"particle": func(_ *Name, _ string) *Candidate {
			c := candidate("particle", Name{Given: "Ги", Family: "де Мопассан", Display: "Ги де Мопассан",
				Kind: "malformed"})
			c.Source = source("particle", []string{"particle"}, "Ги", nil, "де Мопассан")
			return c
		},
	}
	for shape, build := range shapes {
		for form, kind := range kinds {
			t.Run(shape+"/"+form, func(t *testing.T) {
				n := assembled("Ги де Мопассан", kind)
				if kind == "person" {
					n = ptrName(person("Ги", "де Мопассан"))
				}
				d := Decide(build(n, form), testEnv())
				assert.NotEqual(t, "malformed", d.After.Kind)
				if d.Step == 0 {
					assert.Empty(t, d.Action)
					return
				}
				assert.Contains(t, []Action{ActionAccept, ActionEdit, ActionClassify}, d.Action)
				assert.Contains(t, []string{"person", "collective", "unknown"}, d.After.Kind)
				assert.NotEmpty(t, d.After.Display)
			})
		}
	}
}

func TestActionFor(t *testing.T) {
	p := person("Михаил", "Андреев")
	edited := p
	edited.Sort = "Андреев Михаил"
	classified := p
	classified.Kind = "collective"
	assert.Equal(t, ActionAccept, ActionFor(&p, &p))
	assert.Equal(t, ActionEdit, ActionFor(&p, &edited), "any part differs")
	assert.Equal(t, ActionClassify, ActionFor(&p, &classified), "only the kind differs")

	// A non-person answer carries no parts: display and kind decide.
	assert.Equal(t, ActionClassify, ActionFor(&p, assembled("Михаил Андреев", "unknown")))
	assert.Equal(t, ActionAccept, ActionFor(&classified, assembled("Михаил Андреев", "collective")))
	assert.Equal(t, ActionEdit, ActionFor(&p, assembled("Андреев", "unknown")))
	assert.Equal(t, ActionEdit, ActionFor(assembled("Михаил Андреев", "unknown"), &p),
		"to a person, the parts are new")
	unknown := p
	unknown.Kind = "unknown"
	assert.Equal(t, ActionClassify, ActionFor(&unknown, &p), "to a person, only the kind differs")
}

func TestPlaceholderPhrase(t *testing.T) {
	for _, s := range []string{"неизвестен Автор", "(Неизвестен автор)", "Неизвестный автор", "anonymous", "Аноним",
		"Admin", "USER", "Автор", "неизвестен неизвестен", "Unbekannter Autor", //nolint:misspell // a German placeholder phrase
		"Невідомий автор", "noname",
		"Неизвестного", "anonymus", "Нет автора", "Author Unknown", "Анонимный автор"} {
		assert.True(t, PlaceholderPhrase(s), s)
	}
	for _, s := range []string{"Аноним Иванов", "Неизвестный Солдат Второй", "Автор Книги", "Михаил Андреев", "",
		"Unknown Pleasures", "Администратор"} {
		assert.False(t, PlaceholderPhrase(s), s)
	}
}

func TestStepsAreTheCascadeInOrder(t *testing.T) {
	var names []string
	for i, s := range Steps() {
		assert.Equal(t, Step(i+1), s)
		names = append(names, s.Name())
	}
	assert.Equal(t, []string{"four_model", "judges_only", "rules", "same_name", "placeholder_org",
		"as_named_collective", "keep_handle", "particle_only"}, names)
	assert.Empty(t, Step(0).Name())
	assert.Empty(t, Step(9).Name())
	assert.Equal(t, "triage-v1", RuleVersion)
}

// An item two steps hold for is decided by the earlier one, with its own
// name and evidence: the cascade stops at the first step that holds.
func TestTheFirstStepThatHoldsDecides(t *testing.T) {
	swapped := person("Андреев", "Михаил")
	caps := Name{Given: "МИХАИЛ", Family: "АНДРЕЕВ", Display: "МИХАИЛ АНДРЕЕВ", Sort: "АНДРЕЕВ, МИХАИЛ", Kind: "person"}
	capsItem := candidate("case_defect", caps)
	capsItem.Source = source("case_defect", []string{"all_caps"}, "МИХАИЛ", nil, "АНДРЕЕВ")
	capsItem.Answers = fourModel(ptrName(swapped), "person")

	zorro := handle("Зорро")
	given := Name{Given: "Зорро", Display: "Зорро", Sort: "Зорро", Kind: "person"}
	handleItem := candidate("mononym", zorro)
	handleItem.Source = source("mononym", []string{"single_token"}, "Зорро", nil, "")
	handleItem.Answers = fourModel(ptrName(given), "person")

	for title, c := range map[string]struct {
		c     *Candidate
		later Step
		after Name
		other Name
	}{
		"four models before the rules": {c: capsItem, later: StepRules, after: swapped,
			other: person("Михаил", "Андреев")},
		"four models before the kept handle": {c: handleItem, later: StepKeepHandle, after: given, other: zorro},
	} {
		t.Run(title, func(t *testing.T) {
			env := testEnv()
			cons := JudgesConsensus(c.c.Answers)
			other, _, ok := cascade[c.later](c.c, env, &cons)
			require.True(t, ok, "the later step holds too")
			require.Equal(t, c.other, other)
			require.NotEqual(t, c.after, other, "the two steps give different names")

			d := Decide(c.c, env)
			assert.Equal(t, StepFourModel, d.Step)
			assert.Equal(t, "four_model", d.Rule)
			assert.Equal(t, ActionEdit, d.Action)
			assert.Equal(t, c.after, d.After)
			assert.Equal(t, Evidence{RunID: 14, Arm: "a", BatchSize: 4, Canonical: "c1", Form: "person",
				PairRunID: 14, PairArm: "a", PairBatchSize: 4}, d.Evidence)
		})
	}
}
