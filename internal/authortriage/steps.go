package authortriage

import "gopds-api/internal/authorentity"

// RuleVersion names this rule set. A batch is planned under it, and an
// undone item is remembered per version.
const RuleVersion = "triage-v1"

// Step is one step of the cascade, 1..8 in order.
type Step int

const (
	StepFourModel         Step = iota + 1 // four_model
	StepJudgesOnly                        // judges_only
	StepRules                             // rules
	StepSameName                          // same_name
	StepPlaceholderOrg                    // placeholder_org
	StepAsNamedCollective                 // as_named_collective
	StepKeepHandle                        // keep_handle
	StepParticleOnly                      // particle_only
)

// Names of the steps, and of the rules a decision's evidence cites. same_name
// and as_named are also the entity build's rules a step reads.
const (
	nameFourModel         = "four_model"
	nameJudgesOnly        = "judges_only"
	nameRules             = "rules"
	nameSameName          = "same_name"
	namePlaceholderOrg    = "placeholder_org"
	nameAsNamedCollective = "as_named_collective"
	nameKeepHandle        = "keep_handle"
	nameParticleOnly      = "particle_only"
	ruleAsNamed           = "as_named"
	rulePlaceholder       = "placeholder"
	ruleOrganization      = "organization"
)

var stepNames = map[Step]string{
	StepFourModel:         nameFourModel,
	StepJudgesOnly:        nameJudgesOnly,
	StepRules:             nameRules,
	StepSameName:          nameSameName,
	StepPlaceholderOrg:    namePlaceholderOrg,
	StepAsNamedCollective: nameAsNamedCollective,
	StepKeepHandle:        nameKeepHandle,
	StepParticleOnly:      nameParticleOnly,
}

// Steps lists every step in cascade order.
func Steps() []Step {
	return []Step{StepFourModel, StepJudgesOnly, StepRules, StepSameName, StepPlaceholderOrg,
		StepAsNamedCollective, StepKeepHandle, StepParticleOnly}
}

// Name is the step's name; empty for no step.
func (s Step) Name() string { return stepNames[s] }

// Action is how a step decides an item: the database.ReviewAction values of
// the three decisions a batch may take.
type Action string

const (
	ActionAccept   Action = "accepted"
	ActionEdit     Action = "edited"
	ActionClassify Action = "classified"
)

// Exclusion is why an item is kept out of every step, checked in this order.
type Exclusion string

const (
	ExcludedNotAmbiguous       Exclusion = "not_ambiguous"
	ExcludedCreditScoped       Exclusion = "credit_scoped"
	ExcludedNoProposal         Exclusion = "no_proposal"
	ExcludedDuplicateComponent Exclusion = "duplicate_component"
	ExcludedPinned             Exclusion = "pinned"
	ExcludedUndoneBefore       Exclusion = "undone_before"
)

// Name is a decided name; Kind is person, collective or unknown (the
// models.NormalizationKind values). Additional is the additional names
// joined with spaces.
type Name struct {
	Given      string `json:"given"`
	Additional string `json:"additional"`
	Family     string `json:"family"`
	Nickname   string `json:"nickname"`
	Prefix     string `json:"prefix"`
	Suffix     string `json:"suffix"`
	Display    string `json:"display"`
	Sort       string `json:"sort"`
	Kind       string `json:"kind"`
}

// Kinds of a decided name.
const (
	KindPerson     = "person"
	KindCollective = "collective"
	KindUnknown    = "unknown"
)

// Answer is one stored model answer about the item's fingerprint.
type Answer struct {
	RunID     int64
	Arm       string
	BatchSize int
	// Slot is production_a, production_b, judge_a or judge_b.
	Slot string
	// Canonical is the hex of the answer's canonical_sha256.
	Canonical string
	Refused   bool
	Form      string
	// Name is the assembled answer; nil when it was refused or when the
	// stored reply no longer assembles.
	Name *Name
}

// Answer slots.
const (
	SlotProductionA = "production_a"
	SlotProductionB = "production_b"
	SlotJudgeA      = "judge_a"
	SlotJudgeB      = "judge_b"
)

// Candidate is one open review item with the facts the rules read.
type Candidate struct {
	ItemID       int64
	Class        string
	Reason       string
	CreditScoped bool
	HasProposal  bool
	Credits      int
	Proposal     Name
	// Source is the unselected credit the build reads for the fingerprint:
	// nil when no person credit of it is under review.
	Source *authorentity.Unselected
	// Answers are the stored model answers about the fingerprint.
	Answers []Answer
	// EntityRule is the build's rule for the fingerprint's entity
	// assignment (same_name, as_named, pair, listed, ...).
	EntityRule string
	// SameName are the distinct person structures of the entity the build
	// joined the name to by same name.
	SameName     []Name
	Pinned       bool
	UndoneBefore bool
}

// Env is what the rules read of the whole catalog.
type Env struct {
	Given authorentity.GivenNames
}

// Evidence is what a decision rests on, stored with it.
type Evidence struct {
	RunID     int64  `json:"run_id,omitempty"`
	Arm       string `json:"arm,omitempty"`
	BatchSize int    `json:"batch_size,omitempty"`
	Canonical string `json:"canonical,omitempty"`
	Form      string `json:"form,omitempty"`
	Rule      string `json:"rule,omitempty"`
}

// Decision is what Decide made of an item: a step, its rule, the action and
// the name after it; or the exclusion; or nothing (Step 0: left).
type Decision struct {
	Step     Step
	Rule     string
	Action   Action
	After    Name
	Evidence Evidence
	Excluded Exclusion
}
