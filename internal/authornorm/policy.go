package authornorm

import (
	"errors"
	"slices"
	"strings"
)

var (
	// ErrInvalidPolicyVersion marks a policy, or a registration row, whose
	// version is zero or negative, and a zero-value policy used to decide.
	ErrInvalidPolicyVersion = errors.New("authornorm: acceptance policy version must be positive")
	// ErrClassNotSelectable marks a registration of an ambiguous or malformed
	// decision class; such classes are routed by scope A3 and never selected.
	ErrClassNotSelectable = errors.New("authornorm: decision class cannot be registered for automatic selection")
	// ErrMissingEvidence marks a registration without the hash of the
	// frozen evidence report that justified it.
	ErrMissingEvidence = errors.New("authornorm: class registration has no evidence report hash")
	// ErrDuplicateRegistration marks two rows for the same (decision class,
	// script) within the versions one policy is built from.
	ErrDuplicateRegistration = errors.New("authornorm: decision class and script registered twice")
	// ErrNormalizerVersionMismatch marks a result produced by a different
	// normalizer version than the one the policy was evaluated for.
	ErrNormalizerVersionMismatch = errors.New("authornorm: result normalizer version differs from the policy")
)

// BasePolicyVersion is the version of the empty policy. Registrations ship
// with the code as migrations, each change adding its (decision class,
// script) pairs under the next version; migration 25 registers
// structured_person in Cyrillic and Latin script as version 2.
const BasePolicyVersion = 1

// ClassRegistration is one row of the acceptance-class table: results of
// decision class DecisionClass in script Script are selected from policy
// version PolicyVersion on, as justified by the immutable evidence whose
// SHA-256 is EvidenceSHA256.
type ClassRegistration struct {
	PolicyVersion  int
	DecisionClass  DecisionClass
	Script         Script
	EvidenceSHA256 [32]byte
}

// acceptancePair is what a registration is keyed by.
type acceptancePair struct {
	class  DecisionClass
	script Script
}

// AcceptancePolicy decides whether a local result is selected automatically.
// Versions are cumulative: the policy of version N holds every pair
// registered in versions 1..N, so a registration never changes what an
// earlier version selected. A zero value is not a policy and fails every
// decision.
type AcceptancePolicy struct {
	version           int
	normalizerVersion string
	registered        map[acceptancePair][32]byte
}

// NewAcceptancePolicy builds the policy of one exact version, evaluated for
// one exact normalizer version, from all registration rows. Every row is
// validated; the rows of this version and of every earlier one register
// their pair.
func NewAcceptancePolicy(version int, normalizerVersion string, rows []ClassRegistration) (AcceptancePolicy, error) {
	if version <= 0 {
		return AcceptancePolicy{}, ErrInvalidPolicyVersion
	}
	if strings.TrimSpace(normalizerVersion) == "" {
		return AcceptancePolicy{}, ErrEmptyVersion
	}
	p := AcceptancePolicy{
		version:           version,
		normalizerVersion: normalizerVersion,
		registered:        map[acceptancePair][32]byte{},
	}
	for _, row := range rows {
		if err := row.validate(); err != nil {
			return AcceptancePolicy{}, err
		}
		if row.PolicyVersion > version {
			continue
		}
		pair := acceptancePair{class: row.DecisionClass, script: row.Script}
		if _, dup := p.registered[pair]; dup {
			return AcceptancePolicy{}, ErrDuplicateRegistration
		}
		p.registered[pair] = row.EvidenceSHA256
	}
	return p, nil
}

func (row ClassRegistration) validate() error {
	if row.PolicyVersion <= 0 {
		return ErrInvalidPolicyVersion
	}
	if err := row.DecisionClass.Validate(); err != nil {
		return err
	}
	if !row.DecisionClass.Selectable() {
		return ErrClassNotSelectable
	}
	if err := row.Script.Validate(); err != nil {
		return err
	}
	if row.EvidenceSHA256 == ([32]byte{}) {
		return ErrMissingEvidence
	}
	return nil
}

