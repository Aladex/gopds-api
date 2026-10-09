package authornorm

import (
	"crypto/sha256"
	"errors"
	"slices"
	"testing"
)

// evidence stands in for the immutable eval report hash a registration must
// reference (scope A2); the bytes are arbitrary but never all-zero.
func evidence(label string) [32]byte { return sha256.Sum256([]byte("eval-report:" + label)) }

func structuredPersonResult(t *testing.T) Result {
	t.Helper()
	return mustNormalize(t, mustSource(t,
		SourceComponent{Kind: ComponentFirst, Value: "Лев"},
		SourceComponent{Kind: ComponentLast, Value: "Толстой"},
	))
}

func mustPolicy(t *testing.T, version int, rows ...ClassRegistration) AcceptancePolicy {
	t.Helper()
	p, err := NewAcceptancePolicy(version, NormalizerVersion, rows)
	if err != nil {
		t.Fatalf("NewAcceptancePolicy(%d): %v", version, err)
	}
	return p
}

func mustDecide(t *testing.T, p AcceptancePolicy, r *Result) Decision {
	t.Helper()
	d, err := p.Decide(r)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	return d
}

func TestProductionPolicyIsEmpty(t *testing.T) {
	p := ProductionAcceptancePolicy()
	if !p.Empty() {
		t.Fatal("the production acceptance policy must register no class (scope A2)")
	}
	if p.Version() <= 0 {
		t.Errorf("production policy version = %d, want positive", p.Version())
	}
}

// TestEmptyPolicySelectsNothing runs every golden case through the production
// policy: zero automatic selection, and every credit lands in exactly the A3
// bucket its decision class names.
func TestEmptyPolicySelectsNothing(t *testing.T) {
	p := ProductionAcceptancePolicy()
	for _, tc := range loadLocalCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			r := mustNormalize(t, tc.source(t))
			d := mustDecide(t, p, &r)
			if d.Selected() {
				t.Fatalf("empty policy selected class %q", r.DecisionClass)
			}
			var want Outcome
			var wantReason UnresolvedReason
			switch {
			case r.Kind == KindMalformed:
				want = OutcomeInvalid
			case r.DecisionClass.Ambiguous():
				want = OutcomeReview
			default:
				want, wantReason = OutcomeUnresolved, ReasonPolicyNotRegistered
			}
			if d.Outcome != want || d.Reason != wantReason {
				t.Errorf("class %q: decision = %q/%q, want %q/%q", r.DecisionClass, d.Outcome, d.Reason, want, wantReason)
			}
		})
	}
}

func TestEmptyPolicyDoesNotSelectAValidStructuredCandidate(t *testing.T) {
	r := structuredPersonResult(t)
	if err := r.Validate(); err != nil {
		t.Fatalf("candidate is not valid: %v", err)
	}
	d := mustDecide(t, mustPolicy(t, 7), &r)
	if d.Selected() || d.Outcome != OutcomeUnresolved || d.Reason != ReasonPolicyNotRegistered {
		t.Errorf("decision = %+v, want unresolved/policy_not_registered", d)
	}
}

