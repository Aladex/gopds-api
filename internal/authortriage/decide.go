package authortriage

import (
	"slices"
	"strings"

	"gopds-api/internal/authorentity"
)

// Review item facts the exclusions read.
const (
	reasonAmbiguous          = "ambiguous_decision"
	classDuplicateComponent  = "duplicate_component"
	classCollectiveCandidate = "collective_candidate"
	classMononym             = "mononym"
	entityRulePair           = "pair"
	entityRuleListed         = "listed"
)

// step is one step's predicate: the name after it, the evidence, and whether
// it holds.
type step func(c *Candidate, env *Env, cons *Consensus) (Name, Evidence, bool)

var cascade = map[Step]step{
	StepFourModel:         fromConsensus(true),
	StepJudgesOnly:        fromConsensus(false),
	StepRules:             byRule,
	StepSameName:          bySameName,
	StepPlaceholderOrg:    byPlaceholderOrOrganization,
	StepAsNamedCollective: asNamedCollective,
	StepKeepHandle:        keepHandle,
	StepParticleOnly:      particleOnly,
}

// Decide decides one item: the exclusions first, in their order, then the
// first step of the cascade that holds and gives a name of a kind a batch may
// write with a display. No step: Step 0, the item is left.
func Decide(c *Candidate, env *Env) Decision {
	if ex := exclusion(c); ex != "" {
		return Decision{Excluded: ex}
	}
	if env == nil {
		env = &Env{}
	}
	cons := JudgesConsensus(c.Answers)
	for _, s := range Steps() {
		after, ev, ok := cascade[s](c, env, &cons)
		if !ok || after.Display == "" || !decidable(after.Kind) {
			continue
		}
		rule := ev.Rule
		if rule == "" {
			rule = s.Name()
		}
		return Decision{Step: s, Rule: rule, Action: ActionFor(&c.Proposal, &after), After: after, Evidence: ev}
	}
	return Decision{}
}

func exclusion(c *Candidate) Exclusion {
	switch {
	case c.Reason != reasonAmbiguous:
		return ExcludedNotAmbiguous
	case c.CreditScoped:
		return ExcludedCreditScoped
	case !c.HasProposal:
		return ExcludedNoProposal
	case c.Class == classDuplicateComponent:
		return ExcludedDuplicateComponent
	case c.Pinned:
		return ExcludedPinned
	case c.UndoneBefore:
		return ExcludedUndoneBefore
	}
	return ""
}

// decidable reports a kind a batch writes: never malformed, never anything
// the assembler does not give.
func decidable(kind string) bool {
	return kind == KindPerson || kind == KindCollective || kind == KindUnknown
}

// ActionFor is how the review service turns proposal into after. To a
// person: all nine fields equal is an accept, only the kind differing a
// classify, anything else an edit. To a collective or unknown, whose
// assembled name has no parts: the display and the kind decide — the same
// display is an accept or a classify (the service keeps the proposal's parts
// with the new kind, as a human Classify does), another display an edit.
func ActionFor(proposal, after *Name) Action {
	if after.Kind == KindPerson {
		kindless := *proposal
		kindless.Kind = after.Kind
		switch {
		case *proposal == *after:
			return ActionAccept
		case kindless == *after:
			return ActionClassify
		}
		return ActionEdit
	}
	switch {
	case proposal.Display != after.Display:
		return ActionEdit
	case proposal.Kind == after.Kind:
		return ActionAccept
	}
	return ActionClassify
}

// fromConsensus is step 1 (fourModel) or step 2: the judges agree on one
// answer that still assembles. The evidence is the lowest agreeing judges'
// group, and for step 1 also the group of the pair that confirmed it.
func fromConsensus(fourModel bool) step {
	return func(_ *Candidate, _ *Env, cons *Consensus) (Name, Evidence, bool) {
		if cons.Conflict || cons.Agreed == nil || cons.Agreed.Name == nil || cons.FourModel != fourModel {
			return Name{}, Evidence{}, false
		}
		a := cons.Agreed
		ev := Evidence{RunID: a.RunID, Arm: a.Arm, BatchSize: a.BatchSize, Canonical: a.Canonical, Form: a.Form}
		if p := cons.Pair; p != nil {
			ev.PairRunID, ev.PairArm, ev.PairBatchSize = p.RunID, p.Arm, p.BatchSize
		}
		return *a.Name, ev, true
	}
}