// ProductionAcceptancePolicy is the empty base policy: a positive version and
// no registered class, so nothing is autoaccepted. The registrations in force
// live in the class table and are loaded from there.
func ProductionAcceptancePolicy() AcceptancePolicy {
	return AcceptancePolicy{
		version:           BasePolicyVersion,
		normalizerVersion: NormalizerVersion,
		registered:        map[acceptancePair][32]byte{},
	}
}

// Version returns the policy version.
func (p AcceptancePolicy) Version() int { return p.version }

// Empty reports whether the policy registers no class.
func (p AcceptancePolicy) Empty() bool { return len(p.registered) == 0 }

// Outcome is where a credit's candidate goes under scope A3.
type Outcome string

// Closed set of outcomes.
const (
	// OutcomeSelected: registered class, selected automatically with audit.
	OutcomeSelected Outcome = "selected"
	// OutcomeReview: ambiguous class, stored as a proposal plus a review item.
	OutcomeReview Outcome = "review"
	// OutcomeUnresolved: non-ambiguous class the policy does not register,
	// stored as a proposal; the credit is unresolved with a closed reason.
	OutcomeUnresolved Outcome = "unresolved"
	// OutcomeInvalid: malformed input, the credit is accounted as invalid.
	OutcomeInvalid Outcome = "invalid"
)

// UnresolvedReason is the closed reason of OutcomeUnresolved.
type UnresolvedReason string

// ReasonPolicyNotRegistered: the class is eligible but not registered.
const ReasonPolicyNotRegistered UnresolvedReason = "policy_not_registered"

// Decision is the policy verdict for one result. For a selection it carries
// the policy version and the evidence hash the audit must record.
type Decision struct {
	Outcome        Outcome
	Reason         UnresolvedReason
	PolicyVersion  int
	EvidenceSHA256 [32]byte
}

// Selected reports an automatic selection.
func (d Decision) Selected() bool { return d.Outcome == OutcomeSelected }

// Decide routes a valid result by its decision class: malformed is invalid,
// ambiguous goes to review, and an eligible class is selected only when this
// policy version registers it for the result's script. Self-reported confidence does not exist
// here; a valid candidate alone is never enough.
func (p AcceptancePolicy) Decide(r *Result) (Decision, error) {
	if p.version <= 0 {
		return Decision{}, ErrInvalidPolicyVersion
	}
	if err := r.Validate(); err != nil {
		return Decision{}, err
	}
	if r.NormalizerVersion != p.normalizerVersion {
		return Decision{}, ErrNormalizerVersionMismatch
	}
	switch {
	case r.Kind == KindMalformed || r.DecisionClass.Kind() == KindMalformed:
		return Decision{Outcome: OutcomeInvalid, PolicyVersion: p.version}, nil
	case r.DecisionClass.Ambiguous():
		return Decision{Outcome: OutcomeReview, PolicyVersion: p.version}, nil
	}
	evidence, ok := p.registered[acceptancePair{class: r.DecisionClass, script: r.Script}]
	if !ok {
		return Decision{Outcome: OutcomeUnresolved, Reason: ReasonPolicyNotRegistered, PolicyVersion: p.version}, nil
	}
	return Decision{Outcome: OutcomeSelected, PolicyVersion: p.version, EvidenceSHA256: evidence}, nil
}

// Scripts returns every script a result may carry — each ISO 15924 code of a
// Unicode script and ScriptMixed — sorted. It is the closed set the schema
// checks a registered script against.
func Scripts() []Script {
	scripts := make([]Script, 0, len(knownScriptCodes)+1)
	for code := range knownScriptCodes {
		scripts = append(scripts, code)
	}
	scripts = append(scripts, ScriptMixed)
	slices.Sort(scripts)
	return scripts
}
