package llmres

import (
	"errors"
	"testing"
)

// Fixes from eval pilot run 4, with its real cases. The order is computed
// from the roles — a model no longer answers it — and a cannot_tell answer
// is its form alone.

func run4Input(t *testing.T, fields ...FieldValue) *Input {
	t.Helper()
	in, err := NewInput(fields)
	if err != nil {
		t.Fatal(err)
	}
	return &in
}

func parseValid(t *testing.T, in *Input, raw string) Reply {
	t.Helper()
	rep, err := ParseReply([]byte(raw))
	if err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	if err := rep.Validate(in); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return rep
}

// «А Сказов»: Opus labeled given,family and answered order family_first,
// reading the order as a catalog convention — V5 refused it. The order is
// no longer the model's to give.
func TestRun4OrderIsComputedFromTheRoles(t *testing.T) {
	in := run4Input(t, FieldValue{Field: FieldFirst, Text: "А"}, FieldValue{Field: FieldLast, Text: "Сказов"})
	rep := parseValid(t, in, `{"form":"person","roles":[{"i":0,"role":"given"},{"i":1,"role":"family"}],"case_fix":[]}`)
	if rep.Order != OrderGivenFirst {
		t.Errorf("order = %q, want given_first from the roles", rep.Order)
	}
	family := parseValid(t, in, `{"form":"person","roles":[{"i":0,"role":"family"},{"i":1,"role":"given"}],"case_fix":[]}`)
	if family.Order != OrderFamilyFirst {
		t.Errorf("order = %q, want family_first", family.Order)
	}
	// The run-4 reply itself: an order is not part of the answer any more.
	_, err := ParseReply([]byte(`{"form":"person","order":"family_first",` +
		`"roles":[{"i":0,"role":"given"},{"i":1,"role":"family"}],"case_fix":[]}`))
	var v *Violation
	if !errors.As(err, &v) || v.Validator != validatorV1 {
		t.Errorf("a reply with an order: %v, want V1", err)
	}

	single := run4Input(t, FieldValue{Field: FieldNickname, Text: "Дельфин"})
	mononym := `{"form":"person","roles":[{"i":0,"role":"single_name"}],"case_fix":[]}`
	if got := parseValid(t, single, mononym); got.Order != OrderSingleName {
		t.Errorf("mononym order = %q", got.Order)
	}
	collective := `{"form":"collective","roles":[{"i":0,"role":"single_name"}],"case_fix":[]}`
	if got := parseValid(t, single, collective); got.Order != OrderNotApplicable {
		t.Errorf("collective order = %q", got.Order)
	}
}

// The canonical answer does not carry an order: it follows from the roles.
func TestRun4CanonicalFormHasNoOrder(t *testing.T) {
	in := run4Input(t, FieldValue{Field: FieldFirst, Text: "А"}, FieldValue{Field: FieldLast, Text: "Сказов"})
	rep := parseValid(t, in, `{"form":"person","roles":[{"i":1,"role":"family"},{"i":0,"role":"given"}],"case_fix":[]}`)
	want := `{"form":"person","roles":[{"i":0,"role":"given"},{"i":1,"role":"family"}],"case_fix":[]}`
	if got := string(rep.CanonicalBytes()); got != want {
		t.Errorf("canonical = %s, want %s", got, want)
	}
}

// «М», «Л»: both judges answered cannot_tell with different roles — the same
// refusal. A person's roles still decide agreement.
func TestRun4CannotTellIsItsFormAlone(t *testing.T) {
	for _, name := range []string{"М", "Л", "Б"} {
		in := run4Input(t, FieldValue{Field: FieldFirst, Text: name})
		a := parseValid(t, in, `{"form":"cannot_tell","roles":[{"i":0,"role":"given"}],"case_fix":[]}`)
		b := parseValid(t, in, `{"form":"cannot_tell","roles":[{"i":0,"role":"family"}],"case_fix":[]}`)
		if !Agree(&a, &b) {
			t.Errorf("«%s»: two cannot_tell answers with different roles disagree", name)
		}
		if got := string(a.CanonicalBytes()); got != `{"form":"cannot_tell","roles":[],"case_fix":[]}` {
			t.Errorf("cannot_tell canonical = %s", got)
		}
	}
	in := run4Input(t, FieldValue{Field: FieldFirst, Text: "Л"})
	a := parseValid(t, in, `{"form":"person","roles":[{"i":0,"role":"given"}],"case_fix":[]}`)
	b := parseValid(t, in, `{"form":"person","roles":[{"i":0,"role":"family"}],"case_fix":[]}`)
	if Agree(&a, &b) {
		t.Error("two person answers with different roles agree")
	}
	// A collective's roles still matter (filler and separator shape its
	// display): only a refusal is its form alone.
	c := parseValid(t, in, `{"form":"collective","roles":[{"i":0,"role":"single_name"}],"case_fix":[]}`)
	d := parseValid(t, in, `{"form":"collective","roles":[{"i":0,"role":"given"}],"case_fix":[]}`)
	if Agree(&c, &d) {
		t.Error("two collective answers with different roles agree")
	}
}

