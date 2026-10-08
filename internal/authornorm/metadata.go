package authornorm

import "errors"

// ErrInvalidSourceMetadata marks a SourceMetadata whose shape violates the
// closed invariants of contract 3.2 (unknown enum value, non-contiguous
// positions, a sequence parent that does not point backwards).
var ErrInvalidSourceMetadata = errors.New("authornorm: source metadata shape is invalid")

// ContributorRole is the closed set of credit roles an FB2 metadata
// extraction produces. document-info/author never yields a credit, so no
// "editor"/"converter" role exists here by construction.
type ContributorRole string

const (
	RoleAuthor     ContributorRole = "author"
	RoleTranslator ContributorRole = "translator"
)

func (r ContributorRole) valid() bool {
	return r == RoleAuthor || r == RoleTranslator
}

// ExtractionOutcome is the closed set of terminal extraction outcomes for one
// book entry (contract 3.2). Error paths are typed errors, not outcomes.
type ExtractionOutcome string

const (
	OutcomeExtracted ExtractionOutcome = "extracted"
	OutcomeNoAuthor  ExtractionOutcome = "extracted_no_author"
)

func (o ExtractionOutcome) valid() bool {
	return o == OutcomeExtracted || o == OutcomeNoAuthor
}

// Contributor is one credit: exactly one non-empty title-info/author or
// title-info/translator XML element. Position is zero-based inside the role
// and preserves XML order; it is consumed only by created credits, so skipped
// empty elements never leave gaps. SourceID is the contributor element's id
// attribute (nil when absent, a pointer to "" when present-but-empty).
type Contributor struct {
	Role     ContributorRole
	Position int
	SourceID *string
	Value    SourceValue
}

// Sequence is one FB2 sequence element in lossless form: document order
// (Index), nesting (Parent is the Index of the enclosing sequence, -1 at top
// level), the name attribute and the nullable number attribute. Contract 3.2
// outputs all sequences, so title-info, src-title-info and publish-info
// sequences are all recorded; contributor credits still come from title-info
// only.
type Sequence struct {
	Index  int
	Parent int
	Name   string
	Number *string
}

// SourceMetadata is the complete lossless output of one metadata-only FB2
// extraction: the snapshot payload plus its provenance echo. It contains no
// canonical or normalized data — only source strings, exactly as the contract
// admits them.
type SourceMetadata struct {
	// Provenance echo of the extractor input.
	BookID           int64
	ArchivePath      string
	EntryName        string
	ExtractorVersion string
	// BookMD5 is the hex MD5 of the raw entry bytes: the echo of the known
	// value when one was supplied, otherwise the value computed by streaming
	// the entry through the hash.
	BookMD5 string

	Title   string
	Lang    *string
	SrcLang *string

	Contributors []Contributor
	Sequences    []Sequence

	ISBNs     []string
	Publisher []string
	City      []string
	Year      []string

	DocumentID      *string
	DocumentVersion *string

	Outcome ExtractionOutcome
}

// Validate checks the closed shape invariants of an assembled SourceMetadata.
// It is a guard for the snapshot boundary, not a re-extraction: the extractor
// builds these invariants by construction, and a violation means a bug or a
// hand-assembled value.
func (m *SourceMetadata) Validate() error {
	if m.BookID <= 0 {
		return ErrInvalidSourceMetadata
	}
	if m.ExtractorVersion == "" {
		return ErrEmptyVersion
	}
	if !m.Outcome.valid() {
		return ErrInvalidSourceMetadata
	}
	nextPosition := map[ContributorRole]int{
		RoleAuthor:     0,
		RoleTranslator: 0,
	}
	for _, c := range m.Contributors {
		if !c.Role.valid() {
			return ErrInvalidSourceMetadata
		}
		if c.Position != nextPosition[c.Role] {
			return ErrInvalidSourceMetadata
		}
		nextPosition[c.Role]++
	}
	for i, s := range m.Sequences {
		if s.Index != i {
			return ErrInvalidSourceMetadata
		}
		if s.Parent < -1 || s.Parent >= s.Index {
			return ErrInvalidSourceMetadata
		}
	}
	if m.Outcome == OutcomeExtracted && nextPosition[RoleAuthor] == 0 {
		return ErrInvalidSourceMetadata
	}
	if m.Outcome == OutcomeNoAuthor && nextPosition[RoleAuthor] > 0 {
		return ErrInvalidSourceMetadata
	}
	return nil
}