// Policy versions are cumulative: a pair registered in version N belongs to
// every later version, and to no earlier one. A registration only ever adds a
// pair, under a version of its own, so the selections made under an older
// version stay exactly what that version said.
func TestPolicyVersionsAreCumulative(t *testing.T) {
	r := structuredPersonResult(t)
	proof := evidence("structured-v2")
	rows := []ClassRegistration{
		{PolicyVersion: 2, DecisionClass: ClassStructuredPerson, Script: ScriptCyrillic, EvidenceSHA256: proof},
		{PolicyVersion: 3, DecisionClass: ClassPlaceholder, Script: ScriptCyrillic, EvidenceSHA256: evidence("placeholder-v3")},
	}
	placeholder := mustNormalize(t, mustSource(t, SourceComponent{Kind: ComponentLast, Value: "Аноним"}))

	// Version 1 predates both registrations.
	for _, res := range []*Result{&r, &placeholder} {
		if d := mustDecide(t, mustPolicy(t, 1, rows...), res); d.Selected() {
			t.Errorf("policy 1 selected %q registered only later: %+v", res.DecisionClass, d)
		}
	}

	d := mustDecide(t, mustPolicy(t, 2, rows...), &r)
	if !d.Selected() || d.PolicyVersion != 2 || d.EvidenceSHA256 != proof {
		t.Fatalf("policy 2, structured_person: decision = %+v, want selected under 2 with its evidence", d)
	}
	if d := mustDecide(t, mustPolicy(t, 2, rows...), &placeholder); d.Selected() {
		t.Errorf("policy 2 selected %q registered in 3", placeholder.DecisionClass)
	}

	// Version 3 keeps the version-2 pair and adds its own; a later version
	// with no row of its own is the same set again.
	for _, version := range []int{3, 4} {
		p := mustPolicy(t, version, rows...)
		if d := mustDecide(t, p, &r); !d.Selected() || d.PolicyVersion != version || d.EvidenceSHA256 != proof {
			t.Errorf("policy %d, structured_person: decision = %+v, want selected under %d with the v2 evidence",
				version, d, version)
		}
		if d := mustDecide(t, p, &placeholder); !d.Selected() || d.PolicyVersion != version {
			t.Errorf("policy %d, placeholder: decision = %+v, want selected", version, d)
		}
	}
}

// Registration is per (decision class, script): a Cyrillic registration
// selects Cyrillic results of the class and nothing else.
func TestPolicyIsKeyedByScript(t *testing.T) {
	cyrillic := structuredPersonResult(t)
	latin := mustNormalize(t, mustSource(t,
		SourceComponent{Kind: ComponentFirst, Value: "Arthur"},
		SourceComponent{Kind: ComponentLast, Value: "Conan"},
	))
	if cyrillic.DecisionClass != ClassStructuredPerson || latin.DecisionClass != ClassStructuredPerson ||
		cyrillic.Script != ScriptCyrillic || latin.Script != ScriptLatin {
		t.Fatalf("fixtures drifted: %q/%q and %q/%q", cyrillic.DecisionClass, cyrillic.Script, latin.DecisionClass, latin.Script)
	}
	cyrl := ClassRegistration{PolicyVersion: 2, DecisionClass: ClassStructuredPerson, Script: ScriptCyrillic, EvidenceSHA256: evidence("cyrl")}
	latn := ClassRegistration{PolicyVersion: 3, DecisionClass: ClassStructuredPerson, Script: ScriptLatin, EvidenceSHA256: evidence("latn")}

	onlyCyrillic := mustPolicy(t, 2, cyrl, latn)
	if d := mustDecide(t, onlyCyrillic, &cyrillic); !d.Selected() {
		t.Errorf("registered (structured_person, Cyrl): decision = %+v, want selected", d)
	}
	if d := mustDecide(t, onlyCyrillic, &latin); d.Selected() || d.Outcome != OutcomeUnresolved || d.Reason != ReasonPolicyNotRegistered {
		t.Errorf("unregistered (structured_person, Latn): decision = %+v, want unresolved/policy_not_registered", d)
	}

	both := mustPolicy(t, 3, cyrl, latn)
	for _, tc := range []struct {
		r     *Result
		proof [32]byte
	}{{&cyrillic, cyrl.EvidenceSHA256}, {&latin, latn.EvidenceSHA256}} {
		if d := mustDecide(t, both, tc.r); !d.Selected() || d.EvidenceSHA256 != tc.proof {
			t.Errorf("%s under both pairs: decision = %+v, want selected with its own evidence", tc.r.Script, d)
		}
	}
}

func TestPolicyRoutesAmbiguousAndMalformedEvenWhenOtherClassesAreRegistered(t *testing.T) {
	p := mustPolicy(t, 1,
		ClassRegistration{PolicyVersion: 1, DecisionClass: ClassStructuredPerson, Script: ScriptCyrillic, EvidenceSHA256: evidence("a")},
		ClassRegistration{PolicyVersion: 1, DecisionClass: ClassPlaceholder, Script: ScriptCyrillic, EvidenceSHA256: evidence("b")},
	)
	initials := mustNormalize(t, mustSource(t,
		SourceComponent{Kind: ComponentFirst, Value: "И."},
		SourceComponent{Kind: ComponentLast, Value: "Петров"},
	))
	if d := mustDecide(t, p, &initials); d.Outcome != OutcomeReview || d.Selected() {
		t.Errorf("initials: decision = %+v, want review", d)
	}
	garbage := mustNormalize(t, mustSource(t, SourceComponent{Kind: ComponentLast, Value: "***"}))
	if d := mustDecide(t, p, &garbage); d.Outcome != OutcomeInvalid || d.Selected() {
		t.Errorf("malformed: decision = %+v, want invalid", d)
	}
}

