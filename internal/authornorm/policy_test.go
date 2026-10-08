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

func TestRegisteredExactPolicyClassIsSelected(t *testing.T) {
	r := structuredPersonResult(t)
	proof := evidence("structured-v2")
	rows := []ClassRegistration{
		{PolicyVersion: 2, DecisionClass: ClassStructuredPerson, EvidenceSHA256: proof},
		{PolicyVersion: 3, DecisionClass: ClassPlaceholder, EvidenceSHA256: evidence("placeholder-v3")},
	}

	d := mustDecide(t, mustPolicy(t, 2, rows...), &r)
	if !d.Selected() || d.Outcome != OutcomeSelected {
		t.Fatalf("registered (2, structured_person): decision = %+v, want selected", d)
	}
	if d.PolicyVersion != 2 || d.EvidenceSHA256 != proof {
		t.Errorf("selection audit = (%d, %x), want (2, %x)", d.PolicyVersion, d.EvidenceSHA256, proof)
	}

	// The same rows under another policy version register a different class:
	// the version-2 row must not leak into version 3.
	other := mustDecide(t, mustPolicy(t, 3, rows...), &r)
	if other.Selected() || other.Reason != ReasonPolicyNotRegistered {
		t.Errorf("policy version 3: decision = %+v, want unresolved/policy_not_registered", other)
	}

	// A version with no rows at all selects nothing either.
	if d := mustDecide(t, mustPolicy(t, 4, rows...), &r); d.Selected() {
		t.Errorf("policy version 4 without rows selected: %+v", d)
	}

	// A registered class does not select a different class.
	placeholder := mustNormalize(t, mustSource(t, SourceComponent{Kind: ComponentLast, Value: "Аноним"}))
	if d := mustDecide(t, mustPolicy(t, 2, rows...), &placeholder); d.Selected() {
		t.Errorf("policy 2 selected unregistered class %q", placeholder.DecisionClass)
	}
	if d := mustDecide(t, mustPolicy(t, 3, rows...), &placeholder); !d.Selected() {
		t.Errorf("policy 3 did not select its registered class %q: %+v", placeholder.DecisionClass, d)
	}
}

func TestPolicyRoutesAmbiguousAndMalformedEvenWhenOtherClassesAreRegistered(t *testing.T) {
	p := mustPolicy(t, 1,
		ClassRegistration{PolicyVersion: 1, DecisionClass: ClassStructuredPerson, EvidenceSHA256: evidence("a")},
		ClassRegistration{PolicyVersion: 1, DecisionClass: ClassPlaceholder, EvidenceSHA256: evidence("b")},
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
	ok := ClassRegistration{PolicyVersion: 1, DecisionClass: ClassStructuredPerson, EvidenceSHA256: evidence("ok")}
	row := func(version int, class DecisionClass, label string) ClassRegistration {
		return ClassRegistration{PolicyVersion: version, DecisionClass: class, EvidenceSHA256: evidence(label)}
	}
	unproven := ClassRegistration{PolicyVersion: 1, DecisionClass: ClassPlaceholder}
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
	registered := ClassRegistration{PolicyVersion: 1, DecisionClass: ClassStructuredPerson, EvidenceSHA256: evidence("s")}

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
		PolicyVersion: 2, DecisionClass: ClassPlaceholder, EvidenceSHA256: evidence("placeholder-v2"),
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
					PolicyVersion: 5, DecisionClass: class, EvidenceSHA256: evidence(string(class)),
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
