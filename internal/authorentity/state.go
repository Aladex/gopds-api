package authorentity

import (
	"github.com/google/uuid"
)

// Result is a selected result as the build reads it. The credits of one
// result share it: the catalog has four credits per result on average.
type Result struct {
	ResultID int64
	// Kind is the result's kind: person or collective.
	Kind        string
	Script      string
	Family      string
	Given       string
	Additional  []string
	Suffix      string
	Nickname    string
	DisplayName string
	SortName    string
}

// Credit is one selected author credit of a current snapshot with its
// selected result, as the build reads it.
type Credit struct {
	ID               int64
	BookID           int64
	Fingerprint      string
	ExtractorVersion string
	// Tag is the namesake tag "(N)" of the credit's source fields; 0 when
	// there is none.
	Tag int
	*Result
}

// Book is what the evidence reads of a book.
type Book struct {
	Title  string
	Series []int64
}

// Input is everything one pass reads.
type Input struct {
	Credits []Credit
	Books   map[int64]Book
	// Legacy is the legacy author each credit's read-model row is matched
	// to (book_author_display.legacy_author_id), for the comparison report.
	Legacy map[int64]int64
	// Old is what the previous pass and the administrator stored; nil is
	// an empty store.
	Old *State
	// NewID makes the ID of a new entity; nil is uuid.NewV7.
	NewID func() uuid.UUID
	// Reference is the Wikidata evidence (E4w); nil is none.
	Reference *Reference
	// Decisions are the live merge decisions (E4b): same ones and revokes.
	Decisions []MergeDecision
}

// Methods of the stored rows (§3).
const (
	MethodDeterministic = "deterministic"
	MethodEvidence      = "evidence"
	MethodModel         = "model"
	MethodManual        = "manual"
)

// Rules of the automatic rows.
const (
	RuleD1 = "D1"
	RuleD2 = "D2"
	// RuleD2b is a name the source split into other fields: the same words
	// in the same order (E4·D2b).
	RuleD2b  = "D2b"
	RuleTypo = "typo"
	// ruleD3 prefixes the evidence kinds of a book moved out of a bucket:
	// "D3:title", "D3:series+coauthor".
	ruleD3 = "D3:"
)

// The evidence kinds of D3 and the typo merge.
const (
	EvidenceTitle    = "title"
	EvidenceSeries   = "series"
	EvidenceCoauthor = "coauthor"
)

// Kinds of an entity.
const (
	KindPerson     = "person"
	KindCollective = "collective"
)

// FPKey names a fingerprint assignment (§3): the fingerprint, its extractor
// version and the person's ordinal in the credit.
type FPKey struct {
	Fingerprint      string
	ExtractorVersion string
	Ordinal          int
}

// CreditKey names a credit assignment.
type CreditKey struct {
	CreditID int64
	Ordinal  int
}

// EntityRow is a stored author_entity row.
type EntityRow struct {
	ID          uuid.UUID
	Kind        string
	DisplayName string
	SortName    string
	SearchKey   string
	NamePinned  bool
	MergedInto  *uuid.UUID
}

func (e *EntityRow) sameAs(o *EntityRow) bool {
	return e.Kind == o.Kind && e.DisplayName == o.DisplayName && e.SortName == o.SortName &&
		e.SearchKey == o.SearchKey && e.NamePinned == o.NamePinned && equalID(e.MergedInto, o.MergedInto)
}

func equalID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// AssignmentRow is a stored author_entity_assignment row.
type AssignmentRow struct {
	Key      FPKey
	EntityID uuid.UUID
	Method   string
	Rule     string
}

// CreditRow is a stored author_entity_credit_assignment row; a nil EntityID
// is the decision "no entity".
type CreditRow struct {
	Key      CreditKey
	EntityID *uuid.UUID
	Method   string
	Rule     string
}

// MergeRow is a logged merge.
type MergeRow struct {
	From, To uuid.UUID
	Method   string
	// Rule is the evidence's rule of a merge on evidence: the Wikidata
	// item and extract; empty for a deterministic merge.
	Rule string
	// SharedBooks is how many books of the absorbed entity the survivor
	// took.
	SharedBooks int
	// DecisionIDs are a model merge's decision path (§5.1), ReportID and
	// Stratum those of its first decision; Members and MemberCredits what
	// the absorbed entity held: the input of its revival (§5.2).
	DecisionIDs   []int64
	ReportID      int64
	Stratum       string
	Members       []FPKey
	MemberCredits []CreditKey
	// Repoint is the new absorption of an entity merged already: one a
	// revival could not give its ID back to.
	Repoint bool
}

// SourceWikidata is the source of an entity's Wikidata item.
const SourceWikidata = "wikidata"

// RefKey names an entity's reference to one source.
type RefKey struct {
	EntityID uuid.UUID
	Source   string
}

