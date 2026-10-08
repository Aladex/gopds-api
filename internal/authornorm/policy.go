package authornorm

import (
	"errors"
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
	// immutable eval report that justified it.
	ErrMissingEvidence = errors.New("authornorm: class registration has no evidence report hash")
	// ErrDuplicateRegistration marks two rows for the same class in one
	// policy version.
	ErrDuplicateRegistration = errors.New("authornorm: decision class registered twice in one policy version")
	// ErrNormalizerVersionMismatch marks a result produced by a different
	// normalizer version than the one the policy was evaluated for.
	ErrNormalizerVersionMismatch = errors.New("authornorm: result normalizer version differs from the policy")
)

// productionPolicyVersion is the version of the policy shipped with this
// normalizer. It registers nothing (scope A2): registration is a later,
// separate step backed by a deterministic eval report.
const productionPolicyVersion = 1

// ClassRegistration is one row of the acceptance-class table: decision class
// DecisionClass passed eval for policy PolicyVersion, as recorded by the
// immutable report whose SHA-256 is EvidenceSHA256.
type ClassRegistration struct {
	PolicyVersion  int
	DecisionClass  DecisionClass
	EvidenceSHA256 [32]byte
}

// AcceptancePolicy decides whether a local result is selected automatically.
// It holds only the registrations of its own version; a zero value is not a
// policy and fails every decision.
type AcceptancePolicy struct {
	version           int
	normalizerVersion string
	registered        map[DecisionClass][32]byte
}

// NewAcceptancePolicy builds the policy of one exact version, evaluated for
// one exact normalizer version, from all registration rows. Every row is
// validated; only rows of the requested version register a class.
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
		registered:        map[DecisionClass][32]byte{},
	}
	for _, row := range rows {
		if err := row.validate(); err != nil {
			return AcceptancePolicy{}, err
		}
		if row.PolicyVersion != version {
			continue
		}
		if _, dup := p.registered[row.DecisionClass]; dup {
			return AcceptancePolicy{}, ErrDuplicateRegistration
		}
		p.registered[row.DecisionClass] = row.EvidenceSHA256
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
	if !row.DecisionClass.selectable() {
		return ErrClassNotSelectable
	}
	if row.EvidenceSHA256 == ([32]byte{}) {
		return ErrMissingEvidence
	}
	return nil
}

// ProductionAcceptancePolicy is the policy this normalizer ships with: a
// positive version and no registered class, so nothing is autoaccepted.
func ProductionAcceptancePolicy() AcceptancePolicy {
	return AcceptancePolicy{
		version:           productionPolicyVersion,
		normalizerVersion: NormalizerVersion,
		registered:        map[DecisionClass][32]byte{},
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
// exact policy version registers it. Self-reported confidence does not exist
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
	evidence, ok := p.registered[r.DecisionClass]
	if !ok {
		return Decision{Outcome: OutcomeUnresolved, Reason: ReasonPolicyNotRegistered, PolicyVersion: p.version}, nil
	}
	return Decision{Outcome: OutcomeSelected, PolicyVersion: p.version, EvidenceSHA256: evidence}, nil
}
