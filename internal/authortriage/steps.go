package authortriage

import "gopds-api/internal/authorentity"

// RuleVersion names the rule set new batches are planned under. A batch is
// planned, stored and applied under one version, and an undone item is
// remembered per version.
const RuleVersion = RuleVersionV2

// The rule versions. triage-v1 stays for the history of the batches planned
// under it (DecideV1); triage-v2 is triage-v1 without steps 4 and 8, with
// guards after steps 1, 2, 5, 6 and 7 (guards.go).
const (
	RuleVersionV1 = "triage-v1"
	RuleVersionV2 = "triage-v2"
)

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

// Refusal is why a guard of triage-v2 sent an item a step holds for to the
// people instead: the item is left, and the plan keeps the reason so that
// what is left can be grouped by it.
type Refusal string

const (
	// RefusedLostToken: a word of the proposal's display is missing from
	// the answer's display: not moved to the prefix, not a role or clerical
	// title, and not a one-word nickname beside a full display.
	RefusedLostToken Refusal = "lost_token"
	// RefusedUnparsedWord: a word of a person's display or sort that none
	// of the name parts holds («Kelley St. John» with the family «John»).
	RefusedUnparsedWord Refusal = "display_word_not_in_parts"
	// RefusedRoleAsName: a role or clerical title kept as a name part, or a
	// prefix in capitals; a person's role inside a collective's name.
	RefusedRoleAsName Refusal = "role_as_name"
	// RefusedGenreList: a list of genres or a kind of book («Эпосы, мифы,
	// легенды и сказания», «[ сборник ]») is no collective.
	RefusedGenreList Refusal = "genre_list"
	// RefusedPlaceholderTitle: a placeholder phrase with more after it
	// («Автор неизвестен -- Эротика и секс»).
	RefusedPlaceholderTitle Refusal = "placeholder_with_title"
	// RefusedOnePersonTitles: one person with titles joined by «и» («Давид
	// Царь и Пророк»).
	RefusedOnePersonTitles Refusal = "one_person_titles"
	// RefusedTitleShape: a pair whose words are not names («Заек и лъв»):
	// the title of a tale, not two people.
	RefusedTitleShape Refusal = "title_shape"
	// RefusedListedName: a handle on the list of organizations, magazines
	// and titles («Компьютерра», «РПЦ», «Bībele»).
	RefusedListedName Refusal = "listed_name"
	// RefusedShortCaps: a handle of at most five capital letters, an
	// abbreviation more often than a person.
	RefusedShortCaps Refusal = "short_caps"
	// RefusedHeavy: a handle on so many credits that a person decides it.
	RefusedHeavy Refusal = "heavy"
)

// Fixes a guard makes to a stored answer, named in the evidence.
const (
	FixLookalike   = "lookalike"
	FixPunctuation = "punctuation"
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
	// The production pair's group that confirmed the canonical (step 1
	// only): with the judges of PairRunID, the four-model proof.
	PairRunID     int64  `json:"pair_run_id,omitempty"`
	PairArm       string `json:"pair_arm,omitempty"`
	PairBatchSize int    `json:"pair_batch_size,omitempty"`
	// Fixes are the guards' fixes to the stored answer (triage-v2).
	Fixes []string `json:"fixes,omitempty"`
	// RefusedStep and Refusal are the step that held and the guard that
	// refused it, on a left item (triage-v2).
	RefusedStep Step    `json:"refused_step,omitempty"`
	Refusal     Refusal `json:"refusal,omitempty"`
}

// Decision is what Decide made of an item: a step, its rule, the action and
// the name after it; or the exclusion; or nothing (Step 0: left). A left
// item a guard refused carries the refusal, and the refused step's evidence
// with Evidence.RefusedStep and Evidence.Refusal set.
type Decision struct {
	Step     Step
	Rule     string
	Action   Action
	After    Name
	Evidence Evidence
	Excluded Exclusion
	Refusal  Refusal
}