// ReferenceRow is a stored author_entity_reference row: the entity's item in
// a source, the extract it was read from, and whether an administrator
// pinned it (the build never touches a pinned row).
type ReferenceRow struct {
	Key     RefKey
	Ref     string
	Extract string
	Pinned  bool
}

// State is what is stored: the rows of the entity tables.
type State struct {
	Entities     map[uuid.UUID]EntityRow
	Fingerprints map[FPKey]AssignmentRow
	Credits      map[CreditKey]CreditRow
	References   map[RefKey]ReferenceRow
	Merges       []MergeRow
	// ModelMerges are the owning generations of the merged entities whose
	// newest merge is a model union, by the absorbed entity.
	ModelMerges map[uuid.UUID]ModelMerge
}

// NewState is an empty store.
func NewState() *State {
	return &State{Entities: map[uuid.UUID]EntityRow{}, Fingerprints: map[FPKey]AssignmentRow{},
		Credits: map[CreditKey]CreditRow{}, References: map[RefKey]ReferenceRow{}, ModelMerges: map[uuid.UUID]ModelMerge{}}
}

// Unit is the writes of one entity, applied together or not at all: the
// entity row, the entities it absorbs and its assignments. The entity's
// names are the read-model worker's (migration 57). A unit the database
// refuses is skipped with the units it depends on, its component
// (Plan.Components), and nothing else (§1.5).
type Unit struct {
	EntityID uuid.UUID
	// Entity is the row to insert or update; nil when it is stored as is.
	Entity *EntityRow
	// Absorb are the old entities this one takes over: each gets
	// merged_into and is logged.
	Absorb             []MergeRow
	Fingerprints       []AssignmentRow
	DeleteFingerprints []FPKey
	Credits            []CreditRow
	DeleteCredits      []CreditKey
	// References are the entity's references to write; DeleteReferences
	// the ones it, or an entity it absorbs, no longer has.
	References       []ReferenceRow
	DeleteReferences []RefKey
}

// Changes counts the rows the unit writes.
func (u *Unit) Changes() int {
	n := len(u.Absorb) + len(u.Fingerprints) + len(u.DeleteFingerprints) + len(u.Credits) + len(u.DeleteCredits) +
		len(u.References) + len(u.DeleteReferences)
	if u.Entity != nil {
		n++
	}
	return n
}

// Apply writes the unit into an in-memory store the way the database layer
// writes it: manual rows are never replaced or removed.
func (u *Unit) Apply(s *State) {
	if s.ModelMerges == nil {
		s.ModelMerges = map[uuid.UUID]ModelMerge{}
	}
	if u.Entity != nil {
		// A written entity row is live: a revived one is merged no more.
		s.Entities[u.EntityID] = *u.Entity
		delete(s.ModelMerges, u.EntityID)
	}
	for _, m := range u.Absorb {
		row, ok := s.Entities[m.From]
		if !ok || (row.MergedInto != nil && !m.Repoint) {
			continue
		}
		to := m.To
		row.MergedInto = &to
		s.Entities[m.From] = row
		s.Merges = append(s.Merges, m)
		delete(s.ModelMerges, m.From)
		if m.Method == MethodModel {
			s.ModelMerges[m.From] = ModelMerge{LogID: int64(len(s.Merges)), Entity: m.From, Into: m.To,
				DecisionIDs: m.DecisionIDs, Members: m.Members, Credits: m.MemberCredits}
		}
	}
	u.applyFingerprints(s)
	u.applyCredits(s)
	u.applyReferences(s)
}

func (u *Unit) applyReferences(s *State) {
	if s.References == nil {
		s.References = map[RefKey]ReferenceRow{}
	}
	for _, r := range u.References {
		if old, ok := s.References[r.Key]; ok && old.Pinned {
			continue
		}
		s.References[r.Key] = r
	}
	for _, k := range u.DeleteReferences {
		if old, ok := s.References[k]; ok && !old.Pinned {
			delete(s.References, k)
		}
	}
}

func (u *Unit) applyFingerprints(s *State) {
	for _, a := range u.Fingerprints {
		if old, ok := s.Fingerprints[a.Key]; ok && old.Method == MethodManual {
			continue
		}
		s.Fingerprints[a.Key] = a
	}
	for _, k := range u.DeleteFingerprints {
		if old, ok := s.Fingerprints[k]; ok && old.Method != MethodManual {
			delete(s.Fingerprints, k)
		}
	}
}

func (u *Unit) applyCredits(s *State) {
	for _, c := range u.Credits {
		if old, ok := s.Credits[c.Key]; ok && old.Method == MethodManual {
			continue
		}
		s.Credits[c.Key] = c
	}
	for _, k := range u.DeleteCredits {
		if old, ok := s.Credits[k]; ok && old.Method != MethodManual {
			delete(s.Credits, k)
		}
	}
}