func TestNewAcceptancePolicyRejectsInvalidInput(t *testing.T) {
	ok := ClassRegistration{PolicyVersion: 1, DecisionClass: ClassStructuredPerson, Script: ScriptCyrillic, EvidenceSHA256: evidence("ok")}
	row := func(version int, class DecisionClass, label string) ClassRegistration {
		return ClassRegistration{PolicyVersion: version, DecisionClass: class, Script: ScriptCyrillic, EvidenceSHA256: evidence(label)}
	}
	unproven := ClassRegistration{PolicyVersion: 1, DecisionClass: ClassPlaceholder, Script: ScriptCyrillic}
	withScript := func(r ClassRegistration, script Script) ClassRegistration { r.Script = script; return r }
	cases := []struct {
		name       string
		version    int
		normalizer string
		rows       []ClassRegistration
		want       error
	}{
		{"zero policy version", 0, NormalizerVersion, nil, ErrInvalidPolicyVersion},
		{"negative policy version", -3, NormalizerVersion, nil, ErrInvalidPolicyVersion},
		{"blank normalizer version", 1, " ", nil, ErrEmptyVersion},
		{"row with zero version", 1, NormalizerVersion, []ClassRegistration{ok, row(0, ClassPlaceholder, "z")}, ErrInvalidPolicyVersion},
		{"row with negative version", 1, NormalizerVersion, []ClassRegistration{row(-1, ClassPlaceholder, "n")}, ErrInvalidPolicyVersion},
		{"unknown class", 1, NormalizerVersion, []ClassRegistration{row(1, "trusted", "u")}, ErrUnknownDecisionClass},
		{"unknown class in another version", 1, NormalizerVersion, []ClassRegistration{row(2, "trusted", "u")}, ErrUnknownDecisionClass},
		{"ambiguous class", 1, NormalizerVersion, []ClassRegistration{row(1, ClassInitials, "i")}, ErrClassNotSelectable},
		{"malformed class", 1, NormalizerVersion, []ClassRegistration{row(1, ClassMalformed, "m")}, ErrClassNotSelectable},
		{"missing evidence", 1, NormalizerVersion, []ClassRegistration{unproven}, ErrMissingEvidence},
		{"duplicate class", 1, NormalizerVersion, []ClassRegistration{ok, ok}, ErrDuplicateRegistration},
		{"duplicate pair across versions", 2, NormalizerVersion,
			[]ClassRegistration{ok, row(2, ClassStructuredPerson, "again")}, ErrDuplicateRegistration},
		{"missing script", 1, NormalizerVersion, []ClassRegistration{withScript(ok, "")}, ErrUnknownScript},
		{"unknown script", 1, NormalizerVersion, []ClassRegistration{withScript(ok, "Abcd")}, ErrUnknownScript},
		{"lower-case script", 1, NormalizerVersion, []ClassRegistration{withScript(ok, "cyrl")}, ErrUnknownScript},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewAcceptancePolicy(tc.version, tc.normalizer, tc.rows); !errors.Is(err, tc.want) {
				t.Errorf("NewAcceptancePolicy() err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDecideFailsClosed(t *testing.T) {
	r := structuredPersonResult(t)
	registered := ClassRegistration{
		PolicyVersion: 1, DecisionClass: ClassStructuredPerson, Script: ScriptCyrillic, EvidenceSHA256: evidence("s"),
	}

	var zero AcceptancePolicy
	if _, err := zero.Decide(&r); !errors.Is(err, ErrInvalidPolicyVersion) {
		t.Errorf("zero-value policy: err = %v, want ErrInvalidPolicyVersion", err)
	}

	broken := r
	broken.SchemaVersion = 0
	if _, err := mustPolicy(t, 1, registered).Decide(&broken); !errors.Is(err, ErrInvalidSchemaVersion) {
		t.Errorf("invalid result: err = %v, want ErrInvalidSchemaVersion", err)
	}

	foreign, err := NewAcceptancePolicy(1, "authornorm-local-v0", []ClassRegistration{registered})
	if err != nil {
		t.Fatalf("NewAcceptancePolicy: %v", err)
	}
	if _, err := foreign.Decide(&r); !errors.Is(err, ErrNormalizerVersionMismatch) {
		t.Errorf("policy for another normalizer version: err = %v, want ErrNormalizerVersionMismatch", err)
	}
}

// TestStructuralAmbiguityOutranksPlaceholder pins review finding B1: an exact
// placeholder display must not erase the ambiguity of a broken or partial
// structure. Such a credit stays a review proposal under the empty
// production policy and is never selected even when placeholder is
// registered.
func TestStructuralAmbiguityOutranksPlaceholder(t *testing.T) {
	cases := []struct {
		name       string
		components []SourceComponent
		flag       QualityFlag
	}{
		{"duplicate empty first-name child", []SourceComponent{
			{Kind: ComponentFirst, Value: "Автор"},
			{Kind: ComponentFirst, Value: ""},
			{Kind: ComponentLast, Value: "неизвестен"},
		}, FlagDuplicateComponent},
		{"text only in the duplicate child", []SourceComponent{
			{Kind: ComponentFirst, Value: ""},
			{Kind: ComponentFirst, Value: "Аноним"},
		}, FlagDuplicateComponent},
		{"partial structure without a family name", []SourceComponent{
			{Kind: ComponentFirst, Value: "Неизвестный"},
			{Kind: ComponentMiddle, Value: "автор"},
		}, FlagPartialStructure},
		{"unusual characters around the placeholder", []SourceComponent{
			{Kind: ComponentLast, Value: "Аноним (?)"},
		}, FlagUnusualCharacters},
		// "No." is a dot-terminated two-letter initial, and the search key
		// "no name" is an exact placeholder: a real overlap with the current
		// vocabulary.
		{"dotted initial inside an exact placeholder", []SourceComponent{
			{Kind: ComponentLast, Value: "No. Name"},
		}, FlagInitials},
	}
	registered := mustPolicy(t, 2, ClassRegistration{
		PolicyVersion: 2, DecisionClass: ClassPlaceholder, Script: ScriptCyrillic, EvidenceSHA256: evidence("placeholder-v2"),
	})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := mustNormalize(t, mustSource(t, tc.components...))
			if !slices.Contains(r.QualityFlags, tc.flag) || !slices.Contains(r.QualityFlags, FlagPlaceholder) {
				t.Fatalf("flags = %q, want both %q and %q: the fixture no longer overlaps", r.QualityFlags, tc.flag, FlagPlaceholder)
			}
			if !r.DecisionClass.Ambiguous() || r.Status != StatusUnresolved {
				t.Errorf("class/status = %q/%q, want an ambiguous unresolved class", r.DecisionClass, r.Status)
			}
			if d := mustDecide(t, ProductionAcceptancePolicy(), &r); d.Outcome != OutcomeReview {
				t.Errorf("production policy: decision = %q/%q, want review", d.Outcome, d.Reason)
			}
			if d := mustDecide(t, registered, &r); d.Selected() || d.Outcome != OutcomeReview {
				t.Errorf("policy registering placeholder: decision = %q/%q, want review", d.Outcome, d.Reason)
			}
		})
	}
}