// The run-4 shapes the new prompt rules settle are valid answers: a Western
// middle name is additional, a double surname is family twice, and the
// tokens of the nickname field stay nickname.
func TestRun4PromptRuleShapesAreValid(t *testing.T) {
	cases := []struct {
		name   string
		fields []FieldValue
		reply  string
		order  Order
	}{
		{"Дж. Майкл Стражински", []FieldValue{{Field: FieldFirst, Text: "Дж."}, {Field: FieldMiddle, Text: "Майкл"},
			{Field: FieldLast, Text: "Стражински"}},
			`{"form":"person","roles":[{"i":0,"role":"given"},{"i":1,"role":"additional"},{"i":2,"role":"family"}],"case_fix":[]}`,
			OrderGivenFirst},
		{"Ф. Пол Уилсон", []FieldValue{{Field: FieldFirst, Text: "Ф. Пол"}, {Field: FieldLast, Text: "Уилсон"}},
			`{"form":"person","roles":[{"i":0,"role":"given"},{"i":1,"role":"additional"},{"i":2,"role":"family"}],"case_fix":[]}`,
			OrderGivenFirst},
		{"Х Гарсия Мартинес", []FieldValue{{Field: FieldFirst, Text: "Х"}, {Field: FieldLast, Text: "Гарсия Мартинес"}},
			`{"form":"person","roles":[{"i":0,"role":"given"},{"i":1,"role":"family"},{"i":2,"role":"family"}],"case_fix":[]}`,
			OrderGivenFirst},
		{"Милослав Князев К. Милослав", []FieldValue{{Field: FieldFirst, Text: "Милослав"}, {Field: FieldLast, Text: "Князев"},
			{Field: FieldNickname, Text: "К. Милослав"}},
			`{"form":"person","roles":[{"i":0,"role":"given"},{"i":1,"role":"family"},{"i":2,"role":"nickname"},` +
				`{"i":3,"role":"nickname"}],"case_fix":[]}`,
			OrderGivenFirst},
	}
	for _, c := range cases {
		in := run4Input(t, c.fields...)
		if rep := parseValid(t, in, c.reply); rep.Order != c.order {
			t.Errorf("%s: order = %q", c.name, rep.Order)
		}
	}
}

// A frozen anchor set stores the expected answer as it was canonical when it
// was frozen — with an order. It still reads, and still matches a model's
// answer of the same roles.
func TestRun4StoredAnswerWithAnOrderStillMatches(t *testing.T) {
	stored := `{"form":"person","order":"given_first","roles":[{"i":0,"role":"given"},{"i":1,"role":"family"}],"case_fix":[]}`
	want, err := ParseStoredReply([]byte(stored))
	if err != nil {
		t.Fatalf("a stored anchor answer: %v", err)
	}
	in := run4Input(t, FieldValue{Field: FieldFirst, Text: "Иван"}, FieldValue{Field: FieldLast, Text: "Петров"})
	got := parseValid(t, in, `{"form":"person","roles":[{"i":0,"role":"given"},{"i":1,"role":"family"}],"case_fix":[]}`)
	if !Agree(&want, &got) {
		t.Errorf("stored %s and answered %s disagree", want.CanonicalBytes(), got.CanonicalBytes())
	}
	if _, err := ParseStoredReply([]byte(`{"form":"person","order":"given_first","roles":[],"case_fix":[],"x":1}`)); err == nil {
		t.Error("a stored answer with another unknown key read")
	}
}