// byRule is step 3: the build's case, look-alike or nickname read of a
// proposal with no prefix.
func byRule(c *Candidate, env *Env, _ *Consensus) (Name, Evidence, bool) {
	if c.Proposal.Prefix != "" || c.Source == nil {
		return Name{}, Evidence{}, false
	}
	res, rule, ok := authorentity.ReadReviewRule(c.Source, env.Given)
	if !ok {
		return Name{}, Evidence{}, false
	}
	return Name{Given: res.Given, Additional: strings.Join(res.Additional, " "), Family: res.Family,
		Nickname: res.Nickname, Suffix: res.Suffix, Display: res.DisplayName, Sort: res.SortName,
		Kind: KindPerson}, Evidence{Rule: nameRules + ":" + rule}, true
}

// bySameName is step 4: the one person structure of the entity the build
// joined a person proposal to by same name.
func bySameName(c *Candidate, _ *Env, _ *Consensus) (Name, Evidence, bool) {
	if c.EntityRule != nameSameName || c.Proposal.Kind != KindPerson || len(c.SameName) != 1 ||
		c.SameName[0].Kind != KindPerson {
		return Name{}, Evidence{}, false
	}
	return c.SameName[0], Evidence{Rule: nameSameName}, true
}

// byPlaceholderOrOrganization is step 5: a placeholder phrase is unknown, an
// organization's name or a collective phrase a collective. A collective
// candidate the build reads as a pair or a list is several people, not one
// collective.
func byPlaceholderOrOrganization(c *Candidate, _ *Env, _ *Consensus) (Name, Evidence, bool) {
	if c.Class == classCollectiveCandidate && (c.EntityRule == entityRulePair || c.EntityRule == entityRuleListed) {
		return Name{}, Evidence{}, false
	}
	after := c.Proposal
	switch {
	case PlaceholderPhrase(c.Proposal.Display):
		after.Kind = KindUnknown
		return after, Evidence{Rule: rulePlaceholder}, true
	case collectiveName(c.Proposal.Display):
		after.Kind = KindCollective
		return after, Evidence{Rule: ruleOrganization}, true
	}
	return Name{}, Evidence{}, false
}

// asNamedCollective is step 6: a collective candidate the build reads as
// named is a collective of that name.
func asNamedCollective(c *Candidate, _ *Env, _ *Consensus) (Name, Evidence, bool) {
	if c.Class != classCollectiveCandidate || c.EntityRule != ruleAsNamed {
		return Name{}, Evidence{}, false
	}
	after := c.Proposal
	after.Kind = KindCollective
	return after, Evidence{Rule: ruleAsNamed}, true
}

// keepHandle is step 7: a one-word person proposal of the mononym class, no
// namesake tag, no given name the catalog knows, no placeholder or
// organization, and no judge who called it anything but a person.
func keepHandle(c *Candidate, env *Env, _ *Consensus) (Name, Evidence, bool) {
	p := c.Proposal
	if c.Class != classMononym || p.Kind != KindPerson || len(strings.Fields(p.Display)) != 1 ||
		c.Source == nil || c.Source.Tag != 0 || env.Given.Known(p.Display) ||
		PlaceholderPhrase(p.Display) || collectiveName(p.Display) {
		return Name{}, Evidence{}, false
	}
	if slices.ContainsFunc(c.Answers, func(a Answer) bool {
		return (a.Slot == SlotJudgeA || a.Slot == SlotJudgeB) && !a.Refused && a.Form != KindPerson
	}) {
		return Name{}, Evidence{}, false
	}
	return p, Evidence{Rule: nameKeepHandle}, true
}

// particleOnly is step 8: a proposal whose only doubt is a name particle,
// accepted as it is.
func particleOnly(c *Candidate, _ *Env, _ *Consensus) (Name, Evidence, bool) {
	if c.Source == nil || c.Source.Result == nil || !c.Source.ParticleOnly() {
		return Name{}, Evidence{}, false
	}
	return c.Proposal, Evidence{Rule: nameParticleOnly}, true
}