// structuralFlagsUnderTest is stated independently of the production list
// on purpose: deriving it from structuralAmbiguityFlags would let a deletion
// there shrink this test too.
var structuralFlagsUnderTest = []QualityFlag{
	"duplicate_component", "mixed_script", "unusual_characters", "initials",
	"particle", "affix", "partial_structure",
}

// TestEveryStructuralFlagIsRejectedAtResultBoundary pins the public half of
// the B1 invariant: a hand-built or stale local result whose class is
// selectable but which carries any structural ambiguity flag is rejected by
// Validate and by Decide, under the empty policy and under a policy that
// registers its class, and is never selected.
func TestEveryStructuralFlagIsRejectedAtResultBoundary(t *testing.T) {
	candidates := map[DecisionClass]Result{
		ClassStructuredPerson: structuredPersonResult(t),
		ClassPlaceholder:      mustNormalize(t, mustSource(t, SourceComponent{Kind: ComponentLast, Value: "Аноним"})),
	}
	for class, candidate := range candidates {
		if candidate.DecisionClass != class || candidate.Validate() != nil {
			t.Fatalf("candidate for %q is %q (Validate %v): fixture drifted", class, candidate.DecisionClass, candidate.Validate())
		}
	}
	for _, flag := range structuralFlagsUnderTest {
		for class, candidate := range candidates {
			t.Run(string(flag)+"/"+string(class), func(t *testing.T) {
				r := candidate
				r.QualityFlags = append(slices.Clone(candidate.QualityFlags), flag)
				if err := r.Validate(); !errors.Is(err, ErrInconsistentResult) {
					t.Errorf("Validate() = %v, want ErrInconsistentResult", err)
				}
				registered := mustPolicy(t, 5, ClassRegistration{
					PolicyVersion: 5, DecisionClass: class, Script: ScriptCyrillic, EvidenceSHA256: evidence(string(class)),
				})
				for name, p := range map[string]AcceptancePolicy{"production": ProductionAcceptancePolicy(), "registered": registered} {
					d, err := p.Decide(&r)
					if !errors.Is(err, ErrInconsistentResult) || d.Selected() {
						t.Errorf("%s policy: Decide() = %+v, %v; want ErrInconsistentResult and no selection", name, d, err)
					}
				}
			})
		}
	}
}

// The same class under two scripts is two registrations, not a duplicate.
func TestOneClassMayBeRegisteredForSeveralScripts(t *testing.T) {
	_, err := NewAcceptancePolicy(3, NormalizerVersion, []ClassRegistration{
		{PolicyVersion: 2, DecisionClass: ClassStructuredPerson, Script: ScriptCyrillic, EvidenceSHA256: evidence("c")},
		{PolicyVersion: 3, DecisionClass: ClassStructuredPerson, Script: ScriptLatin, EvidenceSHA256: evidence("l")},
	})
	if err != nil {
		t.Fatalf("NewAcceptancePolicy: %v", err)
	}
}

// Selectable is stated here independently of the routing table: exactly the
// two non-ambiguous classes of well-formed input may be registered.
func TestSelectableClasses(t *testing.T) {
	want := map[DecisionClass]bool{ClassPlaceholder: true, ClassStructuredPerson: true}
	for _, class := range DecisionClasses() {
		if class.Selectable() != want[class] {
			t.Errorf("%q.Selectable() = %v, want %v", class, class.Selectable(), want[class])
		}
	}
	if DecisionClass("trusted").Selectable() {
		t.Error("an unknown class is selectable")
	}
}

// Scripts is the closed set Validate accepts, in a fixed order: the schema's
// closed check is compared against it.
func TestScriptsAreTheValidatedSet(t *testing.T) {
	scripts := Scripts()
	if !slices.IsSorted(scripts) || len(slices.Compact(slices.Clone(scripts))) != len(scripts) {
		t.Fatalf("Scripts() is not sorted and unique: %q", scripts)
	}
	for _, s := range scripts {
		if err := s.Validate(); err != nil {
			t.Errorf("Scripts() lists %q, which Validate refuses: %v", s, err)
		}
	}
	for _, s := range []Script{ScriptCyrillic, ScriptLatin, ScriptUndetermined, ScriptMixed, "Grek", "Zinh"} {
		if !slices.Contains(scripts, s) {
			t.Errorf("Scripts() misses %q", s)
		}
	}
	if len(scripts) != len(knownScriptCodes)+1 {
		t.Errorf("len(Scripts()) = %d, want every ISO 15924 code (%d) and mixed", len(scripts), len(knownScriptCodes))
	}
}
